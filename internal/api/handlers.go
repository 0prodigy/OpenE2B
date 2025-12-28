package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/0prodigy/OpenE2B/internal/scheduler"
	"github.com/0prodigy/OpenE2B/internal/templatebuild"
)

// BuildService interface for build operations
type BuildService interface {
	StartBuild(ctx context.Context, templateID, buildID string, config *templatebuild.Config) error
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
	scheduler    *scheduler.Scheduler
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

// SetScheduler sets the scheduler for sandbox operations
func (h *Handler) SetScheduler(sched *scheduler.Scheduler) {
	h.scheduler = sched
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
