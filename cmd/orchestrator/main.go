// Package main implements the Orchestrator daemon.
//
// The Orchestrator runs on each compute node and manages sandbox lifecycle
// using Docker or Firecracker runtimes. It communicates with the Control Plane
// (API Server + Scheduler) via gRPC.
//
// This is the renamed version of the former "node-agent" component, aligned
// with E2B BYOC terminology.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/0prodigy/OpenE2B/internal/orchestrator"
	"github.com/0prodigy/OpenE2B/pkg/proto/node/nodeconnect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Configuration flags
	nodeID := flag.String("node-id", "", "Unique node identifier")
	listenAddr := flag.String("listen", ":9000", "gRPC listen address")
	edgeAddr := flag.String("edge-listen", ":8080", "Edge controller listen address for SDK traffic")
	controlPlane := flag.String("control-plane", "", "Control plane URL for registration")
	artifactsDir := flag.String("artifacts-dir", "/opt/e2b/artifacts", "Directory for template artifacts")
	sandboxesDir := flag.String("sandboxes-dir", "/opt/e2b/sandboxes", "Directory for sandbox data")
	runtimeMode := flag.String("runtime", "docker", "Sandbox runtime: docker or firecracker")

	flag.Parse()

	// Generate node ID if not provided
	if *nodeID == "" {
		hostname, _ := os.Hostname()
		*nodeID = fmt.Sprintf("node-%s-%d", hostname, time.Now().Unix())
	}

	log.Printf("Starting E2B Orchestrator (Node Daemon)...")
	log.Printf("  Node ID: %s", *nodeID)
	log.Printf("  gRPC Listen: %s", *listenAddr)
	log.Printf("  Edge Controller: %s", *edgeAddr)
	log.Printf("  Runtime: %s", *runtimeMode)
	log.Printf("  Artifacts: %s", *artifactsDir)
	log.Printf("  Sandboxes: %s", *sandboxesDir)

	// Ensure directories exist
	os.MkdirAll(*artifactsDir, 0755)
	os.MkdirAll(*sandboxesDir, 0755)

	// Create the orchestrator service
	orchestratorConfig := orchestrator.Config{
		NodeID:       *nodeID,
		ArtifactsDir: *artifactsDir,
		SandboxesDir: *sandboxesDir,
		RuntimeMode:  *runtimeMode,
		ControlPlane: *controlPlane,
	}

	orchestratorSvc, err := orchestrator.New(orchestratorConfig)
	if err != nil {
		log.Fatalf("Failed to create orchestrator service: %v", err)
	}

	// Set up HTTP mux for Connect
	mux := http.NewServeMux()

	// Register the NodeAgent service (gRPC interface for control plane)
	path, handler := nodeconnect.NewNodeAgentHandler(orchestratorSvc, connect.WithInterceptors())
	mux.Handle(path, handler)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Start HTTP/2 server
	server := &http.Server{
		Addr:    *listenAddr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	// Start gRPC server in background
	go func() {
		log.Printf("gRPC server listening on %s", *listenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Start Edge Controller for SDK traffic routing
	edgeController := orchestrator.NewEdgeController(orchestratorSvc.GetRuntime())
	edgeServer := &http.Server{
		Addr:    *edgeAddr,
		Handler: edgeController,
	}

	go func() {
		log.Printf("Edge Controller listening on %s", *edgeAddr)
		if err := edgeServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Edge controller failed: %v", err)
		}
	}()

	// Start heartbeat if control plane is configured
	if *controlPlane != "" {
		go orchestratorSvc.StartHeartbeat(context.Background())
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown both servers
	server.Shutdown(ctx)
	edgeServer.Shutdown(ctx)
	orchestratorSvc.Shutdown()

	log.Println("Shutdown complete")
}
