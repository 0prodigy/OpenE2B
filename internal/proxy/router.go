package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

// SandboxState represents the state of a sandbox
type SandboxState string

const (
	SandboxStateRunning SandboxState = "running"
	SandboxStatePaused  SandboxState = "paused"
	SandboxStateStopped SandboxState = "stopped"
	SandboxStateError   SandboxState = "error"
)

// SandboxLookup provides sandbox connection info for routing
type SandboxLookup interface {
	// GetSandboxHost returns the host, port, and state for a sandbox
	GetSandboxHost(sandboxID string) (host string, port int, state SandboxState, ok bool)
}

// Router provides host-based routing to sandbox envd instances
type Router struct {
	domain       string
	envdPort     int
	sandboxStore SandboxLookup
}

// NewRouter creates a new proxy router
func NewRouter(domain string, envdPort int, store SandboxLookup) *Router {
	return &Router{
		domain:       domain,
		envdPort:     envdPort,
		sandboxStore: store,
	}
}

// hostPattern matches {port}-{sandboxID}.{domain}
var hostPattern = regexp.MustCompile(`^(\d+)-([a-z0-9-]+)\.`)

// ServeHTTP routes requests to the appropriate sandbox
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host

	// Health check endpoint
	if req.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	var sandboxID string
	var requestedPort string

	// Method 1: Check for E2B SDK headers (E2b-Sandbox-Id, E2b-Sandbox-Port)
	// This allows routing when Host header can't be set (e.g., local development without DNS)
	sdkSandboxID := req.Header.Get("E2b-Sandbox-Id")
	sdkSandboxPort := req.Header.Get("E2b-Sandbox-Port")
	if sdkSandboxID != "" && sdkSandboxPort != "" {
		sandboxID = sdkSandboxID
		requestedPort = sdkSandboxPort
		log.Printf("[proxy] Using SDK headers: sandboxID=%s port=%s", sandboxID, requestedPort)
	} else {
		// Method 2: Parse host to extract port and sandbox ID
		// Format: {port}-{sandboxID}.{domain}
		matches := hostPattern.FindStringSubmatch(host)
		if matches == nil {
			log.Printf("[proxy] Invalid host format: %s (no SDK headers provided either)", host)
			http.Error(w, "Invalid host format. Expected: {port}-{sandboxID}.{domain} or E2b-Sandbox-Id/E2b-Sandbox-Port headers", http.StatusBadRequest)
			return
		}
		requestedPort = matches[1]
		sandboxID = matches[2]
	}

	log.Printf("[proxy] Routing request: host=%s sandboxID=%s port=%s path=%s", host, sandboxID, requestedPort, req.URL.Path)

	// Look up sandbox
	sandboxHost, sandboxPort, state, ok := r.sandboxStore.GetSandboxHost(sandboxID)
	if !ok {
		log.Printf("[proxy] Sandbox not found: %s", sandboxID)
		http.Error(w, "Sandbox not found", http.StatusNotFound)
		return
	}

	// Check sandbox state
	switch state {
	case SandboxStatePaused:
		log.Printf("[proxy] Sandbox is paused: %s", sandboxID)
		http.Error(w, "Sandbox is paused", http.StatusServiceUnavailable)
		return
	case SandboxStateStopped:
		log.Printf("[proxy] Sandbox is stopped: %s", sandboxID)
		http.Error(w, "Sandbox is stopped", http.StatusServiceUnavailable)
		return
	case SandboxStateError:
		log.Printf("[proxy] Sandbox is in error state: %s", sandboxID)
		http.Error(w, "Sandbox is in error state", http.StatusServiceUnavailable)
		return
	}

	// Validate access token
	token := req.Header.Get("X-Access-Token")
	if token == "" {
		token = req.URL.Query().Get("access_token")
	}
	// Note: In production, validate the token against the sandbox's envdAccessToken
	// For local development, we allow requests without validation

	// Build target URL using the sandbox's assigned host port
	targetURL := fmt.Sprintf("http://%s:%d", sandboxHost, sandboxPort)
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Printf("[proxy] Invalid target URL: %s", targetURL)
		http.Error(w, "Invalid target", http.StatusInternalServerError)
		return
	}

	log.Printf("[proxy] Proxying to: %s", targetURL)

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Enable streaming by flushing immediately (required for Connect RPC streaming)
	proxy.FlushInterval = -1 // Flush immediately

	// Custom error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[proxy] Proxy error for sandbox %s: %v", sandboxID, err)
		http.Error(w, "Sandbox unreachable", http.StatusBadGateway)
	}

	// Modify request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Set the host to the target for proper routing
		req.Host = target.Host
	}

	proxy.ServeHTTP(w, req)
}

// BuildSandboxURL constructs the public URL for a sandbox port
func (r *Router) BuildSandboxURL(sandboxID string, port int) string {
	return fmt.Sprintf("https://%d-%s.%s", port, sandboxID, r.domain)
}

// BuildEnvdURL constructs the envd URL for a sandbox
func (r *Router) BuildEnvdURL(sandboxID string) string {
	return r.BuildSandboxURL(sandboxID, r.envdPort)
}

// ParseSandboxHost extracts sandbox ID and port from a hostname
func ParseSandboxHost(host, domain string) (sandboxID string, port int, ok bool) {
	// Remove domain suffix
	suffix := "." + domain
	if !strings.HasSuffix(host, suffix) {
		return "", 0, false
	}
	prefix := strings.TrimSuffix(host, suffix)

	// Parse port-sandboxID
	parts := strings.SplitN(prefix, "-", 2)
	if len(parts) != 2 {
		return "", 0, false
	}

	var p int
	if _, err := fmt.Sscanf(parts[0], "%d", &p); err != nil {
		return "", 0, false
	}

	return parts[1], p, true
}

// HostRouter wraps the Router to support host-based routing decisions
type HostRouter struct {
	proxyRouter *Router
	apiHandler  http.Handler
	domain      string
}

// NewHostRouter creates a new host-based router that can route between API and proxy
func NewHostRouter(proxyRouter *Router, apiHandler http.Handler, domain string) *HostRouter {
	return &HostRouter{
		proxyRouter: proxyRouter,
		apiHandler:  apiHandler,
		domain:      domain,
	}
}

// ServeHTTP routes requests based on the host header
func (h *HostRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host

	// Check if this is a sandbox proxy request (matches the pattern)
	if hostPattern.MatchString(host) {
		h.proxyRouter.ServeHTTP(w, req)
		return
	}

	// Otherwise, serve as API
	h.apiHandler.ServeHTTP(w, req)
}
