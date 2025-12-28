// Package orchestrator provides the Edge Controller for routing SDK traffic to sandboxes.
//
// The Edge Controller runs on the same host as the VMs and can access them
// directly via their internal IPs. It routes requests based on the E2b-Sandbox-Id
// header to the correct sandbox's envd instance.
package edge

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
)

// EdgeController handles routing SDK traffic to sandbox envd instances
type EdgeController struct {
	runtime  SandboxRuntime
	envdPort int32
}

// hostPattern matches {port}-{sandboxID}.{domain} in Host header
// Used when E2b-Sandbox-Id header is not present
var hostPattern = regexp.MustCompile(`^(\d+)-([a-z0-9]+)\.`)

// NewEdgeController creates a new edge controller
func NewEdgeController(runtime SandboxRuntime) *EdgeController {
	return &EdgeController{
		runtime:  runtime,
		envdPort: 49983,
	}
}

// ServeHTTP routes requests to the appropriate sandbox based on headers
func (e *EdgeController) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Health check endpoint
	if req.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	// Get sandbox ID from E2B SDK headers (preferred) or Host header (fallback)
	sandboxID := req.Header.Get("E2b-Sandbox-Id")
	if sandboxID == "" {
		// Fallback: try to parse from Host header (port-sandboxID.domain format)
		// This handles SDK requests that don't include the E2b-Sandbox-Id header
		// (e.g., OpenAPI client for filesystem operations)
		host := req.Host
		matches := hostPattern.FindStringSubmatch(host)
		if matches != nil && len(matches) >= 3 {
			sandboxID = matches[2]
			log.Printf("[edge-controller] Parsed sandboxID from Host header: %s (host=%s)", sandboxID, host)
		} else {
			log.Printf("[edge-controller] Missing E2b-Sandbox-Id header and Host header doesn't match pattern: %s", host)
			http.Error(w, "Missing E2b-Sandbox-Id header", http.StatusBadRequest)
			return
		}
	}

	log.Printf("[edge-controller] Routing request: sandboxID=%s path=%s", sandboxID, req.URL.Path)

	// Look up sandbox to get its internal IP
	info, err := e.runtime.GetSandboxStatus(req.Context(), sandboxID)
	if err != nil {
		log.Printf("[edge-controller] Sandbox not found: %s - %v", sandboxID, err)
		http.Error(w, "Sandbox not found", http.StatusNotFound)
		return
	}

	if info == nil {
		log.Printf("[edge-controller] Sandbox not found: %s", sandboxID)
		http.Error(w, "Sandbox not found", http.StatusNotFound)
		return
	}

	// Check sandbox state
	if info.State != "running" {
		log.Printf("[edge-controller] Sandbox not running: %s (state=%s)", sandboxID, info.State)
		http.Error(w, fmt.Sprintf("Sandbox not running (state=%s)", info.State), http.StatusServiceUnavailable)
		return
	}

	// Use the VM's internal IP address from EnvdAddress (e.g., "192.168.100.2:49983")
	// This is set by the Firecracker runtime when the VM is created
	targetURL := fmt.Sprintf("http://%s:%d", info.EnvdAddress, info.EnvdPort)
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Printf("[edge-controller] Invalid target URL: %s", targetURL)
		http.Error(w, "Invalid target", http.StatusInternalServerError)
		return
	}

	log.Printf("[edge-controller] Proxying to: %s (sandbox=%s)", targetURL, sandboxID)

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Enable streaming by flushing immediately (required for Connect RPC streaming)
	proxy.FlushInterval = -1

	// Custom error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[edge-controller] Proxy error for sandbox %s: %v", sandboxID, err)
		http.Error(w, "Sandbox unreachable", http.StatusBadGateway)
	}

	// Modify request to set proper host
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	proxy.ServeHTTP(w, req)
}
