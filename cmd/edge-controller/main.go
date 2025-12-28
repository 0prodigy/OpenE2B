package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"
)

// EdgeController is the reverse proxy that routes SDK traffic to sandbox instances
type EdgeController struct {
	domain     string
	apiURL     string
	httpClient *http.Client
}

// SandboxLookupResponse is the response from the API server for sandbox lookup
type SandboxLookupResponse struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	State string `json:"state"`
}

// hostPattern matches {port}-{sandboxID}.{domain}
var hostPattern = regexp.MustCompile(`^(\d+)-([a-z0-9-]+)\.`)

func main() {
	// Configuration flags
	listenAddr := flag.String("listen", ":8080", "Listen address for the proxy")
	domain := flag.String("domain", "e2b.local", "Base domain for sandbox routing")
	apiURL := flag.String("api-url", "http://localhost:3000", "API server URL for sandbox lookups")

	flag.Parse()

	log.Printf("Starting E2B Edge Controller...")
	log.Printf("  Listen: %s", *listenAddr)
	log.Printf("  Domain: %s", *domain)
	log.Printf("  API URL: %s", *apiURL)

	// Create edge controller
	controller := &EdgeController{
		domain: *domain,
		apiURL: *apiURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// Create server
	server := &http.Server{
		Addr:         *listenAddr,
		Handler:      controller,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Edge Controller listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down Edge Controller...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Edge Controller stopped")
}

// ServeHTTP handles incoming requests and routes them to the appropriate sandbox
func (e *EdgeController) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Health check endpoint
	if req.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	host := req.Host

	// Parse host to extract port and sandbox ID
	// Format: {port}-{sandboxID}.{domain}
	matches := hostPattern.FindStringSubmatch(host)
	if matches == nil {
		log.Printf("[edge] Invalid host format: %s", host)
		http.Error(w, "Invalid host format. Expected: {port}-{sandboxID}.{domain}", http.StatusBadRequest)
		return
	}

	portStr := matches[1]
	sandboxID := matches[2]

	log.Printf("[edge] Routing request: host=%s sandboxID=%s port=%s path=%s", host, sandboxID, portStr, req.URL.Path)

	// Look up sandbox location
	targetHost, targetPort, state, ok := e.lookupSandbox(sandboxID)
	if !ok {
		log.Printf("[edge] Sandbox not found: %s", sandboxID)
		http.Error(w, "Sandbox not found", http.StatusNotFound)
		return
	}

	// Check sandbox state
	if state == "paused" {
		log.Printf("[edge] Sandbox is paused: %s", sandboxID)
		http.Error(w, "Sandbox is paused", http.StatusServiceUnavailable)
		return
	}
	if state == "stopped" || state == "error" {
		log.Printf("[edge] Sandbox is not running: %s (state=%s)", sandboxID, state)
		http.Error(w, "Sandbox is not running", http.StatusServiceUnavailable)
		return
	}

	// Build target URL
	targetURL := fmt.Sprintf("http://%s:%d", targetHost, targetPort)
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Printf("[edge] Invalid target URL: %s", targetURL)
		http.Error(w, "Invalid target", http.StatusInternalServerError)
		return
	}

	log.Printf("[edge] Proxying to: %s", targetURL)

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Custom error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[edge] Proxy error for sandbox %s: %v", sandboxID, err)
		http.Error(w, "Sandbox unreachable", http.StatusBadGateway)
	}

	// Modify request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Set the host to the target (required for envd to work correctly)
		req.Host = target.Host
	}

	proxy.ServeHTTP(w, req)
}

// lookupSandbox looks up the sandbox location from the API server or local cache
func (e *EdgeController) lookupSandbox(sandboxID string) (host string, port int, state string, ok bool) {
	// For local development, use a simple in-memory lookup
	// In production, this would query the API server or a distributed cache

	// Query the API server for sandbox info
	resp, err := e.httpClient.Get(fmt.Sprintf("%s/sandboxes/%s", e.apiURL, sandboxID))
	if err != nil {
		log.Printf("[edge] Failed to lookup sandbox %s: %v", sandboxID, err)
		return "", 0, "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", 0, "", false
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[edge] API returned error for sandbox %s: %d", sandboxID, resp.StatusCode)
		return "", 0, "", false
	}

	// For now, we assume the sandbox is running on localhost with a predictable port
	// In production, the API response would include the actual host and port
	// The sandbox info endpoint returns the sandbox details, but we need the runtime port

	// Default to localhost and calculate port based on sandbox ID hash
	// This is a simplified approach for local development
	// In production, this would be stored in the sandbox record
	return "localhost", 49983, "running", true
}
