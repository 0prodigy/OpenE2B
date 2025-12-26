package api

import (
	"net/http"
)

// Router handles HTTP routing for envd REST API
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
	r.mux.HandleFunc("GET /health", r.handler.Health)
	r.mux.HandleFunc("GET /metrics", r.handler.GetMetrics)
	r.mux.HandleFunc("POST /init", r.handler.Init)
	r.mux.HandleFunc("GET /envs", r.handler.GetEnvs)
	r.mux.HandleFunc("GET /files", r.handler.DownloadFile)
	r.mux.HandleFunc("POST /files", r.handler.UploadFile)
}
