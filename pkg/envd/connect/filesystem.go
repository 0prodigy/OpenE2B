package connect

import (
	"encoding/json"
	"net/http"

	"github.com/0prodigy/OpenE2B/pkg/envd/filesystem"
)

// FilesystemHandler provides HTTP/Connect handlers for filesystem operations
type FilesystemHandler struct {
	service *filesystem.Service
}

// NewFilesystemHandler creates a new filesystem handler
func NewFilesystemHandler() *FilesystemHandler {
	return &FilesystemHandler{
		service: filesystem.NewService(),
	}
}

// Request/Response types matching the proto definitions

type StatRequest struct {
	Path string `json:"path"`
}

type StatResponse struct {
	Entry *EntryInfo `json:"entry"`
}

type MakeDirRequest struct {
	Path string `json:"path"`
}

type MakeDirResponse struct {
	Entry *EntryInfo `json:"entry"`
}

type MoveRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type MoveResponse struct {
	Entry *EntryInfo `json:"entry"`
}

type ListDirRequest struct {
	Path  string `json:"path"`
	Depth uint32 `json:"depth"`
}

type ListDirResponse struct {
	Entries []EntryInfo `json:"entries"`
}

type RemoveRequest struct {
	Path string `json:"path"`
}

type CreateWatcherRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type CreateWatcherResponse struct {
	WatcherID string `json:"watcher_id"`
}

type GetWatcherEventsRequest struct {
	WatcherID string `json:"watcher_id"`
}

type GetWatcherEventsResponse struct {
	Events []FilesystemEvent `json:"events"`
}

type RemoveWatcherRequest struct {
	WatcherID string `json:"watcher_id"`
}

type EntryInfo struct {
	Name          string  `json:"name"`
	Type          int32   `json:"type"`
	Path          string  `json:"path"`
	Size          int64   `json:"size"`
	Mode          uint32  `json:"mode"`
	Permissions   string  `json:"permissions"`
	Owner         string  `json:"owner"`
	Group         string  `json:"group"`
	ModifiedTime  string  `json:"modified_time"`
	SymlinkTarget *string `json:"symlink_target,omitempty"`
}

type FilesystemEvent struct {
	Name string `json:"name"`
	Type int32  `json:"type"`
}

// Register registers all filesystem routes
func (h *FilesystemHandler) Register(mux *http.ServeMux) {
	// Connect-style paths following the proto service definition
	mux.HandleFunc("POST /filesystem.Filesystem/Stat", h.Stat)
	mux.HandleFunc("POST /filesystem.Filesystem/MakeDir", h.MakeDir)
	mux.HandleFunc("POST /filesystem.Filesystem/Move", h.Move)
	mux.HandleFunc("POST /filesystem.Filesystem/ListDir", h.ListDir)
	mux.HandleFunc("POST /filesystem.Filesystem/Remove", h.Remove)
	mux.HandleFunc("POST /filesystem.Filesystem/CreateWatcher", h.CreateWatcher)
	mux.HandleFunc("POST /filesystem.Filesystem/GetWatcherEvents", h.GetWatcherEvents)
	mux.HandleFunc("POST /filesystem.Filesystem/RemoveWatcher", h.RemoveWatcher)
}

// Stat handles stat requests
func (h *FilesystemHandler) Stat(w http.ResponseWriter, r *http.Request) {
	var req StatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, err := h.service.Stat(r.Context(), req.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, StatResponse{Entry: toConnectEntryInfo(entry)})
}

// MakeDir handles mkdir requests
func (h *FilesystemHandler) MakeDir(w http.ResponseWriter, r *http.Request) {
	var req MakeDirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, err := h.service.MakeDir(r.Context(), req.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, MakeDirResponse{Entry: toConnectEntryInfo(entry)})
}

// Move handles move/rename requests
func (h *FilesystemHandler) Move(w http.ResponseWriter, r *http.Request) {
	var req MoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, err := h.service.Move(r.Context(), req.Source, req.Destination)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, MoveResponse{Entry: toConnectEntryInfo(entry)})
}

// ListDir handles directory listing requests
func (h *FilesystemHandler) ListDir(w http.ResponseWriter, r *http.Request) {
	var req ListDirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := h.service.ListDir(r.Context(), req.Path, req.Depth)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var result []EntryInfo
	for _, e := range entries {
		result = append(result, *toConnectEntryInfo(&e))
	}

	writeJSON(w, http.StatusOK, ListDirResponse{Entries: result})
}

// Remove handles file/directory removal requests
func (h *FilesystemHandler) Remove(w http.ResponseWriter, r *http.Request) {
	var req RemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.service.Remove(r.Context(), req.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, struct{}{})
}

// CreateWatcher handles watcher creation
func (h *FilesystemHandler) CreateWatcher(w http.ResponseWriter, r *http.Request) {
	var req CreateWatcherRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	watcherID, err := h.service.CreateWatcher(r.Context(), req.Path, req.Recursive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, CreateWatcherResponse{WatcherID: watcherID})
}

// GetWatcherEvents handles watcher event retrieval
func (h *FilesystemHandler) GetWatcherEvents(w http.ResponseWriter, r *http.Request) {
	var req GetWatcherEventsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	events, err := h.service.GetWatcherEvents(r.Context(), req.WatcherID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var result []FilesystemEvent
	for _, e := range events {
		result = append(result, FilesystemEvent{
			Name: e.Name,
			Type: int32(e.Type),
		})
	}

	writeJSON(w, http.StatusOK, GetWatcherEventsResponse{Events: result})
}

// RemoveWatcher handles watcher removal
func (h *FilesystemHandler) RemoveWatcher(w http.ResponseWriter, r *http.Request) {
	var req RemoveWatcherRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.service.RemoveWatcher(r.Context(), req.WatcherID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, struct{}{})
}

func toConnectEntryInfo(e *filesystem.EntryInfo) *EntryInfo {
	if e == nil {
		return nil
	}
	return &EntryInfo{
		Name:          e.Name,
		Type:          int32(e.Type),
		Path:          e.Path,
		Size:          e.Size,
		Mode:          e.Mode,
		Permissions:   e.Permissions,
		Owner:         e.Owner,
		Group:         e.Group,
		ModifiedTime:  e.ModifiedTime.Format("2006-01-02T15:04:05Z"),
		SymlinkTarget: e.SymlinkTarget,
	}
}
