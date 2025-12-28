package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0prodigy/OpenE2B/internal/api"
	"github.com/0prodigy/OpenE2B/internal/auth"
	"github.com/0prodigy/OpenE2B/internal/build"
	"github.com/0prodigy/OpenE2B/internal/db"
	"github.com/0prodigy/OpenE2B/internal/proxy"
	"github.com/0prodigy/OpenE2B/internal/scheduler"
)

func main() {
	// Load configuration from environment
	config := loadConfig()
	dbConfig := db.LoadConfig()

	log.Printf("Starting E2B API Server...")
	log.Printf("  Domain: %s", config.Domain)
	log.Printf("  API Port: %d", config.APIPort)
	log.Printf("  Proxy Port: %d", config.ProxyPort)
	log.Printf("  Envd Port: %d", config.EnvdPort)
	if config.EdgeControllerURL != "" {
		log.Printf("  Edge Controller: %s", config.EdgeControllerURL)
	}

	// Initialize database store
	var dbStore db.Store
	var err error

	// Priority: Supabase REST API > PostgreSQL direct > In-Memory
	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey := os.Getenv("SUPABASE_SECRET_KEY")

	if supabaseURL != "" && supabaseKey != "" {
		log.Printf("  Storage: Supabase REST API")
		log.Printf("  Supabase URL: %s", supabaseURL)

		dbStore, err = db.NewSupabaseStore(supabaseURL, supabaseKey)
		if err != nil {
			log.Printf("Warning: Failed to connect to Supabase: %v", err)
			log.Printf("Falling back to in-memory store")
			dbStore = db.NewMemoryStore()
		} else {
			log.Printf("  Connected to Supabase")
		}
	} else if !dbConfig.UseInMemory && dbConfig.DatabaseURL != "" {
		log.Printf("  Storage: PostgreSQL (direct connection)")

		ctx := context.Background()
		dbStore, err = db.NewPostgresStore(ctx, dbConfig.DatabaseURL)
		if err != nil {
			log.Printf("Warning: Failed to connect to database: %v", err)
			log.Printf("Falling back to in-memory store")
			dbStore = db.NewMemoryStore()
		} else {
			log.Printf("  Connected to PostgreSQL database")
		}
	} else {
		log.Printf("  Storage: In-Memory (development mode)")
		dbStore = db.NewMemoryStore()
	}

	// Ensure we close the database on shutdown
	defer func() {
		if err := dbStore.Close(); err != nil {
			log.Printf("Error closing database: %v", err)
		}
	}()

	// Initialize store with database backend
	store := api.NewStore(dbStore)

	// Seed with a default template for testing (only if not exists)
	seedDefaultTemplate(store)

	// Initialize scheduler with either remote orchestrator or local Docker runtime
	var nodeOps scheduler.NodeOperations
	orchestratorNodes := getEnv("E2B_ORCHESTRATOR_NODES", "")

	if orchestratorNodes != "" {
		// Use remote orchestrators
		log.Printf("  Mode: Remote Orchestrators")
		orchestratorClient := scheduler.NewOrchestratorClient()
		nodeOps = orchestratorClient

		// Register remote orchestrator nodes
		// Format: "node-1=http://host:port,node-2=http://host:port"
		for _, nodeDef := range strings.Split(orchestratorNodes, ",") {
			parts := strings.SplitN(strings.TrimSpace(nodeDef), "=", 2)
			if len(parts) != 2 {
				log.Printf("Warning: invalid node definition: %s", nodeDef)
				continue
			}
			nodeID := strings.TrimSpace(parts[0])
			nodeAddr := strings.TrimSpace(parts[1])

			if err := orchestratorClient.RegisterNode(nodeID, nodeAddr); err != nil {
				log.Printf("Warning: failed to register node %s: %v", nodeID, err)
				continue
			}
		}
	} else {
		go func() {
			// start binary /orchestrator with docker runtime and start heartbeat in background
			cmd := exec.Command("./bin/orchestrator", "-node-id", "local-node-1", "-runtime", "docker", "-edge-listen", ":9001")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err := cmd.Run()
			if err != nil {
				// print stack trace
				log.Fatalf("Failed to start orchestrator: %v", err)

			}
		}()
		// wait for orchestrator to start
		time.Sleep(5 * time.Second)
		orchestratorClient := scheduler.NewOrchestratorClient()
		nodeOps = orchestratorClient
		if err := orchestratorClient.RegisterNode("local-node-1", "http://localhost:9000"); err != nil {
			log.Fatalf("Failed to register orchestrator: %v", err)
		}
		log.Printf("Registered local node: %s", "local-node-1")
	}

	sched := scheduler.NewScheduler(nodeOps)

	// Seed nodes for development
	seedNodes(sched, orchestratorNodes)

	// Initialize artifact storage
	storageDir := "./data/artifacts"
	artifactStorage, err := newLocalStorage(storageDir)
	if err != nil {
		log.Fatalf("Failed to initialize artifact storage: %v", err)
	}

	// Initialize image manager
	registryHost := getEnv("E2B_REGISTRY_HOST", "localhost:5000")
	imageManager := newImageManager(artifactStorage, registryHost)

	// Initialize build service with artifacts directory
	artifactsDir := filepath.Join("data", "artifacts")
	buildSvc := build.NewService(dbStore, sched, imageManager, config.Domain, artifactsDir, 2)
	defer buildSvc.Stop()
	log.Printf("  Build Service: Started with 2 workers")
	log.Printf("  Artifacts Dir: %s", artifactsDir)

	// Start sandbox expiration checker
	sched.StartExpirationChecker(context.Background())

	// Create API handler and router
	handler := api.NewHandler(store, &api.Config{
		Domain:   config.Domain,
		APIPort:  config.APIPort,
		EnvdPort: config.EnvdPort,
	})
	handler.SetBuildService(buildSvc)
	handler.SetScheduler(sched)
	apiRouter := api.NewRouter(handler)

	// Apply middleware to API handler
	var apiHandler http.Handler = apiRouter
	apiHandler = auth.AllowAllMiddleware(apiHandler)
	apiHandler = auth.CORSMiddleware(apiHandler)
	apiHandler = loggingMiddleware(apiHandler)

	// Create API server
	apiServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", config.APIPort),
		Handler:      apiHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // VM creation can take 30+ seconds
		IdleTimeout:  120 * time.Second,
	}

	// Start API server in goroutine
	go func() {
		log.Printf("API server listening on %s", apiServer.Addr)
		if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("API server failed: %v", err)
		}
	}()

	// Start local proxy server for SDK sandbox traffic
	// This proxy routes SDK requests to sandbox envd instances
	var proxyServer *http.Server
	if config.ProxyPort > 0 {
		var proxyHandler http.Handler

		if config.EdgeControllerURL != "" {
			// Mode 1: Remote Edge Controller - forward to external Edge Controller
			proxyHandler = newEdgeControllerProxy(config.EdgeControllerURL, config.Domain)
			log.Printf("  Proxy Mode: Forwarding to Edge Controller at %s", config.EdgeControllerURL)
		} else {
			// Mode 2: Local Docker Runtime - route directly to sandbox containers
			// Create a lookup adapter that uses the scheduler's Docker runtime
			lookup := &schedulerLookup{sched: sched, nodeOps: nodeOps}
			proxyHandler = proxy.NewRouter(config.Domain, config.EnvdPort, lookup)
			log.Printf("  Proxy Mode: Local Docker Runtime (direct routing)")
		}

		proxyServer = &http.Server{
			Addr:         fmt.Sprintf(":%d", config.ProxyPort),
			Handler:      loggingMiddleware(proxyHandler),
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 300 * time.Second, // Longer timeout for streaming
			IdleTimeout:  120 * time.Second,
		}

		go func() {
			log.Printf("Proxy server listening on %s", proxyServer.Addr)
			if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Proxy server failed: %v", err)
			}
		}()
	}

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if proxyServer != nil {
		proxyServer.Shutdown(shutdownCtx)
	}
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}

// hostPattern matches {port}-{sandboxID}.{domain} in Host header
var hostPattern = regexp.MustCompile(`^(\d+)-([a-z0-9]+)\.`)

// newEdgeControllerProxy creates a proxy that forwards sandbox traffic to the Edge Controller
// It parses the sandbox ID from the Host header and adds E2b-Sandbox-Id header
func newEdgeControllerProxy(edgeControllerURL, domain string) http.Handler {
	target, err := url.Parse(edgeControllerURL)
	if err != nil {
		log.Fatalf("Invalid edge controller URL: %s", edgeControllerURL)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Health check
		if req.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}

		// Parse sandbox ID from Host header
		host := req.Host
		matches := hostPattern.FindStringSubmatch(host)
		if matches == nil || len(matches) < 3 {
			log.Printf("[proxy] Invalid Host header format: %s (expected: {port}-{sandboxID}.{domain})", host)
			http.Error(w, "Invalid Host header format", http.StatusBadRequest)
			return
		}

		port := matches[1]
		sandboxID := matches[2]

		log.Printf("[proxy] Routing: %s -> Edge Controller (sandbox=%s, port=%s)", req.URL.Path, sandboxID, port)

		// Create reverse proxy
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = -1 // Enable streaming

		// Add sandbox routing headers and forward to Edge Controller
		originalDirector := proxy.Director
		proxy.Director = func(proxyReq *http.Request) {
			originalDirector(proxyReq)
			// Set the Edge Controller as the host
			proxyReq.Host = target.Host
			// Add E2B SDK routing headers
			proxyReq.Header.Set("E2b-Sandbox-Id", sandboxID)
			proxyReq.Header.Set("E2b-Sandbox-Port", port)
		}

		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[proxy] Error forwarding to Edge Controller: %v", err)
			http.Error(w, "Edge Controller unreachable", http.StatusBadGateway)
		}

		proxy.ServeHTTP(w, req)
	})
}

// Config holds the server configuration
type Config struct {
	Domain            string
	APIPort           int
	ProxyPort         int
	EnvdPort          int
	EdgeControllerURL string
}

func loadConfig() *Config {
	domain := getEnv("E2B_DOMAIN", "e2b.local")
	apiPort := getEnvInt("E2B_API_PORT", 3000)
	proxyPort := getEnvInt("E2B_PROXY_PORT", 8080)
	envdPort := getEnvInt("E2B_ENVD_PORT", 49983)
	// Edge Controller URL - where the proxy forwards sandbox traffic
	// This should be the Edge Controller on the EC2 VM
	edgeControllerURL := getEnv("E2B_EDGE_CONTROLLER_URL", "")

	return &Config{
		Domain:            domain,
		APIPort:           apiPort,
		ProxyPort:         proxyPort,
		EnvdPort:          envdPort,
		EdgeControllerURL: edgeControllerURL,
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
		log.Printf("%s %s %s", r.Method, r.Host, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// seedDefaultTemplate creates a default "base" template for testing
func seedDefaultTemplate(store *api.Store) {
	// Check if base template already exists
	if _, exists := store.GetTemplate("base"); exists {
		log.Printf("Default 'base' template already exists")
		return
	}

	req := api.TemplateBuildRequestV3{
		Alias: "base",
	}
	cpuCount := 1
	memoryMB := 512
	req.CPUCount = &cpuCount
	req.MemoryMB = &memoryMB

	_, _, err := store.CreateTemplate(req)
	if err != nil {
		log.Printf("Warning: failed to seed default template: %v", err)
	} else {
		log.Printf("Seeded default 'base' template")
	}
}

func seedNodes(sched *scheduler.Scheduler, orchestratorNodes string) {
	if orchestratorNodes != "" {
		// Register nodes for remote orchestrators
		// Format: "node-1=http://host:port,node-2=http://host:port"
		for _, nodeDef := range strings.Split(orchestratorNodes, ",") {
			parts := strings.SplitN(strings.TrimSpace(nodeDef), "=", 2)
			if len(parts) != 2 {
				continue
			}
			nodeID := strings.TrimSpace(parts[0])
			nodeAddr := strings.TrimSpace(parts[1])

			// Extract host from address (remove http:// and port)
			host := nodeAddr
			host = strings.TrimPrefix(host, "http://")
			host = strings.TrimPrefix(host, "https://")
			if colonIdx := strings.LastIndex(host, ":"); colonIdx > 0 {
				host = host[:colonIdx]
			}

			sched.RegisterNode(&scheduler.Node{
				Spec: scheduler.NodeSpec{
					ID:          nodeID,
					ClusterID:   "remote",
					Address:     host,
					CPUCount:    4,
					MemoryBytes: 8 * 1024 * 1024 * 1024, // 8GB
				},
				Status: scheduler.NodeStatus{
					State: scheduler.NodeStateReady,
				},
			})
			log.Printf("Registered remote node: %s at %s", nodeID, host)
		}
	} else {
		// Seed a local node for development
		nodeID := "local-node-1"
		sched.RegisterNode(&scheduler.Node{
			Spec: scheduler.NodeSpec{
				ID:          nodeID,
				ClusterID:   "local",
				Address:     "localhost",
				CPUCount:    8,
				MemoryBytes: 16 * 1024 * 1024 * 1024, // 16GB
			},
			Status: scheduler.NodeStatus{
				State: scheduler.NodeStateReady,
			},
		})
		log.Printf("Seeded local node: %s", nodeID)
	}
}

// Minimal storage interface adapters to avoid importing orchestrator package
// These will be removed once the full refactoring is complete

type localStorage struct {
	basePath string
}

func newLocalStorage(basePath string) (*localStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, err
	}
	return &localStorage{basePath: basePath}, nil
}

type imageManager struct {
	storage      *localStorage
	registryHost string
}

func newImageManager(storage *localStorage, registryHost string) *imageManager {
	return &imageManager{storage: storage, registryHost: registryHost}
}

// schedulerLookup adapts the scheduler to the proxy.SandboxLookup interface
type schedulerLookup struct {
	sched   *scheduler.Scheduler
	nodeOps scheduler.NodeOperations
}

// GetSandboxHost implements proxy.SandboxLookup
func (s *schedulerLookup) GetSandboxHost(sandboxID string) (host string, port int, state proxy.SandboxState, ok bool) {
	// First check if sandbox exists in scheduler
	sb, exists := s.sched.GetSandbox(sandboxID)
	if !exists || sb == nil {
		return "", 0, proxy.SandboxStateError, false
	}

	// Map scheduler state to proxy state
	switch sb.Status.State {
	case scheduler.SandboxStateRunning:
		state = proxy.SandboxStateRunning
	case scheduler.SandboxStatePaused:
		state = proxy.SandboxStatePaused
	case scheduler.SandboxStateStopped:
		state = proxy.SandboxStateStopped
	default:
		state = proxy.SandboxStateError
	}

	// Get the actual host:port from the node operations
	// For Docker runtime, this gets the allocated host port
	if dockerRuntime, isDR := s.nodeOps.(*scheduler.DockerRuntime); isDR {
		host, port, ok = dockerRuntime.GetSandboxHost(sandboxID)
		return host, port, state, ok
	}

	// For remote orchestrators, use the OrchestratorClient which has the actual host
	if orchestratorClient, isOC := s.nodeOps.(*scheduler.OrchestratorClient); isOC {
		host, port, ok = orchestratorClient.GetSandboxHost(sandboxID)
		return host, port, state, ok
	}

	// Fallback: look up the node's address by ID from the nodes list
	for _, node := range s.sched.ListNodes() {
		if node.Spec.ID == sb.Status.NodeID {
			return node.Spec.Address, sb.Status.HostPort, state, true
		}
	}

	// Last resort - return NodeID (might not work if not resolvable)
	return sb.Status.NodeID, sb.Status.HostPort, state, true
}
