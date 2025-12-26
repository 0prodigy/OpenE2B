package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0prodigy/OpenE2B/internal/build"
	"github.com/0prodigy/OpenE2B/internal/orchestrator"
)

// BuildService interface for build operations
type BuildService interface {
	StartBuild(ctx context.Context, templateID, buildID string, config *build.Config) error
}

// FileStorage interface for file upload operations
type FileStorage interface {
	GetUploadURL(templateID, hash string) (url string, present bool, err error)
}

// Handler holds the HTTP handlers for the API
type Handler struct {
	store        *Store
	config       *Config
	buildService BuildService
	fileStorage  FileStorage
	scheduler    *orchestrator.Scheduler
}

// Config holds server configuration
type Config struct {
	Domain   string
	APIPort  int
	EnvdPort int
}

// NewHandler creates a new API handler
func NewHandler(store *Store, config *Config) *Handler {
	return &Handler{
		store:  store,
		config: config,
	}
}

// SetBuildService sets the build service for the handler
func (h *Handler) SetBuildService(svc BuildService) {
	h.buildService = svc
}

// SetFileStorage sets the file storage for file uploads
func (h *Handler) SetFileStorage(storage FileStorage) {
	h.fileStorage = storage
}

// SetScheduler sets the orchestrator scheduler for sandbox operations
func (h *Handler) SetScheduler(scheduler *orchestrator.Scheduler) {
	h.scheduler = scheduler
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// writeError writes an error response
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, Error{Code: status, Message: message})
}

// ============================================================
// Health
// ============================================================

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ============================================================
// Sandboxes
// ============================================================

// ListSandboxes handles GET /sandboxes
func (h *Handler) ListSandboxes(w http.ResponseWriter, r *http.Request) {
	metadata := parseMetadataQuery(r.URL.Query().Get("metadata"))
	records := h.store.ListSandboxes(metadata, nil)

	sandboxes := make([]ListedSandbox, 0, len(records))
	for _, rec := range records {
		sandboxes = append(sandboxes, toListedSandbox(rec))
	}

	writeJSON(w, http.StatusOK, sandboxes)
}

// ListSandboxesV2 handles GET /v2/sandboxes with pagination and state filter
func (h *Handler) ListSandboxesV2(w http.ResponseWriter, r *http.Request) {
	metadata := parseMetadataQuery(r.URL.Query().Get("metadata"))

	var states []SandboxState
	stateParam := r.URL.Query().Get("state")
	if stateParam != "" {
		for _, s := range strings.Split(stateParam, ",") {
			states = append(states, SandboxState(s))
		}
	}

	records := h.store.ListSandboxes(metadata, states)

	sandboxes := make([]ListedSandbox, 0, len(records))
	for _, rec := range records {
		sandboxes = append(sandboxes, toListedSandbox(rec))
	}

	writeJSON(w, http.StatusOK, sandboxes)
}

// CreateSandbox handles POST /sandboxes
func (h *Handler) CreateSandbox(w http.ResponseWriter, r *http.Request) {
	var req NewSandbox
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.TemplateID == "" {
		writeError(w, http.StatusBadRequest, "templateID is required")
		return
	}

	templateRec, ok := h.store.GetTemplate(req.TemplateID)
	if !ok {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	// Create sandbox record in store first
	record, err := h.store.CreateSandbox(req, templateRec, h.config.Domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// If scheduler is configured, actually create the sandbox container
	if h.scheduler != nil {
		timeout := 300 // default 5 minutes
		if req.Timeout != nil {
			timeout = *req.Timeout
		}

		spec := orchestrator.SandboxSpec{
			ID:         record.SandboxID,
			TemplateID: record.TemplateID,
			BuildID:    templateRec.BuildID,
			CPUCount:   record.CPUCount,
			MemoryMB:   record.MemoryMB,
			DiskSizeMB: record.DiskSizeMB,
			EnvVars:    req.EnvVars,
			Metadata:   req.Metadata,
			Timeout:    time.Duration(timeout) * time.Second,
			AutoPause:  req.AutoPause != nil && *req.AutoPause,
			EnvdToken:  record.EnvdAccessToken,
		}

		_, err := h.scheduler.Schedule(r.Context(), spec)
		if err != nil {
			// Clean up the store record on failure
			h.store.DeleteSandbox(record.SandboxID)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create sandbox: %v", err))
			return
		}
	}

	sandbox := toSandbox(record)
	writeJSON(w, http.StatusCreated, sandbox)
}

// GetSandbox handles GET /sandboxes/{sandboxID}
func (h *Handler) GetSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")

	record, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	detail := toSandboxDetail(record)
	writeJSON(w, http.StatusOK, detail)
}

// DeleteSandbox handles DELETE /sandboxes/{sandboxID}
func (h *Handler) DeleteSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")

	// Stop the actual container if scheduler is configured
	if h.scheduler != nil {
		if err := h.scheduler.Stop(r.Context(), sandboxID); err != nil {
			// Log but don't fail - the sandbox might have already been stopped
			fmt.Printf("[api] Warning: failed to stop sandbox container: %v\n", err)
		}
	}

	if !h.store.DeleteSandbox(sandboxID) {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// PauseSandbox handles POST /sandboxes/{sandboxID}/pause
func (h *Handler) PauseSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/pause")

	record, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	if record.State == SandboxStatePaused {
		writeError(w, http.StatusConflict, "Sandbox is already paused")
		return
	}

	// Pause the actual container if scheduler is configured
	if h.scheduler != nil {
		if err := h.scheduler.Pause(r.Context(), sandboxID); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to pause sandbox: %v", err))
			return
		}
	}

	h.store.UpdateSandboxState(sandboxID, SandboxStatePaused)
	w.WriteHeader(http.StatusNoContent)
}

// ResumeSandbox handles POST /sandboxes/{sandboxID}/resume
func (h *Handler) ResumeSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/resume")

	record, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	if record.State != SandboxStatePaused {
		writeError(w, http.StatusConflict, "Sandbox is not paused")
		return
	}

	var req ResumedSandbox
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Resume the actual container if scheduler is configured
	if h.scheduler != nil {
		if err := h.scheduler.Resume(r.Context(), sandboxID); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to resume sandbox: %v", err))
			return
		}
	}

	h.store.UpdateSandboxState(sandboxID, SandboxStateRunning)
	if req.Timeout != nil {
		h.store.UpdateSandboxTimeout(sandboxID, *req.Timeout)
	}

	record, _ = h.store.GetSandbox(sandboxID)
	sandbox := toSandbox(record)
	writeJSON(w, http.StatusCreated, sandbox)
}

// ConnectSandbox handles POST /sandboxes/{sandboxID}/connect
func (h *Handler) ConnectSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/connect")

	record, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	var req ConnectSandbox
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	wasResumed := record.State == SandboxStatePaused
	if wasResumed {
		h.store.UpdateSandboxState(sandboxID, SandboxStateRunning)
	}

	h.store.UpdateSandboxTimeout(sandboxID, req.Timeout)

	record, _ = h.store.GetSandbox(sandboxID)
	sandbox := toSandbox(record)

	if wasResumed {
		writeJSON(w, http.StatusCreated, sandbox)
	} else {
		writeJSON(w, http.StatusOK, sandbox)
	}
}

// SetSandboxTimeout handles POST /sandboxes/{sandboxID}/timeout
func (h *Handler) SetSandboxTimeout(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/timeout")

	_, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	var req SandboxTimeout
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	h.store.UpdateSandboxTimeout(sandboxID, req.Timeout)
	w.WriteHeader(http.StatusNoContent)
}

// RefreshSandbox handles POST /sandboxes/{sandboxID}/refreshes
func (h *Handler) RefreshSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/refreshes")

	_, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	var req SandboxRefresh
	json.NewDecoder(r.Body).Decode(&req)

	duration := 60
	if req.Duration != nil {
		duration = *req.Duration
	}

	h.store.UpdateSandboxTimeout(sandboxID, duration)
	w.WriteHeader(http.StatusNoContent)
}

// GetSandboxLogs handles GET /sandboxes/{sandboxID}/logs
func (h *Handler) GetSandboxLogs(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/logs")

	_, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	logs := SandboxLogs{
		Logs:       []SandboxLog{},
		LogEntries: []SandboxLogEntry{},
	}
	writeJSON(w, http.StatusOK, logs)
}

// GetSandboxMetrics handles GET /sandboxes/{sandboxID}/metrics
func (h *Handler) GetSandboxMetrics(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractPathParam(r.URL.Path, "/sandboxes/")
	sandboxID = strings.TrimSuffix(sandboxID, "/metrics")

	_, ok := h.store.GetSandbox(sandboxID)
	if !ok {
		writeError(w, http.StatusNotFound, "Sandbox not found")
		return
	}

	writeJSON(w, http.StatusOK, []SandboxMetric{})
}

// GetSandboxesMetrics handles GET /sandboxes/metrics
func (h *Handler) GetSandboxesMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, SandboxesWithMetrics{
		Sandboxes: make(map[string]SandboxMetric),
	})
}

// ============================================================
// Templates API (V2 SDK Flow)
// ============================================================

// ListTemplates handles GET /templates
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	teamID := r.URL.Query().Get("teamID")
	var teamIDPtr *string
	if teamID != "" {
		teamIDPtr = &teamID
	}

	records := h.store.ListTemplates(teamIDPtr)

	templates := make([]Template, 0, len(records))
	for _, rec := range records {
		templates = append(templates, toTemplate(rec))
	}

	writeJSON(w, http.StatusOK, templates)
}

// CreateTemplateV3 handles POST /v3/templates
// SDK Step 1: Create template record
func (h *Handler) CreateTemplateV3(w http.ResponseWriter, r *http.Request) {
	var req TemplateBuildRequestV3
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Alias == "" {
		writeError(w, http.StatusBadRequest, "alias is required")
		return
	}

	templateRec, buildRec, err := h.store.CreateTemplate(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := TemplateRequestResponseV3{
		TemplateID: templateRec.TemplateID,
		BuildID:    buildRec.BuildID,
		Public:     templateRec.Public,
		Aliases:    templateRec.Aliases,
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// GetFileUploadLink handles GET /templates/{templateID}/files/{hash}
// SDK Step 2: Get presigned URL for file upload
func (h *Handler) GetFileUploadLink(w http.ResponseWriter, r *http.Request) {
	// Parse path: /templates/{templateID}/files/{hash}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	templateID := parts[1]
	hash := parts[3]

	_, ok := h.store.GetTemplate(templateID)
	if !ok {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	// Check if file storage is configured
	if h.fileStorage == nil {
		// For local development, return that file is already present (no upload needed)
		// This allows the SDK to proceed without actual file storage
		resp := TemplateBuildFileUpload{
			Present: true,
			URL:     nil,
		}
		writeJSON(w, http.StatusCreated, resp)
		return
	}

	uploadURL, present, err := h.fileStorage.GetUploadURL(templateID, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := TemplateBuildFileUpload{
		Present: present,
	}
	if !present && uploadURL != "" {
		resp.URL = &uploadURL
	}
	writeJSON(w, http.StatusCreated, resp)
}

// StartBuildV2 handles POST /v2/templates/{templateID}/builds/{buildID}
// SDK Step 3: Trigger the build with steps
func (h *Handler) StartBuildV2(w http.ResponseWriter, r *http.Request) {
	// Parse path: /v2/templates/{templateID}/builds/{buildID}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	templateID := parts[2]
	buildID := parts[4]

	var req TemplateBuildStartV2
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	templateRec, ok := h.store.GetTemplate(templateID)
	if !ok {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	buildRec, ok := h.store.GetBuild(buildID)
	if !ok {
		writeError(w, http.StatusNotFound, "Build not found")
		return
	}

	// Check build is in waiting state
	if buildRec.Status != TemplateBuildStatusWaiting {
		writeError(w, http.StatusConflict, "Build is not in waiting state")
		return
	}

	// Transition to building state
	if !h.store.UpdateBuildStatus(buildID, TemplateBuildStatusBuilding) {
		writeError(w, http.StatusInternalServerError, "Failed to update build status")
		return
	}

	// Build configuration from request
	config := &build.Config{
		Force: req.Force != nil && *req.Force,
	}
	if req.FromImage != nil {
		config.FromImage = *req.FromImage
	}
	if req.FromTemplate != nil {
		config.FromTemplate = *req.FromTemplate
	}
	if req.FromImageRegistry != nil {
		config.RegistryURL = req.FromImageRegistry.URL
		if req.FromImageRegistry.Username != nil {
			config.RegistryUsername = *req.FromImageRegistry.Username
		}
		if req.FromImageRegistry.Password != nil {
			config.RegistryPassword = *req.FromImageRegistry.Password
		}
	}
	if req.StartCmd != nil {
		config.StartCmd = *req.StartCmd
	}
	if req.ReadyCmd != nil {
		config.ReadyCmd = *req.ReadyCmd
	}

	// Convert steps
	for _, step := range req.Steps {
		filesHash := ""
		if step.FilesHash != nil {
			filesHash = *step.FilesHash
		}
		config.Steps = append(config.Steps, build.Step{
			Type:      step.Type,
			Args:      step.Args,
			FilesHash: filesHash,
			Force:     step.Force != nil && *step.Force,
		})
	}

	// Start the build asynchronously
	if h.buildService != nil {
		fmt.Printf("[api] Triggering build for template=%s build=%s\n", templateID, buildID)
		go func() {
			if err := h.buildService.StartBuild(context.Background(), templateRec.TemplateID, buildID, config); err != nil {
				fmt.Printf("[api] Build failed to start: %v\n", err)
			}
		}()
	}

	w.WriteHeader(http.StatusAccepted)
}

// GetBuildStatus handles GET /templates/{templateID}/builds/{buildID}/status
// SDK Step 4: Poll for build status and logs
func (h *Handler) GetBuildStatus(w http.ResponseWriter, r *http.Request) {
	// Parse path: /templates/{templateID}/builds/{buildID}/status
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	templateID := parts[1]
	buildID := parts[3]

	_, ok := h.store.GetTemplate(templateID)
	if !ok {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	buildRec, ok := h.store.GetBuild(buildID)
	if !ok {
		writeError(w, http.StatusNotFound, "Build not found")
		return
	}

	// Parse logsOffset query param
	logsOffset := 0
	if offsetStr := r.URL.Query().Get("logsOffset"); offsetStr != "" {
		fmt.Sscanf(offsetStr, "%d", &logsOffset)
	}

	// Get logs starting from offset
	logs := buildRec.Logs
	logEntries := buildRec.LogEntries
	if logsOffset > 0 && logsOffset < len(logEntries) {
		logEntries = logEntries[logsOffset:]
	} else if logsOffset >= len(logEntries) {
		logEntries = []BuildLogEntry{}
	}

	resp := TemplateBuildInfo{
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     buildRec.Status,
		Logs:       logs,
		LogEntries: logEntries,
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetTemplate handles GET /templates/{templateID}
func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := extractPathParam(r.URL.Path, "/templates/")

	templateRec, ok := h.store.GetTemplate(templateID)
	if !ok {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	builds := h.store.GetBuildsForTemplate(templateRec.TemplateID)
	templateBuilds := make([]TemplateBuild, 0, len(builds))
	for _, b := range builds {
		templateBuilds = append(templateBuilds, toBuild(b))
	}

	resp := TemplateWithBuilds{
		TemplateID:    templateRec.TemplateID,
		Public:        templateRec.Public,
		Aliases:       templateRec.Aliases,
		CreatedAt:     templateRec.CreatedAt,
		UpdatedAt:     templateRec.UpdatedAt,
		LastSpawnedAt: templateRec.LastSpawnedAt,
		SpawnCount:    templateRec.SpawnCount,
		Builds:        templateBuilds,
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteTemplate handles DELETE /templates/{templateID}
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := extractPathParam(r.URL.Path, "/templates/")

	if !h.store.DeleteTemplate(templateID) {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UpdateTemplate handles PATCH /templates/{templateID}
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := extractPathParam(r.URL.Path, "/templates/")

	var req TemplateUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !h.store.UpdateTemplate(templateID, req) {
		writeError(w, http.StatusNotFound, "Template not found")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ============================================================
// Admin API
// ============================================================

// ListNodes handles GET /nodes
func (h *Handler) ListNodes(w http.ResponseWriter, r *http.Request) {
	records := h.store.ListNodes()

	nodes := make([]Node, 0, len(records))
	for _, rec := range records {
		nodes = append(nodes, rec.Node)
	}

	writeJSON(w, http.StatusOK, nodes)
}

// GetNode handles GET /nodes/{nodeID}
func (h *Handler) GetNode(w http.ResponseWriter, r *http.Request) {
	nodeID := extractPathParam(r.URL.Path, "/nodes/")

	rec, ok := h.store.GetNode(nodeID)
	if !ok {
		writeError(w, http.StatusNotFound, "Node not found")
		return
	}

	var sandboxes []ListedSandbox
	for _, sbID := range rec.Sandboxes {
		if sbRec, ok := h.store.GetSandbox(sbID); ok {
			sandboxes = append(sandboxes, toListedSandbox(sbRec))
		}
	}

	detail := NodeDetail{
		ClusterID:         rec.ClusterID,
		Version:           rec.Version,
		Commit:            rec.Commit,
		ID:                rec.ID,
		ServiceInstanceID: rec.ServiceInstanceID,
		NodeID:            rec.NodeID,
		Status:            rec.Status,
		Sandboxes:         sandboxes,
		Metrics:           rec.Metrics,
		CachedBuilds:      rec.CachedBuilds,
		CreateSuccesses:   rec.CreateSuccesses,
		CreateFails:       rec.CreateFails,
	}
	writeJSON(w, http.StatusOK, detail)
}

// UpdateNodeStatus handles POST /nodes/{nodeID}
func (h *Handler) UpdateNodeStatus(w http.ResponseWriter, r *http.Request) {
	nodeID := extractPathParam(r.URL.Path, "/nodes/")

	var req NodeStatusChange
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !h.store.UpdateNodeStatus(nodeID, req.Status) {
		writeError(w, http.StatusNotFound, "Node not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListTeams handles GET /teams
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	teams := []Team{
		{
			TeamID:    "default",
			Name:      "Default Team",
			APIKey:    "e2b_default_key",
			IsDefault: true,
		},
	}
	writeJSON(w, http.StatusOK, teams)
}

// ============================================================
// Helper Functions
// ============================================================

func parseMetadataQuery(q string) map[string]string {
	if q == "" {
		return nil
	}
	result := make(map[string]string)
	values, err := url.ParseQuery(q)
	if err != nil {
		return nil
	}
	for k, v := range values {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}

func extractPathParam(path, prefix string) string {
	p := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(p, "/"); idx != -1 {
		p = p[:idx]
	}
	return p
}

func toSandbox(rec *SandboxRecord) Sandbox {
	return Sandbox{
		TemplateID:      rec.TemplateID,
		SandboxID:       rec.SandboxID,
		Alias:           rec.Alias,
		ClientID:        rec.ClientID,
		EnvdVersion:     rec.EnvdVersion,
		EnvdAccessToken: &rec.EnvdAccessToken,
		Domain:          &rec.Domain,
	}
}

func toSandboxDetail(rec *SandboxRecord) SandboxDetail {
	return SandboxDetail{
		TemplateID:      rec.TemplateID,
		Alias:           rec.Alias,
		SandboxID:       rec.SandboxID,
		ClientID:        rec.ClientID,
		StartedAt:       rec.StartedAt,
		EndAt:           rec.EndAt,
		EnvdVersion:     rec.EnvdVersion,
		EnvdAccessToken: &rec.EnvdAccessToken,
		Domain:          &rec.Domain,
		CPUCount:        rec.CPUCount,
		MemoryMB:        rec.MemoryMB,
		DiskSizeMB:      rec.DiskSizeMB,
		Metadata:        rec.Metadata,
		State:           rec.State,
	}
}

func toListedSandbox(rec *SandboxRecord) ListedSandbox {
	return ListedSandbox{
		TemplateID:  rec.TemplateID,
		Alias:       rec.Alias,
		SandboxID:   rec.SandboxID,
		ClientID:    rec.ClientID,
		StartedAt:   rec.StartedAt,
		EndAt:       rec.EndAt,
		CPUCount:    rec.CPUCount,
		MemoryMB:    rec.MemoryMB,
		DiskSizeMB:  rec.DiskSizeMB,
		Metadata:    rec.Metadata,
		State:       rec.State,
		EnvdVersion: rec.EnvdVersion,
	}
}

func toTemplate(rec *TemplateRecord) Template {
	return Template{
		TemplateID:    rec.TemplateID,
		BuildID:       rec.BuildID,
		CPUCount:      rec.CPUCount,
		MemoryMB:      rec.MemoryMB,
		DiskSizeMB:    rec.DiskSizeMB,
		Public:        rec.Public,
		Aliases:       rec.Aliases,
		CreatedAt:     rec.CreatedAt,
		UpdatedAt:     rec.UpdatedAt,
		CreatedBy:     rec.CreatedBy,
		LastSpawnedAt: rec.LastSpawnedAt,
		SpawnCount:    rec.SpawnCount,
		BuildCount:    rec.BuildCount,
		EnvdVersion:   rec.EnvdVersion,
		BuildStatus:   rec.BuildStatus,
	}
}

func toBuild(rec *BuildRecord) TemplateBuild {
	return TemplateBuild{
		BuildID:     rec.BuildID,
		Status:      rec.Status,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
		FinishedAt:  rec.FinishedAt,
		CPUCount:    rec.CPUCount,
		MemoryMB:    rec.MemoryMB,
		DiskSizeMB:  rec.DiskSizeMB,
		EnvdVersion: rec.EnvdVersion,
	}
}
