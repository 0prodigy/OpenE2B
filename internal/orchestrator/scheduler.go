package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Scheduler manages sandbox placement and lifecycle
type Scheduler struct {
	nodes     map[string]*Node
	sandboxes map[string]*Sandbox
	mu        sync.RWMutex

	// Callbacks for node operations
	nodeOps NodeOperations
}

// NodeOperations defines the interface for node management
type NodeOperations interface {
	CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) error
	StopSandbox(ctx context.Context, node *Node, sandboxID string) error
	PauseSandbox(ctx context.Context, node *Node, sandboxID string) error
	ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error
}

// NewScheduler creates a new scheduler
func NewScheduler(nodeOps NodeOperations) *Scheduler {
	s := &Scheduler{
		nodes:     make(map[string]*Node),
		sandboxes: make(map[string]*Sandbox),
		nodeOps:   nodeOps,
	}
	return s
}

// RegisterNode adds a node to the scheduler
func (s *Scheduler) RegisterNode(node *Node) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[node.Spec.ID] = node
}

// UnregisterNode removes a node from the scheduler
func (s *Scheduler) UnregisterNode(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodes, nodeID)
}

// Schedule places a sandbox on a suitable node
func (s *Scheduler) Schedule(ctx context.Context, spec SandboxSpec) (*Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find a suitable node
	node, err := s.selectNode(spec)
	if err != nil {
		return nil, err
	}

	// Create sandbox record
	sandbox := &Sandbox{
		Spec: spec,
		Status: SandboxStatus{
			State:     SandboxStateStarting,
			NodeID:    node.Spec.ID,
			StartedAt: time.Now(),
			ExpiresAt: time.Now().Add(spec.Timeout),
		},
	}

	// Create on node
	if s.nodeOps != nil {
		if err := s.nodeOps.CreateSandbox(ctx, node, spec); err != nil {
			sandbox.Status.State = SandboxStateError
			sandbox.Status.ErrorReason = err.Error()
			return nil, err
		}
	}

	sandbox.Status.State = SandboxStateRunning
	s.sandboxes[spec.ID] = sandbox

	// Update node allocations
	node.Status.AllocatedCPU += spec.CPUCount
	node.Status.AllocatedMemory += int64(spec.MemoryMB) * 1024 * 1024
	node.Status.SandboxCount++

	return sandbox, nil
}

// selectNode picks the best node for a sandbox
func (s *Scheduler) selectNode(spec SandboxSpec) (*Node, error) {
	var bestNode *Node
	var bestScore float64 = -1

	for _, node := range s.nodes {
		if node.Status.State != NodeStateReady {
			continue
		}

		// Check if node has enough resources
		availableCPU := node.Spec.CPUCount - node.Status.AllocatedCPU
		availableMemory := node.Spec.MemoryBytes - node.Status.AllocatedMemory

		if availableCPU < spec.CPUCount {
			continue
		}
		if availableMemory < int64(spec.MemoryMB)*1024*1024 {
			continue
		}

		// Score based on available resources (prefer less loaded nodes)
		score := float64(availableCPU)/float64(node.Spec.CPUCount) +
			float64(availableMemory)/float64(node.Spec.MemoryBytes)

		// Bonus for having cached build
		for _, buildID := range node.Status.CachedBuilds {
			if buildID == spec.BuildID {
				score += 0.5
				break
			}
		}

		if score > bestScore {
			bestScore = score
			bestNode = node
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node available")
	}

	return bestNode, nil
}

// Stop terminates a sandbox
func (s *Scheduler) Stop(ctx context.Context, sandboxID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sandbox, ok := s.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	node, ok := s.nodes[sandbox.Status.NodeID]
	if !ok {
		return fmt.Errorf("node not found: %s", sandbox.Status.NodeID)
	}

	if s.nodeOps != nil {
		if err := s.nodeOps.StopSandbox(ctx, node, sandboxID); err != nil {
			return err
		}
	}

	// Update node allocations
	node.Status.AllocatedCPU -= sandbox.Spec.CPUCount
	node.Status.AllocatedMemory -= int64(sandbox.Spec.MemoryMB) * 1024 * 1024
	node.Status.SandboxCount--

	sandbox.Status.State = SandboxStateStopped
	delete(s.sandboxes, sandboxID)

	return nil
}

// Pause pauses a sandbox
func (s *Scheduler) Pause(ctx context.Context, sandboxID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sandbox, ok := s.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	if sandbox.Status.State != SandboxStateRunning {
		return fmt.Errorf("sandbox is not running")
	}

	node, ok := s.nodes[sandbox.Status.NodeID]
	if ok && s.nodeOps != nil {
		if err := s.nodeOps.PauseSandbox(ctx, node, sandboxID); err != nil {
			return err
		}
	}

	sandbox.Status.State = SandboxStatePaused
	return nil
}

// Resume resumes a paused sandbox
func (s *Scheduler) Resume(ctx context.Context, sandboxID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sandbox, ok := s.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	if sandbox.Status.State != SandboxStatePaused {
		return fmt.Errorf("sandbox is not paused")
	}

	node, ok := s.nodes[sandbox.Status.NodeID]
	if ok && s.nodeOps != nil {
		if err := s.nodeOps.ResumeSandbox(ctx, node, sandboxID); err != nil {
			return err
		}
	}

	sandbox.Status.State = SandboxStateRunning
	return nil
}

// ExtendTimeout extends a sandbox's TTL
func (s *Scheduler) ExtendTimeout(sandboxID string, duration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sandbox, ok := s.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	sandbox.Status.ExpiresAt = time.Now().Add(duration)
	return nil
}

// GetSandbox returns a sandbox by ID
func (s *Scheduler) GetSandbox(sandboxID string) (*Sandbox, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sb, ok := s.sandboxes[sandboxID]
	return sb, ok
}

// ListSandboxes returns all sandboxes
func (s *Scheduler) ListSandboxes() []*Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Sandbox, 0, len(s.sandboxes))
	for _, sb := range s.sandboxes {
		result = append(result, sb)
	}
	return result
}

// ListNodes returns all nodes
func (s *Scheduler) ListNodes() []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		result = append(result, n)
	}
	return result
}

// StartExpirationChecker starts a goroutine to check for expired sandboxes
func (s *Scheduler) StartExpirationChecker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.checkExpirations(ctx)
			}
		}
	}()
}

func (s *Scheduler) checkExpirations(ctx context.Context) {
	s.mu.RLock()
	var expired []string
	now := time.Now()
	for id, sb := range s.sandboxes {
		if sb.Status.State == SandboxStateRunning && now.After(sb.Status.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	s.mu.RUnlock()

	for _, id := range expired {
		s.mu.RLock()
		sb, ok := s.sandboxes[id]
		s.mu.RUnlock()
		if !ok {
			continue
		}

		if sb.Spec.AutoPause {
			s.Pause(ctx, id)
		} else {
			s.Stop(ctx, id)
		}
	}
}
