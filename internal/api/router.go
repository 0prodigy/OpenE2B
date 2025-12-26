package api

import (
	"net/http"
	"strings"
)

// Router handles HTTP routing for the API
type Router struct {
	handler *Handler
	mux     *http.ServeMux
}

// NewRouter creates a new router with all routes registered
func NewRouter(handler *Handler) *Router {
	r := &Router{
		handler: handler,
		mux:     http.NewServeMux(),
	}
	r.registerRoutes()
	return r
}

// ServeHTTP implements the http.Handler interface
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) registerRoutes() {
	// Health
	r.mux.HandleFunc("GET /health", r.handler.Health)

	// ============================================================
	// Sandboxes API
	// ============================================================
	r.mux.HandleFunc("GET /sandboxes", r.handler.ListSandboxes)
	r.mux.HandleFunc("POST /sandboxes", r.handler.CreateSandbox)
	r.mux.HandleFunc("GET /v2/sandboxes", r.handler.ListSandboxesV2)
	r.mux.HandleFunc("GET /sandboxes/metrics", r.handler.GetSandboxesMetrics)

	// Sandbox operations
	r.mux.HandleFunc("GET /sandboxes/", r.routeSandbox)
	r.mux.HandleFunc("DELETE /sandboxes/", r.routeSandbox)
	r.mux.HandleFunc("POST /sandboxes/", r.routeSandbox)

	// ============================================================
	// Templates API (V2 SDK Flow)
	// ============================================================
	// Step 1: Create template - POST /v3/templates
	r.mux.HandleFunc("POST /v3/templates", r.handler.CreateTemplateV3)

	// Step 2: Get file upload link - GET /templates/{templateID}/files/{hash}
	// Step 3: Trigger build - POST /v2/templates/{templateID}/builds/{buildID}
	// Step 4: Get build status - GET /templates/{templateID}/builds/{buildID}/status
	r.mux.HandleFunc("GET /templates", r.handler.ListTemplates)
	r.mux.HandleFunc("GET /templates/", r.routeTemplate)
	r.mux.HandleFunc("DELETE /templates/", r.routeTemplate)
	r.mux.HandleFunc("PATCH /templates/", r.routeTemplate)

	// V2 template builds
	r.mux.HandleFunc("POST /v2/templates/", r.routeTemplateV2)

	// ============================================================
	// Admin API
	// ============================================================
	r.mux.HandleFunc("GET /nodes", r.handler.ListNodes)
	r.mux.HandleFunc("GET /nodes/", r.routeNode)
	r.mux.HandleFunc("POST /nodes/", r.routeNode)

	// Teams
	r.mux.HandleFunc("GET /teams", r.handler.ListTeams)
}

// routeSandbox routes sandbox sub-paths
func (r *Router) routeSandbox(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/sandboxes/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodGet:
		if len(parts) == 1 {
			r.handler.GetSandbox(w, req)
		} else if len(parts) == 2 {
			switch parts[1] {
			case "logs":
				r.handler.GetSandboxLogs(w, req)
			case "metrics":
				r.handler.GetSandboxMetrics(w, req)
			default:
				http.NotFound(w, req)
			}
		} else {
			http.NotFound(w, req)
		}

	case http.MethodDelete:
		if len(parts) == 1 {
			r.handler.DeleteSandbox(w, req)
		} else {
			http.NotFound(w, req)
		}

	case http.MethodPost:
		if len(parts) < 2 {
			http.NotFound(w, req)
			return
		}
		switch parts[1] {
		case "pause":
			r.handler.PauseSandbox(w, req)
		case "resume":
			r.handler.ResumeSandbox(w, req)
		case "connect":
			r.handler.ConnectSandbox(w, req)
		case "timeout":
			r.handler.SetSandboxTimeout(w, req)
		case "refreshes":
			r.handler.RefreshSandbox(w, req)
		default:
			http.NotFound(w, req)
		}

	default:
		http.NotFound(w, req)
	}
}

// routeTemplate routes template sub-paths
func (r *Router) routeTemplate(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/templates/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodGet:
		if len(parts) == 1 {
			// GET /templates/{templateID}
			r.handler.GetTemplate(w, req)
		} else if len(parts) == 3 && parts[1] == "files" {
			// GET /templates/{templateID}/files/{hash}
			r.handler.GetFileUploadLink(w, req)
		} else if len(parts) == 4 && parts[1] == "builds" && parts[3] == "status" {
			// GET /templates/{templateID}/builds/{buildID}/status
			r.handler.GetBuildStatus(w, req)
		} else {
			http.NotFound(w, req)
		}

	case http.MethodDelete:
		if len(parts) == 1 {
			r.handler.DeleteTemplate(w, req)
		} else {
			http.NotFound(w, req)
		}

	case http.MethodPatch:
		if len(parts) == 1 {
			r.handler.UpdateTemplate(w, req)
		} else {
			http.NotFound(w, req)
		}

	default:
		http.NotFound(w, req)
	}
}

// routeTemplateV2 routes v2 template sub-paths
func (r *Router) routeTemplateV2(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/v2/templates/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodPost:
		if len(parts) == 3 && parts[1] == "builds" {
			// POST /v2/templates/{templateID}/builds/{buildID}
			r.handler.StartBuildV2(w, req)
		} else {
			http.NotFound(w, req)
		}

	default:
		http.NotFound(w, req)
	}
}

// routeNode routes node sub-paths
func (r *Router) routeNode(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/nodes/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodGet:
		if len(parts) == 1 {
			r.handler.GetNode(w, req)
		} else {
			http.NotFound(w, req)
		}

	case http.MethodPost:
		if len(parts) == 1 {
			r.handler.UpdateNodeStatus(w, req)
		} else {
			http.NotFound(w, req)
		}

	default:
		http.NotFound(w, req)
	}
}
