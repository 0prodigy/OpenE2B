package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

// Router provides host-based routing to sandbox envd instances
type Router struct {
	domain       string
	envdPort     int
	sandboxStore SandboxLookup
}

// SandboxLookup provides sandbox connection info
type SandboxLookup interface {
	GetSandboxHost(sandboxID string) (host string, token string, ok bool)
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
	
	// Parse host to extract port and sandbox ID
	matches := hostPattern.FindStringSubmatch(host)
	if matches == nil {
		http.Error(w, "Invalid host format", http.StatusBadRequest)
		return
	}

	port := matches[1]
	sandboxID := matches[2]

	// Look up sandbox
	sandboxHost, expectedToken, ok := r.sandboxStore.GetSandboxHost(sandboxID)
	if !ok {
		http.Error(w, "Sandbox not found", http.StatusNotFound)
		return
	}

	// Validate traffic token if required
	if expectedToken != "" {
		token := req.Header.Get("X-Traffic-Token")
		if token == "" {
			token = req.URL.Query().Get("traffic_token")
		}
		if token != expectedToken {
			http.Error(w, "Invalid traffic token", http.StatusUnauthorized)
			return
		}
	}

	// Build target URL
	targetURL := fmt.Sprintf("http://%s:%s", sandboxHost, port)
	target, err := url.Parse(targetURL)
	if err != nil {
		http.Error(w, "Invalid target", http.StatusInternalServerError)
		return
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)
	
	// Modify request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip the port-sandboxID prefix from host
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

// ParseSandboxHost extracts sandbox ID from a hostname
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
