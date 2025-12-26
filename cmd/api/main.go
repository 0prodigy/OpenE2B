package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/0prodigy/OpenE2B/internal/api"
	"github.com/0prodigy/OpenE2B/internal/auth"
	"github.com/0prodigy/OpenE2B/internal/build"
	"github.com/0prodigy/OpenE2B/internal/db"
	"github.com/0prodigy/OpenE2B/internal/orchestrator"
)

func main() {
	// Load configuration from environment
	config := loadConfig()
	dbConfig := db.LoadConfig()

	log.Printf("Starting E2B API Server...")
	log.Printf("  Domain: %s", config.Domain)
	log.Printf("  Port: %d", config.APIPort)
	log.Printf("  Envd Port: %d", config.EnvdPort)

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

	// Initialize orchestrator components
	// 1. Artifact Storage (Local)
	storageDir := "./data/artifacts"
	artifactStorage, err := orchestrator.NewLocalStorage(storageDir)
	if err != nil {
		log.Fatalf("Failed to initialize artifact storage: %v", err)
	}

	// 2. Image Manager
	registryHost := "localhost:5000" // Default for dev
	imageManager := orchestrator.NewImageManager(artifactStorage, registryHost)

	// 3. Scheduler
	// Use DockerNode for real execution
	dockerNode := orchestrator.NewDockerNode()
	scheduler := orchestrator.NewScheduler(dockerNode)

	// Seed a dummy node so builds can be scheduled
	seedInternalNode(scheduler)

	// Initialize build service with artifacts directory
	artifactsDir := filepath.Join("data", "artifacts")
	buildSvc := build.NewService(dbStore, scheduler, imageManager, config.Domain, artifactsDir, 2)
	defer buildSvc.Stop()
	log.Printf("  Build Service: Started with 2 workers")
	log.Printf("  Artifacts Dir: %s", artifactsDir)

	// Start sandbox expiration checker
	scheduler.StartExpirationChecker(context.Background())

	// Create handler and router
	handler := api.NewHandler(store, config)
	handler.SetBuildService(buildSvc)
	handler.SetScheduler(scheduler)
	router := api.NewRouter(handler)

	// Apply middleware chain
	var h http.Handler = router
	h = auth.AllowAllMiddleware(h)
	h = auth.CORSMiddleware(h)
	h = loggingMiddleware(h)

	// Create server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", config.APIPort),
		Handler:      h,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}

func loadConfig() *api.Config {
	domain := getEnv("E2B_DOMAIN", "e2b.local")
	apiPort := getEnvInt("E2B_API_PORT", 3000)
	envdPort := getEnvInt("E2B_ENVD_PORT", 49983)

	return &api.Config{
		Domain:   domain,
		APIPort:  apiPort,
		EnvdPort: envdPort,
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

func seedInternalNode(scheduler *orchestrator.Scheduler) {
	nodeID := "local-node-1"
	scheduler.RegisterNode(&orchestrator.Node{
		Spec: orchestrator.NodeSpec{
			ID:          nodeID,
			ClusterID:   "local",
			CPUCount:    8,
			MemoryBytes: 16 * 1024 * 1024 * 1024, // 16GB
		},
		Status: orchestrator.NodeStatus{
			State: orchestrator.NodeStateReady,
		},
	})
	log.Printf("Seeded internal node: %s", nodeID)
}
