package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/0prodigy/OpenE2B/pkg/envd/api"
	"github.com/0prodigy/OpenE2B/pkg/envd/connect"
)

func main() {
	// Load configuration from environment
	config := loadConfig()

	log.Printf("Starting envd daemon...")
	log.Printf("  Port: %d", config.Port)
	log.Printf("  Root Path: %s", config.RootPath)

	// Create main mux for all routes
	mux := http.NewServeMux()

	// Create REST API handler and router
	restHandler := api.NewHandler(config)
	restRouter := api.NewRouter(restHandler)

	// Register REST routes
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("POST /init", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("GET /envs", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("POST /files", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })

	// Create and register Connect-RPC handlers
	fsHandler := connect.NewFilesystemHandler()
	fsHandler.Register(mux)

	procHandler := connect.NewProcessHandler()
	procHandler.Register(mux)

	// Apply middleware chain
	var h http.Handler = mux
	h = api.AuthMiddleware(restHandler)(h)
	h = api.CORSMiddleware(h)
	h = loggingMiddleware(h)

	// Start server
	addr := fmt.Sprintf(":%d", config.Port)
	log.Printf("Listening on %s", addr)

	if err := http.ListenAndServe(addr, h); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func loadConfig() *api.Config {
	port := getEnvInt("E2B_ENVD_PORT", 49983)
	rootPath := getEnv("E2B_ENVD_ROOT", "/")

	return &api.Config{
		Port:     port,
		RootPath: rootPath,
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
