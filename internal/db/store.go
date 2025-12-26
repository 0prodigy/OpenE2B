package db

import (
	"context"
	"time"
)

// Store defines the interface for persistent storage
// This allows swapping between in-memory and database implementations
type Store interface {
	// Sandbox operations
	CreateSandbox(ctx context.Context, sandbox *Sandbox) error
	GetSandbox(ctx context.Context, sandboxID string) (*Sandbox, error)
	ListSandboxes(ctx context.Context, filter *SandboxFilter) ([]*Sandbox, error)
	UpdateSandbox(ctx context.Context, sandboxID string, updates *SandboxUpdate) error
	DeleteSandbox(ctx context.Context, sandboxID string) error

	// Template operations
	CreateTemplate(ctx context.Context, template *Template, build *Build) error
	GetTemplate(ctx context.Context, idOrAlias string) (*Template, error)
	ListTemplates(ctx context.Context, teamID *string) ([]*Template, error)
	UpdateTemplate(ctx context.Context, templateID string, updates *TemplateUpdate) error
	DeleteTemplate(ctx context.Context, templateID string) error
	IncrementTemplateSpawnCount(ctx context.Context, templateID string) error

	// Build operations
	CreateBuild(ctx context.Context, build *Build) error
	GetBuild(ctx context.Context, buildID string) (*Build, error)
	ListBuildsForTemplate(ctx context.Context, templateID string) ([]*Build, error)
	UpdateBuild(ctx context.Context, buildID string, updates *BuildUpdate) error

	// Node operations
	CreateNode(ctx context.Context, node *Node) error
	GetNode(ctx context.Context, nodeID string) (*Node, error)
	ListNodes(ctx context.Context) ([]*Node, error)
	UpdateNodeStatus(ctx context.Context, nodeID string, status string) error
	UpdateNodeMetrics(ctx context.Context, nodeID string, metrics *NodeMetrics) error

	// Team operations
	GetTeam(ctx context.Context, teamID string) (*Team, error)
	ListTeams(ctx context.Context) ([]*Team, error)
	CreateTeam(ctx context.Context, team *Team) error

	// API Key operations
	CreateAPIKey(ctx context.Context, apiKey *APIKey) error
	GetAPIKeyByHash(ctx context.Context, hashedKey string) (*APIKey, error)
	ListAPIKeys(ctx context.Context, teamID string) ([]*APIKey, error)
	DeleteAPIKey(ctx context.Context, keyID string) error
	UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error

	// Health check
	Ping(ctx context.Context) error

	// Close the store connection
	Close() error
}

// Sandbox represents a sandbox in the database
type Sandbox struct {
	SandboxID       string            `json:"sandbox_id"`
	TemplateID      string            `json:"template_id"`
	Alias           *string           `json:"alias,omitempty"`
	ClientID        string            `json:"client_id"`
	EnvdVersion     string            `json:"envd_version"`
	EnvdAccessToken string            `json:"envd_access_token"`
	Domain          string            `json:"domain"`
	CPUCount        int               `json:"cpu_count"`
	MemoryMB        int               `json:"memory_mb"`
	DiskSizeMB      int               `json:"disk_size_mb"`
	Metadata        map[string]string `json:"metadata"`
	EnvVars         map[string]string `json:"env_vars"`
	State           string            `json:"state"`
	StartedAt       time.Time         `json:"started_at"`
	EndAt           time.Time         `json:"end_at"`
	AutoPause       bool              `json:"auto_pause"`
	NodeID          string            `json:"node_id,omitempty"`
	TeamID          string            `json:"team_id,omitempty"`
}

// SandboxFilter defines filters for listing sandboxes
type SandboxFilter struct {
	Metadata map[string]string
	States   []string
	TeamID   *string
	NodeID   *string
}

// SandboxUpdate defines updatable fields for a sandbox
type SandboxUpdate struct {
	State  *string
	EndAt  *time.Time
	NodeID *string
}

// Template represents a template in the database
type Template struct {
	TemplateID    string     `json:"template_id"`
	BuildID       string     `json:"build_id"`
	CPUCount      int        `json:"cpu_count"`
	MemoryMB      int        `json:"memory_mb"`
	DiskSizeMB    int        `json:"disk_size_mb"`
	Public        bool       `json:"public"`
	Aliases       []string   `json:"aliases"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CreatedByID   *string    `json:"created_by_id,omitempty"`
	LastSpawnedAt *time.Time `json:"last_spawned_at,omitempty"`
	SpawnCount    int64      `json:"spawn_count"`
	BuildCount    int        `json:"build_count"`
	EnvdVersion   string     `json:"envd_version"`
	BuildStatus   string     `json:"build_status"`
	TeamID        string     `json:"team_id,omitempty"`
}

// TemplateUpdate defines updatable fields for a template
type TemplateUpdate struct {
	Public      *bool
	BuildStatus *string
	BuildID     *string
}

// Build represents a template build in the database
type Build struct {
	BuildID     string       `json:"build_id"`
	TemplateID  string       `json:"template_id"`
	Status      string       `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	FinishedAt  *time.Time   `json:"finished_at,omitempty"`
	CPUCount    int          `json:"cpu_count"`
	MemoryMB    int          `json:"memory_mb"`
	DiskSizeMB  *int         `json:"disk_size_mb,omitempty"`
	EnvdVersion *string      `json:"envd_version,omitempty"`
	Logs        []string     `json:"logs"`
	LogEntries  []LogEntry   `json:"log_entries"`
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     string    `json:"level"`
	Step      *string   `json:"step,omitempty"`
}

// BuildUpdate defines updatable fields for a build
type BuildUpdate struct {
	Status      *string
	FinishedAt  *time.Time
	Logs        []string
	LogEntries  []LogEntry
	DiskSizeMB  *int
	EnvdVersion *string
}

// Node represents a compute node in the database
type Node struct {
	NodeID               string       `json:"node_id"`
	ID                   string       `json:"id"`
	Version              string       `json:"version"`
	Commit               string       `json:"commit"`
	ServiceInstanceID    string       `json:"service_instance_id"`
	ClusterID            string       `json:"cluster_id"`
	Status               string       `json:"status"`
	SandboxCount         int          `json:"sandbox_count"`
	SandboxStartingCount int          `json:"sandbox_starting_count"`
	CreateSuccesses      int64        `json:"create_successes"`
	CreateFails          int64        `json:"create_fails"`
	Metrics              *NodeMetrics `json:"metrics,omitempty"`
	CachedBuilds         []string     `json:"cached_builds"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

// NodeMetrics represents node-level metrics
type NodeMetrics struct {
	AllocatedCPU         uint32        `json:"allocated_cpu"`
	CPUPercent           uint32        `json:"cpu_percent"`
	CPUCount             uint32        `json:"cpu_count"`
	AllocatedMemoryBytes uint64        `json:"allocated_memory_bytes"`
	MemoryUsedBytes      uint64        `json:"memory_used_bytes"`
	MemoryTotalBytes     uint64        `json:"memory_total_bytes"`
	Disks                []DiskMetrics `json:"disks"`
}

// DiskMetrics represents disk metrics
type DiskMetrics struct {
	MountPoint     string `json:"mount_point"`
	Device         string `json:"device"`
	FilesystemType string `json:"filesystem_type"`
	UsedBytes      uint64 `json:"used_bytes"`
	TotalBytes     uint64 `json:"total_bytes"`
}

// Team represents a team in the database
type Team struct {
	TeamID    string    `json:"team_id"`
	Name      string    `json:"name"`
	APIKey    string    `json:"api_key"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TeamUser represents a user in a team
type TeamUser struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"team_id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKey represents an API key in the database
type APIKey struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	HashedKey         string     `json:"hashed_key"`
	TeamID            string     `json:"team_id"`
	MaskPrefix        string     `json:"mask_prefix"`
	MaskValueLength   int        `json:"mask_value_length"`
	MaskedValuePrefix string     `json:"masked_value_prefix"`
	MaskedValueSuffix string     `json:"masked_value_suffix"`
	CreatedAt         time.Time  `json:"created_at"`
	CreatedByID       *string    `json:"created_by_id,omitempty"`
	LastUsed          *time.Time `json:"last_used,omitempty"`
}
