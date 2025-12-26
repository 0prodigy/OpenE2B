package auth

import (
	"context"
	"net/http"
)

// Auth header constants
const (
	HeaderAPIKey        = "X-API-Key"
	HeaderAccessToken   = "Authorization"
	HeaderSupabaseToken = "X-Supabase-Token"
	HeaderSupabaseTeam  = "X-Supabase-Team"
	HeaderAdminToken    = "X-Admin-Token"
	HeaderEnvdToken     = "X-Access-Token"
	HeaderKeepalive     = "Keepalive-Ping-Interval"
)

// Context keys for auth information
type contextKey string

const (
	ContextKeyUserID   contextKey = "userID"
	ContextKeyTeamID   contextKey = "teamID"
	ContextKeyAPIKeyID contextKey = "apiKeyID"
	ContextKeyIsAdmin  contextKey = "isAdmin"
)

// AuthInfo holds authentication context
type AuthInfo struct {
	UserID   string
	TeamID   string
	APIKeyID string
	IsAdmin  bool
}

// GetAuthInfo retrieves auth info from context
func GetAuthInfo(ctx context.Context) *AuthInfo {
	info := &AuthInfo{}
	if v, ok := ctx.Value(ContextKeyUserID).(string); ok {
		info.UserID = v
	}
	if v, ok := ctx.Value(ContextKeyTeamID).(string); ok {
		info.TeamID = v
	}
	if v, ok := ctx.Value(ContextKeyAPIKeyID).(string); ok {
		info.APIKeyID = v
	}
	if v, ok := ctx.Value(ContextKeyIsAdmin).(bool); ok {
		info.IsAdmin = v
	}
	return info
}

// AllowAllMiddleware is a passthrough auth middleware for Phase 1.
// It sets default auth context values without validating credentials.
// Future versions will implement proper API key/bearer/Supabase validation.
func AllowAllMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Check for admin token (still allow-all for now)
		if r.Header.Get(HeaderAdminToken) != "" {
			ctx = context.WithValue(ctx, ContextKeyIsAdmin, true)
		}

		// Extract API key if present (just store, don't validate)
		if apiKey := r.Header.Get(HeaderAPIKey); apiKey != "" {
			ctx = context.WithValue(ctx, ContextKeyAPIKeyID, apiKey)
		}

		// Extract Supabase team if present
		if teamID := r.Header.Get(HeaderSupabaseTeam); teamID != "" {
			ctx = context.WithValue(ctx, ContextKeyTeamID, teamID)
		}

		// Set default team for development
		if ctx.Value(ContextKeyTeamID) == nil {
			ctx = context.WithValue(ctx, ContextKeyTeamID, "default")
		}

		// Set default user for development
		ctx = context.WithValue(ctx, ContextKeyUserID, "dev-user")

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdminMiddleware requires admin token (allow-all for now)
func RequireAdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For Phase 1, we allow all admin requests
		// Future: validate X-Admin-Token header
		ctx := context.WithValue(r.Context(), ContextKeyIsAdmin, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CORSMiddleware adds CORS headers
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Supabase-Token, X-Supabase-Team, X-Admin-Token, X-Access-Token, Keepalive-Ping-Interval")
		w.Header().Set("Access-Control-Expose-Headers", "x-next-token")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
