package connect

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents an error in Connect format
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	code := "unknown"
	switch status {
	case http.StatusBadRequest:
		code = "invalid_argument"
	case http.StatusNotFound:
		code = "not_found"
	case http.StatusInternalServerError:
		code = "internal"
	case http.StatusUnauthorized:
		code = "unauthenticated"
	case http.StatusForbidden:
		code = "permission_denied"
	}
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}
