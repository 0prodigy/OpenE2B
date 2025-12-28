package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SandboxState represents the lifecycle state of a sandbox
type SandboxState string

const (
	SandboxStatePending  SandboxState = "pending"
	SandboxStateStarting SandboxState = "starting"
	SandboxStateRunning  SandboxState = "running"
	SandboxStatePaused   SandboxState = "paused"
	SandboxStateStopping SandboxState = "stopping"
	SandboxStateStopped  SandboxState = "stopped"
	SandboxStateError    SandboxState = "error"
)

// SandboxSpec defines the desired state for a sandbox
type SandboxSpec struct {
	ID            string
	TemplateID    string
	BuildID       string
	CPUCount      int
	MemoryMB      int
	DiskSizeMB    int
	EnvVars       map[string]string
	Metadata      map[string]string
	Timeout       time.Duration
	AutoPause     bool
	EnvdToken     string
	NetworkConfig *NetworkConfig
}

// NetworkConfig defines sandbox network settings
type NetworkConfig struct {
	AllowPublicTraffic bool
	AllowOut           []string
	DenyOut            []string
	MaskRequestHost    string
}

// SandboxStatus represents the current observed state
type SandboxStatus struct {
	State       SandboxState
	NodeID      string
	HostPort    int
	EnvdPort    int
	StartedAt   time.Time
	ExpiresAt   time.Time
	ErrorReason string
}

// Sandbox combines spec and status
type Sandbox struct {
	Spec   SandboxSpec
	Status SandboxStatus
}

// NodeState represents the health state of a node
type NodeState string

const (
	NodeStateReady      NodeState = "ready"
	NodeStateDraining   NodeState = "draining"
	NodeStateConnecting NodeState = "connecting"
	NodeStateUnhealthy  NodeState = "unhealthy"
)

// NodeSpec defines node configuration
type NodeSpec struct {
	ID          string
	ClusterID   string
	Address     string
	CPUCount    int
	MemoryBytes int64
}

// NodeStatus represents current node state
type NodeStatus struct {
	State           NodeState
	AllocatedCPU    int
	AllocatedMemory int64
	SandboxCount    int
	CachedBuilds    []string
	LastHeartbeat   time.Time
}

// Node combines spec and status
type Node struct {
	Spec   NodeSpec
	Status NodeStatus
}

// NodeOperations defines the interface for node management (implemented by Orchestrator clients)
type NodeOperations interface {
	CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) (*SandboxResult, error)
	StopSandbox(ctx context.Context, node *Node, sandboxID string) error
	PauseSandbox(ctx context.Context, node *Node, sandboxID string) error
	ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error
	GetSandboxHost(sandboxID string) (host string, port int, ok bool)
}

// SandboxResult contains the result of creating a sandbox
type SandboxResult struct {
	HostPort int
	EnvdPort int
}

// Scheduler manages sandbox placement and lifecycle in the control plane
type Scheduler struct {
	nodes     map[string]*Node
	sandboxes map[string]*Sandbox
	mu        sync.RWMutex

	// nodeOps is the interface for communicating with Orchestrator nodes
	nodeOps NodeOperations
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

	// Create on node via Orchestrator
	if s.nodeOps != nil {
		result, err := s.nodeOps.CreateSandbox(ctx, node, spec)
		if err != nil {
			sandbox.Status.State = SandboxStateError
			sandbox.Status.ErrorReason = err.Error()
			return nil, err
		}
		if result != nil {
			sandbox.Status.HostPort = result.HostPort
			sandbox.Status.EnvdPort = result.EnvdPort
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

// GetSandboxHost returns the host and port for a sandbox (for proxy routing)
func (s *Scheduler) GetSandboxHost(sandboxID string) (host string, port int, state SandboxState, ok bool) {
	s.mu.RLock()
	sandbox, exists := s.sandboxes[sandboxID]
	s.mu.RUnlock()

	if !exists {
		return "", 0, "", false
	}

	// Get the host from the node
	node, nodeExists := s.nodes[sandbox.Status.NodeID]
	if !nodeExists {
		return "", 0, sandbox.Status.State, false
	}

	host = node.Spec.Address
	if host == "" {
		host = "localhost" // Default for local development
	}

	return host, sandbox.Status.HostPort, sandbox.Status.State, true
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
