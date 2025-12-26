package orchestrator

import (
	"time"
)

// SandboxState represents the lifecycle state of a sandbox
type SandboxState string

const (
	SandboxStatePending   SandboxState = "pending"
	SandboxStateStarting  SandboxState = "starting"
	SandboxStateRunning   SandboxState = "running"
	SandboxStatePaused    SandboxState = "paused"
	SandboxStateStopping  SandboxState = "stopping"
	SandboxStateStopped   SandboxState = "stopped"
	SandboxStateError     SandboxState = "error"
)

// SandboxSpec defines the desired state for a sandbox
type SandboxSpec struct {
	ID          string
	TemplateID  string
	BuildID     string
	CPUCount    int
	MemoryMB    int
	DiskSizeMB  int
	EnvVars     map[string]string
	Metadata    map[string]string
	Timeout     time.Duration
	AutoPause   bool
	EnvdToken   string
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
	ID              string
	ClusterID       string
	Address         string
	CPUCount        int
	MemoryBytes     int64
}

// NodeStatus represents current node state
type NodeStatus struct {
	State             NodeState
	AllocatedCPU      int
	AllocatedMemory   int64
	SandboxCount      int
	CachedBuilds      []string
	LastHeartbeat     time.Time
}

// Node combines spec and status
type Node struct {
	Spec   NodeSpec
	Status NodeStatus
}

// TemplateImage represents a template image for sandboxes
type TemplateImage struct {
	TemplateID  string
	BuildID     string
	ImageURI    string
	RootfsPath  string
	CPUCount    int
	MemoryMB    int
	DiskSizeMB  int
	EnvdVersion string
}
