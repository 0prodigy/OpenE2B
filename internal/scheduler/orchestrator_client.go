package scheduler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	pb "github.com/0prodigy/OpenE2B/pkg/proto/node"
	"github.com/0prodigy/OpenE2B/pkg/proto/node/nodeconnect"
)

// OrchestratorClient implements NodeOperations by communicating with remote Orchestrator nodes via gRPC
type OrchestratorClient struct {
	mu sync.RWMutex

	// clients maps node IDs to their gRPC clients
	clients map[string]nodeconnect.NodeAgentClient

	// sandboxPorts tracks sandbox to port mappings for proxy routing
	sandboxPorts map[string]sandboxLocation
}

// sandboxLocation tracks where a sandbox is running
type sandboxLocation struct {
	nodeID   string
	host     string
	hostPort int
	envdPort int
}

// NewOrchestratorClient creates a new orchestrator client
func NewOrchestratorClient() *OrchestratorClient {
	return &OrchestratorClient{
		clients:      make(map[string]nodeconnect.NodeAgentClient),
		sandboxPorts: make(map[string]sandboxLocation),
	}
}

// RegisterNode adds a connection to a remote orchestrator node
func (o *OrchestratorClient) RegisterNode(nodeID, address string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Create HTTP client for Connect
	// Use a longer timeout since VM creation can take 30+ seconds
	httpClient := &http.Client{
		Timeout: 120 * time.Second,
	}

	// Create Connect client
	client := nodeconnect.NewNodeAgentClient(httpClient, address)

	// Test connection with health check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Heartbeat(ctx, connect.NewRequest(&pb.HeartbeatRequest{
		NodeId: nodeID,
	}))
	if err != nil {
		return fmt.Errorf("failed to connect to orchestrator %s at %s: %w", nodeID, address, err)
	}

	o.clients[nodeID] = client
	log.Printf("[orchestrator-client] Registered node %s at %s", nodeID, address)
	return nil
}

// UnregisterNode removes a connection to a remote orchestrator node
func (o *OrchestratorClient) UnregisterNode(nodeID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.clients, nodeID)
}

// CreateSandbox creates a sandbox on a remote orchestrator node
func (o *OrchestratorClient) CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) (*SandboxResult, error) {
	o.mu.Lock()
	client, ok := o.clients[node.Spec.ID]
	o.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("no client for node %s", node.Spec.ID)
	}

	req := &pb.CreateSandboxRequest{
		SandboxId:      spec.ID,
		TemplateId:     spec.TemplateID,
		BuildId:        spec.BuildID,
		CpuCount:       int32(spec.CPUCount),
		MemoryMb:       int32(spec.MemoryMB),
		DiskSizeMb:     int32(spec.DiskSizeMB),
		EnvVars:        spec.EnvVars,
		Metadata:       spec.Metadata,
		TimeoutSeconds: int64(spec.Timeout.Seconds()),
		AutoPause:      spec.AutoPause,
		EnvdToken:      spec.EnvdToken,
	}

	resp, err := client.CreateSandbox(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox on node %s: %w", node.Spec.ID, err)
	}

	if !resp.Msg.Success {
		return nil, fmt.Errorf("sandbox creation failed on node %s: %s", node.Spec.ID, resp.Msg.Error)
	}

	// Store sandbox location for proxy routing
	// hostPort is the port on the EC2 host that forwards to the VM's envd (via iptables DNAT)
	o.mu.Lock()
	o.sandboxPorts[spec.ID] = sandboxLocation{
		nodeID:   node.Spec.ID,
		host:     node.Spec.Address,
		hostPort: int(resp.Msg.EnvdPort), // Port on EC2 host that forwards to VM
		envdPort: int(resp.Msg.EnvdPort),
	}
	o.mu.Unlock()

	log.Printf("[orchestrator-client] Created sandbox %s on node %s (envd: %s:%d)",
		spec.ID, node.Spec.ID, node.Spec.Address, resp.Msg.EnvdPort)

	return &SandboxResult{
		HostPort: int(resp.Msg.EnvdPort),
		EnvdPort: int(resp.Msg.EnvdPort),
	}, nil
}

// StopSandbox stops a sandbox on a remote orchestrator node
func (o *OrchestratorClient) StopSandbox(ctx context.Context, node *Node, sandboxID string) error {
	o.mu.Lock()
	client, ok := o.clients[node.Spec.ID]
	o.mu.Unlock()

	if !ok {
		return fmt.Errorf("no client for node %s", node.Spec.ID)
	}

	req := &pb.StopSandboxRequest{
		SandboxId: sandboxID,
		Force:     true,
	}

	resp, err := client.StopSandbox(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to stop sandbox on node %s: %w", node.Spec.ID, err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox stop failed on node %s: %s", node.Spec.ID, resp.Msg.Error)
	}

	// Remove sandbox from tracking
	o.mu.Lock()
	delete(o.sandboxPorts, sandboxID)
	o.mu.Unlock()

	log.Printf("[orchestrator-client] Stopped sandbox %s on node %s", sandboxID, node.Spec.ID)
	return nil
}

// PauseSandbox pauses a sandbox on a remote orchestrator node
func (o *OrchestratorClient) PauseSandbox(ctx context.Context, node *Node, sandboxID string) error {
	o.mu.Lock()
	client, ok := o.clients[node.Spec.ID]
	o.mu.Unlock()

	if !ok {
		return fmt.Errorf("no client for node %s", node.Spec.ID)
	}

	req := &pb.PauseSandboxRequest{
		SandboxId: sandboxID,
	}

	resp, err := client.PauseSandbox(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to pause sandbox on node %s: %w", node.Spec.ID, err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox pause failed on node %s: %s", node.Spec.ID, resp.Msg.Error)
	}

	log.Printf("[orchestrator-client] Paused sandbox %s on node %s", sandboxID, node.Spec.ID)
	return nil
}

// ResumeSandbox resumes a paused sandbox on a remote orchestrator node
func (o *OrchestratorClient) ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error {
	o.mu.Lock()
	client, ok := o.clients[node.Spec.ID]
	o.mu.Unlock()

	if !ok {
		return fmt.Errorf("no client for node %s", node.Spec.ID)
	}

	// Get the snapshot ID from the sandbox location (for now we use sandbox ID)
	req := &pb.ResumeSandboxRequest{
		SandboxId:  sandboxID,
		SnapshotId: sandboxID, // Docker uses sandbox ID as snapshot ID
	}

	resp, err := client.ResumeSandbox(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to resume sandbox on node %s: %w", node.Spec.ID, err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox resume failed on node %s: %s", node.Spec.ID, resp.Msg.Error)
	}

	log.Printf("[orchestrator-client] Resumed sandbox %s on node %s", sandboxID, node.Spec.ID)
	return nil
}

// GetSandboxHost returns the host and port for a sandbox (for proxy routing)
func (o *OrchestratorClient) GetSandboxHost(sandboxID string) (host string, port int, ok bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	loc, exists := o.sandboxPorts[sandboxID]
	if !exists {
		return "", 0, false
	}

	return loc.host, loc.hostPort, true
}
