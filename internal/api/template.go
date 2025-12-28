package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/0prodigy/OpenE2B/internal/templatebuild"
)

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
	config := &templatebuild.Config{
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
		config.Steps = append(config.Steps, templatebuild.Step{
			Type:      step.Type,
			Args:      step.Args,
			FilesHash: filesHash,
			Force:     step.Force != nil && *step.Force,
		})
	}

	// Start the build asynchronously
	if h.buildService != nil {
		log.Printf("[api] Triggering build for template=%s build=%s", templateID, buildID)
		go func() {
			if err := h.buildService.StartBuild(context.Background(), templateRec.TemplateID, buildID, config); err != nil {
				log.Printf("[api] Build failed to start: %v", err)
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
