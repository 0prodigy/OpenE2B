package orchestrator

import (
	"context"
	"testing"
	"time"
)

// mockNodeOps is a mock implementation of NodeOperations
type mockNodeOps struct {
	createCalled int
	stopCalled   int
	pauseCalled  int
	resumeCalled int
}

func (m *mockNodeOps) CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) error {
	m.createCalled++
	return nil
}

func (m *mockNodeOps) StopSandbox(ctx context.Context, node *Node, sandboxID string) error {
	m.stopCalled++
	return nil
}

func (m *mockNodeOps) PauseSandbox(ctx context.Context, node *Node, sandboxID string) error {
	m.pauseCalled++
	return nil
}

func (m *mockNodeOps) ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error {
	m.resumeCalled++
	return nil
}

func TestScheduler_Schedule(t *testing.T) {
	ops := &mockNodeOps{}
	scheduler := NewScheduler(ops)

	// Register a node
	node := &Node{
		Spec: NodeSpec{
			ID:          "node-1",
			ClusterID:   "cluster-1",
			CPUCount:    4,
			MemoryBytes: 8 * 1024 * 1024 * 1024, // 8GB
		},
		Status: NodeStatus{
			State: NodeStateReady,
		},
	}
	scheduler.RegisterNode(node)

	// Schedule a sandbox
	spec := SandboxSpec{
		ID:         "sandbox-1",
		TemplateID: "template-1",
		CPUCount:   1,
		MemoryMB:   512,
		Timeout:    5 * time.Minute,
	}

	ctx := context.Background()
	sandbox, err := scheduler.Schedule(ctx, spec)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	if sandbox.Status.State != SandboxStateRunning {
		t.Errorf("Expected state %s, got %s", SandboxStateRunning, sandbox.Status.State)
	}

	if sandbox.Status.NodeID != "node-1" {
		t.Errorf("Expected nodeID %s, got %s", "node-1", sandbox.Status.NodeID)
	}

	if ops.createCalled != 1 {
		t.Errorf("Expected CreateSandbox to be called once, got %d", ops.createCalled)
	}

	// Verify node allocations updated
	if node.Status.AllocatedCPU != 1 {
		t.Errorf("Expected allocated CPU 1, got %d", node.Status.AllocatedCPU)
	}
}

func TestScheduler_NoAvailableNode(t *testing.T) {
	ops := &mockNodeOps{}
	scheduler := NewScheduler(ops)

	// Don't register any nodes
	spec := SandboxSpec{
		ID:         "sandbox-1",
		TemplateID: "template-1",
		CPUCount:   1,
		MemoryMB:   512,
		Timeout:    5 * time.Minute,
	}

	ctx := context.Background()
	_, err := scheduler.Schedule(ctx, spec)
	if err == nil {
		t.Error("Expected error when no nodes available")
	}
}

func TestScheduler_PauseResume(t *testing.T) {
	ops := &mockNodeOps{}
	scheduler := NewScheduler(ops)

	node := &Node{
		Spec: NodeSpec{
			ID:          "node-1",
			CPUCount:    4,
			MemoryBytes: 8 * 1024 * 1024 * 1024,
		},
		Status: NodeStatus{
			State: NodeStateReady,
		},
	}
	scheduler.RegisterNode(node)

	spec := SandboxSpec{
		ID:       "sandbox-1",
		CPUCount: 1,
		MemoryMB: 512,
		Timeout:  5 * time.Minute,
	}

	ctx := context.Background()
	sandbox, _ := scheduler.Schedule(ctx, spec)

	// Pause
	if err := scheduler.Pause(ctx, "sandbox-1"); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	sandbox, _ = scheduler.GetSandbox("sandbox-1")
	if sandbox.Status.State != SandboxStatePaused {
		t.Errorf("Expected paused state, got %s", sandbox.Status.State)
	}

	// Resume
	if err := scheduler.Resume(ctx, "sandbox-1"); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	sandbox, _ = scheduler.GetSandbox("sandbox-1")
	if sandbox.Status.State != SandboxStateRunning {
		t.Errorf("Expected running state, got %s", sandbox.Status.State)
	}
}

func TestScheduler_ExtendTimeout(t *testing.T) {
	ops := &mockNodeOps{}
	scheduler := NewScheduler(ops)

	node := &Node{
		Spec: NodeSpec{
			ID:          "node-1",
			CPUCount:    4,
			MemoryBytes: 8 * 1024 * 1024 * 1024,
		},
		Status: NodeStatus{
			State: NodeStateReady,
		},
	}
	scheduler.RegisterNode(node)

	spec := SandboxSpec{
		ID:       "sandbox-1",
		CPUCount: 1,
		MemoryMB: 512,
		Timeout:  1 * time.Second,
	}

	ctx := context.Background()
	scheduler.Schedule(ctx, spec)

	// Extend timeout
	newDuration := 1 * time.Hour
	if err := scheduler.ExtendTimeout("sandbox-1", newDuration); err != nil {
		t.Fatalf("ExtendTimeout failed: %v", err)
	}

	sandbox, _ := scheduler.GetSandbox("sandbox-1")
	expectedExpiry := time.Now().Add(newDuration)
	if sandbox.Status.ExpiresAt.Before(expectedExpiry.Add(-1*time.Second)) ||
		sandbox.Status.ExpiresAt.After(expectedExpiry.Add(1*time.Second)) {
		t.Errorf("Expiry time not updated correctly")
	}
}
