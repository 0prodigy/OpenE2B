package orchestrator

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	pb "github.com/0prodigy/OpenE2B/pkg/proto/node"
	"github.com/0prodigy/OpenE2B/pkg/proto/node/nodeconnect"
)

// GRPCNode implements NodeOperations using gRPC to communicate with remote node agents
type GRPCNode struct {
	clients map[string]nodeconnect.NodeAgentClient
}

// NewGRPCNode creates a new gRPC-based node operations handler
func NewGRPCNode() *GRPCNode {
	return &GRPCNode{
		clients: make(map[string]nodeconnect.NodeAgentClient),
	}
}

// RegisterNode registers a node agent at the given address
func (n *GRPCNode) RegisterNode(nodeID, address string) error {
	// Create Connect client for the node agent
	client := nodeconnect.NewNodeAgentClient(
		http.DefaultClient,
		address,
	)

	n.clients[nodeID] = client
	log.Printf("Registered node agent: %s at %s", nodeID, address)
	return nil
}

// UnregisterNode removes a node agent
func (n *GRPCNode) UnregisterNode(nodeID string) {
	delete(n.clients, nodeID)
}

func (n *GRPCNode) getClient(nodeID string) (nodeconnect.NodeAgentClient, error) {
	client, ok := n.clients[nodeID]
	if !ok {
		return nil, fmt.Errorf("node not registered: %s", nodeID)
	}
	return client, nil
}

func (n *GRPCNode) CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) error {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&pb.CreateSandboxRequest{
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
	})

	resp, err := client.CreateSandbox(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC CreateSandbox failed: %w", err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox creation failed: %s", resp.Msg.Error)
	}

	log.Printf("Sandbox %s created on node %s (envd: %s)", spec.ID, node.Spec.ID, resp.Msg.EnvdAddress)
	return nil
}

func (n *GRPCNode) StopSandbox(ctx context.Context, node *Node, sandboxID string) error {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&pb.StopSandboxRequest{
		SandboxId: sandboxID,
		Force:     true,
	})

	resp, err := client.StopSandbox(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC StopSandbox failed: %w", err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox stop failed: %s", resp.Msg.Error)
	}

	return nil
}

func (n *GRPCNode) PauseSandbox(ctx context.Context, node *Node, sandboxID string) error {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&pb.PauseSandboxRequest{
		SandboxId: sandboxID,
	})

	resp, err := client.PauseSandbox(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC PauseSandbox failed: %w", err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox pause failed: %s", resp.Msg.Error)
	}

	return nil
}

func (n *GRPCNode) ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&pb.ResumeSandboxRequest{
		SandboxId: sandboxID,
	})

	resp, err := client.ResumeSandbox(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC ResumeSandbox failed: %w", err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("sandbox resume failed: %s", resp.Msg.Error)
	}

	return nil
}

// PullArtifact requests a node to pull a template artifact
func (n *GRPCNode) PullArtifact(ctx context.Context, node *Node, templateID, buildID, artifactURL string) error {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&pb.PullArtifactRequest{
		TemplateId:  templateID,
		BuildId:     buildID,
		ArtifactUrl: artifactURL,
	})

	resp, err := client.PullArtifact(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC PullArtifact failed: %w", err)
	}

	if !resp.Msg.Success {
		return fmt.Errorf("artifact pull failed: %s", resp.Msg.Error)
	}

	return nil
}

// GetSandboxStatus gets sandbox status from a node
func (n *GRPCNode) GetSandboxStatus(ctx context.Context, node *Node, sandboxID string) (*pb.SandboxInfo, error) {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return nil, err
	}

	req := connect.NewRequest(&pb.GetSandboxStatusRequest{
		SandboxId: sandboxID,
	})

	resp, err := client.GetSandboxStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("gRPC GetSandboxStatus failed: %w", err)
	}

	if !resp.Msg.Exists {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	return resp.Msg.Sandbox, nil
}

// ListSandboxes lists all sandboxes on a node
func (n *GRPCNode) ListSandboxes(ctx context.Context, node *Node) ([]*pb.SandboxInfo, error) {
	client, err := n.getClient(node.Spec.ID)
	if err != nil {
		return nil, err
	}

	req := connect.NewRequest(&pb.ListSandboxesRequest{})

	resp, err := client.ListSandboxes(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("gRPC ListSandboxes failed: %w", err)
	}

	return resp.Msg.Sandboxes, nil
}

// SendHeartbeat sends a heartbeat to a node (for checking connectivity)
func (n *GRPCNode) SendHeartbeat(ctx context.Context, nodeID string) error {
	client, err := n.getClient(nodeID)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&pb.HeartbeatRequest{
		NodeId: nodeID,
		Status: &pb.NodeStatus{
			State: "checking",
		},
	})

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.Heartbeat(ctx, req)
	if err != nil {
		return fmt.Errorf("heartbeat failed: %w", err)
	}

	if !resp.Msg.Acknowledged {
		return fmt.Errorf("heartbeat not acknowledged")
	}

	return nil
}
