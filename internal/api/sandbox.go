package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/0prodigy/OpenE2B/internal/scheduler"
)

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

		spec := scheduler.SandboxSpec{
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
			log.Printf("[api] Warning: failed to stop sandbox container: %v", err)
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
