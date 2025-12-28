package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/0prodigy/OpenE2B/pkg/envd/api"
	"github.com/0prodigy/OpenE2B/pkg/envd/rpc"
	"github.com/0prodigy/OpenE2B/pkg/proto/filesystem/filesystemconnect"
	"github.com/0prodigy/OpenE2B/pkg/proto/process/processconnect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Load configuration from environment
	config := loadConfig()

	log.Printf("Starting envd daemon...")
	log.Printf("  Port: %d", config.Port)
	log.Printf("  Root Path: %s", config.RootPath)

	// Create main mux for all routes
	mux := http.NewServeMux()

	// Create REST API handler for file upload/download
	restHandler := api.NewHandler(config)
	restRouter := api.NewRouter(restHandler)

	// Register REST routes (file upload/download, health, etc.)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("POST /init", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("GET /envs", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })
	mux.HandleFunc("POST /files", func(w http.ResponseWriter, r *http.Request) { restRouter.ServeHTTP(w, r) })

	// Create and register proper Connect-RPC handlers
	// These implement the full Connect-RPC protocol that the SDK expects

	// Process service (command execution)
	procService := rpc.NewProcessService()
	procPath, procHandler := processconnect.NewProcessHandler(procService)
	mux.Handle(procPath, procHandler)
	log.Printf("  Registered Connect-RPC: %s", procPath)

	// Filesystem service
	fsService := rpc.NewFilesystemService(config.RootPath)
	fsPath, fsHandler := filesystemconnect.NewFilesystemHandler(fsService)
	mux.Handle(fsPath, fsHandler)
	log.Printf("  Registered Connect-RPC: %s", fsPath)

	// Apply middleware chain - but bypass for Connect-RPC paths to avoid interference with streaming
	// Create a wrapper that skips middleware for RPC paths
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip middleware for Connect-RPC paths (they use /package.Service/Method format)
		if len(r.URL.Path) > 1 && r.URL.Path[0] == '/' {
			// Check if it looks like a Connect-RPC path
			parts := strings.Split(r.URL.Path[1:], "/")
			if len(parts) >= 2 && strings.Contains(parts[0], ".") {
				// This looks like a Connect-RPC path, skip middleware
				log.Printf("[envd] Connect-RPC: %s %s", r.Method, r.URL.Path)
				mux.ServeHTTP(w, r)
				return
			}
		}

		// Apply middleware for non-RPC paths
		chain := api.AuthMiddleware(restHandler)(mux)
		chain = api.CORSMiddleware(chain)
		chain = loggingMiddleware(chain)
		chain.ServeHTTP(w, r)
	})

	// Use h2c for HTTP/2 without TLS (required for Connect-RPC streaming)
	h2cHandler := h2c.NewHandler(h, &http2.Server{})

	// Start server
	addr := fmt.Sprintf(":%d", config.Port)
	log.Printf("Listening on %s", addr)

	if err := http.ListenAndServe(addr, h2cHandler); err != nil {
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
