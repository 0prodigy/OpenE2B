package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/0prodigy/OpenE2B/pkg/envd"
)

// Handler holds the HTTP handlers for envd REST API
type Handler struct {
	config      *Config
	envVars     envd.EnvVars
	accessToken string
	defaultUser string
	defaultWorkdir string
}

// Config holds envd configuration
type Config struct {
	Port     int
	RootPath string
}

// NewHandler creates a new envd handler
func NewHandler(config *Config) *Handler {
	return &Handler{
		config:         config,
		envVars:        make(envd.EnvVars),
		defaultUser:    "user",
		defaultWorkdir: "/home/user",
	}
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
	writeJSON(w, status, envd.Error{Code: status, Message: message})
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// GetMetrics handles GET /metrics
func (h *Handler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := h.collectMetrics()
	writeJSON(w, http.StatusOK, metrics)
}

// Init handles POST /init
func (h *Handler) Init(w http.ResponseWriter, r *http.Request) {
	var req envd.InitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Store env vars
	if req.EnvVars != nil {
		for k, v := range req.EnvVars {
			h.envVars[k] = v
		}
	}

	// Store access token
	if req.AccessToken != nil {
		h.accessToken = *req.AccessToken
	}

	// Store default user
	if req.DefaultUser != nil {
		h.defaultUser = *req.DefaultUser
	}

	// Store default workdir
	if req.DefaultWorkdir != nil {
		h.defaultWorkdir = *req.DefaultWorkdir
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetEnvs handles GET /envs
func (h *Handler) GetEnvs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.envVars)
}

// DownloadFile handles GET /files
func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path parameter is required")
		return
	}

	// Resolve path
	fullPath := h.resolvePath(path, r.URL.Query().Get("username"))

	// Read file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "File not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// UploadFile handles POST /files
func (h *Handler) UploadFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path parameter is required")
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
		writeError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "File is required")
		return
	}
	defer file.Close()

	// Resolve path
	fullPath := h.resolvePath(path, r.URL.Query().Get("username"))

	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Create file
	dst, err := os.Create(fullPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer dst.Close()

	// Copy content
	if _, err := io.Copy(dst, file); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return entry info
	info := []envd.EntryInfo{{
		Path: fullPath,
		Name: filepath.Base(fullPath),
		Type: "file",
	}}
	writeJSON(w, http.StatusOK, info)
}

// resolvePath resolves a path, handling relative paths
func (h *Handler) resolvePath(path, username string) string {
	if filepath.IsAbs(path) {
		return path
	}
	
	// Use provided username or default
	user := username
	if user == "" {
		user = h.defaultUser
	}

	// Resolve relative to user home
	home := h.defaultWorkdir
	if user != "" && user != h.defaultUser {
		home = filepath.Join("/home", user)
	}

	return filepath.Join(home, path)
}

// collectMetrics gathers system metrics
func (h *Handler) collectMetrics() envd.Metrics {
	metrics := envd.Metrics{
		Timestamp: time.Now().Unix(),
		CPUCount:  runtime.NumCPU(),
	}

	// Get disk usage
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		metrics.DiskTotal = int64(stat.Blocks) * int64(stat.Bsize)
		metrics.DiskUsed = int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize)
	}

	// Memory stats are platform-specific; use sysinfo on Linux
	// For now, return placeholder values
	metrics.MemTotal = 1024 * 1024 * 1024 // 1GB placeholder
	metrics.MemUsed = 512 * 1024 * 1024   // 512MB placeholder
	metrics.CPUUsedPct = 0.0              // Would need to sample over time

	return metrics
}
