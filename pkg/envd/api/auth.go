package api

import (
	"net/http"
)

// Header constants
const (
	HeaderAccessToken = "X-Access-Token"
)

// AuthMiddleware validates X-Access-Token header
// For Phase 2, this is allow-all but logs tokens
func AuthMiddleware(handler *Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health endpoint
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			// Check access token if configured
			if handler.accessToken != "" {
				token := r.Header.Get(HeaderAccessToken)
				if token != handler.accessToken {
					// Also check query params for signed URLs
					if r.URL.Query().Get("signature") == "" {
						writeError(w, http.StatusUnauthorized, "Invalid access token")
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CORSMiddleware adds CORS headers
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Access-Token")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
