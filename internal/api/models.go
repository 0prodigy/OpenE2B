package api

import "time"

// Error represents an API error response
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// SandboxState represents the state of a sandbox
type SandboxState string

const (
	SandboxStateRunning SandboxState = "running"
	SandboxStatePaused  SandboxState = "paused"
)

// SandboxMetadata is a map of string key-value pairs
type SandboxMetadata map[string]string

// EnvVars is a map of environment variables
type EnvVars map[string]string

// SandboxNetworkConfig holds network configuration for a sandbox
type SandboxNetworkConfig struct {
	AllowPublicTraffic *bool    `json:"allowPublicTraffic,omitempty"`
	AllowOut           []string `json:"allowOut,omitempty"`
	DenyOut            []string `json:"denyOut,omitempty"`
	MaskRequestHost    *string  `json:"maskRequestHost,omitempty"`
}

// NewSandbox is the request body for creating a sandbox
type NewSandbox struct {
	TemplateID          string                `json:"templateID"`
	Timeout             *int                  `json:"timeout,omitempty"`
	AutoPause           *bool                 `json:"autoPause,omitempty"`
	Secure              *bool                 `json:"secure,omitempty"`
	AllowInternetAccess *bool                 `json:"allow_internet_access,omitempty"`
	Network             *SandboxNetworkConfig `json:"network,omitempty"`
	Metadata            SandboxMetadata       `json:"metadata,omitempty"`
	EnvVars             EnvVars               `json:"envVars,omitempty"`
	Mcp                 map[string]any        `json:"mcp,omitempty"`
}

// Sandbox is the response when creating a sandbox
type Sandbox struct {
	TemplateID         string  `json:"templateID"`
	SandboxID          string  `json:"sandboxID"`
	Alias              *string `json:"alias,omitempty"`
	ClientID           string  `json:"clientID"`
	EnvdVersion        string  `json:"envdVersion"`
	EnvdAccessToken    *string `json:"envdAccessToken,omitempty"`
	TrafficAccessToken *string `json:"trafficAccessToken,omitempty"`
	Domain             *string `json:"domain,omitempty"`
}

// SandboxDetail provides detailed information about a sandbox
type SandboxDetail struct {
	TemplateID      string          `json:"templateID"`
	Alias           *string         `json:"alias,omitempty"`
	SandboxID       string          `json:"sandboxID"`
	ClientID        string          `json:"clientID"`
	StartedAt       time.Time       `json:"startedAt"`
	EndAt           time.Time       `json:"endAt"`
	EnvdVersion     string          `json:"envdVersion"`
	EnvdAccessToken *string         `json:"envdAccessToken,omitempty"`
	Domain          *string         `json:"domain,omitempty"`
	CPUCount        int             `json:"cpuCount"`
	MemoryMB        int             `json:"memoryMB"`
	DiskSizeMB      int             `json:"diskSizeMB"`
	Metadata        SandboxMetadata `json:"metadata,omitempty"`
	State           SandboxState    `json:"state"`
}

// ListedSandbox is a sandbox entry in a list response
type ListedSandbox struct {
	TemplateID  string          `json:"templateID"`
	Alias       *string         `json:"alias,omitempty"`
	SandboxID   string          `json:"sandboxID"`
	ClientID    string          `json:"clientID"`
	StartedAt   time.Time       `json:"startedAt"`
	EndAt       time.Time       `json:"endAt"`
	CPUCount    int             `json:"cpuCount"`
	MemoryMB    int             `json:"memoryMB"`
	DiskSizeMB  int             `json:"diskSizeMB"`
	Metadata    SandboxMetadata `json:"metadata,omitempty"`
	State       SandboxState    `json:"state"`
	EnvdVersion string          `json:"envdVersion"`
}

// ConnectSandbox is the request for connecting to a sandbox
type ConnectSandbox struct {
	Timeout int `json:"timeout"`
}

// ResumedSandbox is the request for resuming a paused sandbox
type ResumedSandbox struct {
	Timeout   *int  `json:"timeout,omitempty"`
	AutoPause *bool `json:"autoPause,omitempty"`
}

// SandboxTimeout is the request for setting sandbox timeout
type SandboxTimeout struct {
	Timeout int `json:"timeout"`
}

// SandboxRefresh is the request for refreshing sandbox TTL
type SandboxRefresh struct {
	Duration *int `json:"duration,omitempty"`
}

// SandboxMetric represents resource metrics for a sandbox
type SandboxMetric struct {
	Timestamp     time.Time `json:"timestamp"`
	TimestampUnix int64     `json:"timestampUnix"`
	CPUCount      int       `json:"cpuCount"`
	CPUUsedPct    float32   `json:"cpuUsedPct"`
	MemUsed       int64     `json:"memUsed"`
	MemTotal      int64     `json:"memTotal"`
	DiskUsed      int64     `json:"diskUsed"`
	DiskTotal     int64     `json:"diskTotal"`
}

// SandboxesWithMetrics holds sandbox metrics keyed by sandbox ID
type SandboxesWithMetrics struct {
	Sandboxes map[string]SandboxMetric `json:"sandboxes"`
}

// SandboxLog represents a log entry
type SandboxLog struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

// LogLevel represents log severity
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// SandboxLogEntry represents a structured log entry
type SandboxLogEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Message   string            `json:"message"`
	Level     LogLevel          `json:"level"`
	Fields    map[string]string `json:"fields"`
}

// SandboxLogs holds logs for a sandbox
type SandboxLogs struct {
	Logs       []SandboxLog      `json:"logs"`
	LogEntries []SandboxLogEntry `json:"logEntries"`
}

// TeamUser represents a user in a team
type TeamUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Team represents a team
type Team struct {
	TeamID    string `json:"teamID"`
	Name      string `json:"name"`
	APIKey    string `json:"apiKey"`
	IsDefault bool   `json:"isDefault"`
}

// TeamMetric represents team-level metrics
type TeamMetric struct {
	Timestamp          time.Time `json:"timestamp"`
	TimestampUnix      int64     `json:"timestampUnix"`
	ConcurrentSandboxes int       `json:"concurrentSandboxes"`
	SandboxStartRate   float32   `json:"sandboxStartRate"`
}

// MaxTeamMetric represents maximum metric value
type MaxTeamMetric struct {
	Timestamp     time.Time `json:"timestamp"`
	TimestampUnix int64     `json:"timestampUnix"`
	Value         float64   `json:"value"`
}

// TemplateBuildStatus represents the build status
type TemplateBuildStatus string

const (
	TemplateBuildStatusBuilding TemplateBuildStatus = "building"
	TemplateBuildStatusWaiting  TemplateBuildStatus = "waiting"
	TemplateBuildStatusReady    TemplateBuildStatus = "ready"
	TemplateBuildStatusError    TemplateBuildStatus = "error"
)

// Template represents a template
type Template struct {
	TemplateID    string               `json:"templateID"`
	BuildID       string               `json:"buildID"`
	CPUCount      int                  `json:"cpuCount"`
	MemoryMB      int                  `json:"memoryMB"`
	DiskSizeMB    int                  `json:"diskSizeMB"`
	Public        bool                 `json:"public"`
	Aliases       []string             `json:"aliases"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	CreatedBy     *TeamUser            `json:"createdBy,omitempty"`
	LastSpawnedAt *time.Time           `json:"lastSpawnedAt,omitempty"`
	SpawnCount    int64                `json:"spawnCount"`
	BuildCount    int                  `json:"buildCount"`
	EnvdVersion   string               `json:"envdVersion"`
	BuildStatus   TemplateBuildStatus  `json:"buildStatus"`
}

// TemplateBuild represents a template build
type TemplateBuild struct {
	BuildID     string              `json:"buildID"`
	Status      TemplateBuildStatus `json:"status"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
	FinishedAt  *time.Time          `json:"finishedAt,omitempty"`
	CPUCount    int                 `json:"cpuCount"`
	MemoryMB    int                 `json:"memoryMB"`
	DiskSizeMB  *int                `json:"diskSizeMB,omitempty"`
	EnvdVersion *string             `json:"envdVersion,omitempty"`
}

// TemplateWithBuilds represents a template with its builds
type TemplateWithBuilds struct {
	TemplateID    string          `json:"templateID"`
	Public        bool            `json:"public"`
	Aliases       []string        `json:"aliases"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	LastSpawnedAt *time.Time      `json:"lastSpawnedAt,omitempty"`
	SpawnCount    int64           `json:"spawnCount"`
	Builds        []TemplateBuild `json:"builds"`
}

// TemplateBuildRequest is the request for building a template (legacy)
type TemplateBuildRequest struct {
	Alias      *string `json:"alias,omitempty"`
	Dockerfile string  `json:"dockerfile"`
	TeamID     *string `json:"teamID,omitempty"`
	StartCmd   *string `json:"startCmd,omitempty"`
	ReadyCmd   *string `json:"readyCmd,omitempty"`
	CPUCount   *int    `json:"cpuCount,omitempty"`
	MemoryMB   *int    `json:"memoryMB,omitempty"`
}

// TemplateBuildRequestV2 is the v2 template build request
type TemplateBuildRequestV2 struct {
	Alias    string  `json:"alias"`
	TeamID   *string `json:"teamID,omitempty"`
	CPUCount *int    `json:"cpuCount,omitempty"`
	MemoryMB *int    `json:"memoryMB,omitempty"`
}

// TemplateBuildRequestV3 is the v3 template build request
type TemplateBuildRequestV3 struct {
	Alias    string  `json:"alias"`
	TeamID   *string `json:"teamID,omitempty"`
	CPUCount *int    `json:"cpuCount,omitempty"`
	MemoryMB *int    `json:"memoryMB,omitempty"`
}

// TemplateRequestResponseV3 is the v3 template create response
type TemplateRequestResponseV3 struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Public     bool     `json:"public"`
	Aliases    []string `json:"aliases"`
}

// TemplateLegacy is the legacy template response
type TemplateLegacy struct {
	TemplateID    string     `json:"templateID"`
	BuildID       string     `json:"buildID"`
	CPUCount      int        `json:"cpuCount"`
	MemoryMB      int        `json:"memoryMB"`
	DiskSizeMB    int        `json:"diskSizeMB"`
	Public        bool       `json:"public"`
	Aliases       []string   `json:"aliases"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	CreatedBy     *TeamUser  `json:"createdBy,omitempty"`
	LastSpawnedAt *time.Time `json:"lastSpawnedAt,omitempty"`
	SpawnCount    int64      `json:"spawnCount"`
	BuildCount    int        `json:"buildCount"`
	EnvdVersion   string     `json:"envdVersion"`
}

// TemplateUpdateRequest is the request for updating a template
type TemplateUpdateRequest struct {
	Public *bool `json:"public,omitempty"`
}

// BuildLogEntry represents a build log entry
type BuildLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     LogLevel  `json:"level"`
	Step      *string   `json:"step,omitempty"`
}

// BuildStatusReason holds reason information for build status
type BuildStatusReason struct {
	Message    string          `json:"message"`
	Step       *string         `json:"step,omitempty"`
	LogEntries []BuildLogEntry `json:"logEntries,omitempty"`
}

// TemplateBuildInfo contains build information
type TemplateBuildInfo struct {
	Logs       []string            `json:"logs"`
	LogEntries []BuildLogEntry     `json:"logEntries"`
	TemplateID string              `json:"templateID"`
	BuildID    string              `json:"buildID"`
	Status     TemplateBuildStatus `json:"status"`
	Reason     *BuildStatusReason  `json:"reason,omitempty"`
}

// NodeStatus represents the status of a node
type NodeStatus string

const (
	NodeStatusReady      NodeStatus = "ready"
	NodeStatusDraining   NodeStatus = "draining"
	NodeStatusConnecting NodeStatus = "connecting"
	NodeStatusUnhealthy  NodeStatus = "unhealthy"
)

// DiskMetrics represents disk metrics for a node
type DiskMetrics struct {
	MountPoint     string `json:"mountPoint"`
	Device         string `json:"device"`
	FilesystemType string `json:"filesystemType"`
	UsedBytes      uint64 `json:"usedBytes"`
	TotalBytes     uint64 `json:"totalBytes"`
}

// NodeMetrics represents node-level metrics
type NodeMetrics struct {
	AllocatedCPU         uint32        `json:"allocatedCPU"`
	CPUPercent           uint32        `json:"cpuPercent"`
	CPUCount             uint32        `json:"cpuCount"`
	AllocatedMemoryBytes uint64        `json:"allocatedMemoryBytes"`
	MemoryUsedBytes      uint64        `json:"memoryUsedBytes"`
	MemoryTotalBytes     uint64        `json:"memoryTotalBytes"`
	Disks                []DiskMetrics `json:"disks"`
}

// Node represents a compute node
type Node struct {
	Version            string      `json:"version"`
	Commit             string      `json:"commit"`
	NodeID             string      `json:"nodeID"`
	ID                 string      `json:"id"`
	ServiceInstanceID  string      `json:"serviceInstanceID"`
	ClusterID          string      `json:"clusterID"`
	Status             NodeStatus  `json:"status"`
	SandboxCount       uint32      `json:"sandboxCount"`
	Metrics            NodeMetrics `json:"metrics"`
	CreateSuccesses    uint64      `json:"createSuccesses"`
	CreateFails        uint64      `json:"createFails"`
	SandboxStartingCount int       `json:"sandboxStartingCount"`
}

// NodeDetail provides detailed node information
type NodeDetail struct {
	ClusterID         string          `json:"clusterID"`
	Version           string          `json:"version"`
	Commit            string          `json:"commit"`
	ID                string          `json:"id"`
	ServiceInstanceID string          `json:"serviceInstanceID"`
	NodeID            string          `json:"nodeID"`
	Status            NodeStatus      `json:"status"`
	Sandboxes         []ListedSandbox `json:"sandboxes"`
	Metrics           NodeMetrics     `json:"metrics"`
	CachedBuilds      []string        `json:"cachedBuilds"`
	CreateSuccesses   uint64          `json:"createSuccesses"`
	CreateFails       uint64          `json:"createFails"`
}

// NodeStatusChange is the request for changing node status
type NodeStatusChange struct {
	ClusterID *string    `json:"clusterID,omitempty"`
	Status    NodeStatus `json:"status"`
}

// IdentifierMaskingDetails holds masking information for tokens/keys
type IdentifierMaskingDetails struct {
	Prefix            string `json:"prefix"`
	ValueLength       int    `json:"valueLength"`
	MaskedValuePrefix string `json:"maskedValuePrefix"`
	MaskedValueSuffix string `json:"maskedValueSuffix"`
}

// TeamAPIKey represents an API key for a team
type TeamAPIKey struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Mask      IdentifierMaskingDetails `json:"mask"`
	CreatedAt time.Time                `json:"createdAt"`
	CreatedBy *TeamUser                `json:"createdBy,omitempty"`
	LastUsed  *time.Time               `json:"lastUsed,omitempty"`
}

// CreatedTeamAPIKey is the response when creating an API key
type CreatedTeamAPIKey struct {
	ID        string                   `json:"id"`
	Key       string                   `json:"key"`
	Mask      IdentifierMaskingDetails `json:"mask"`
	Name      string                   `json:"name"`
	CreatedAt time.Time                `json:"createdAt"`
	CreatedBy *TeamUser                `json:"createdBy,omitempty"`
	LastUsed  *time.Time               `json:"lastUsed,omitempty"`
}

// NewTeamAPIKey is the request for creating an API key
type NewTeamAPIKey struct {
	Name string `json:"name"`
}

// UpdateTeamAPIKey is the request for updating an API key
type UpdateTeamAPIKey struct {
	Name string `json:"name"`
}

// NewAccessToken is the request for creating an access token
type NewAccessToken struct {
	Name string `json:"name"`
}

// CreatedAccessToken is the response when creating an access token
type CreatedAccessToken struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Token     string                   `json:"token"`
	Mask      IdentifierMaskingDetails `json:"mask"`
	CreatedAt time.Time                `json:"createdAt"`
}

// ============================================================================
// Build Pipeline Models (V2 SDK Format)
// ============================================================================

// TemplateStep represents a build step/instruction
// Types: COPY, ENV, RUN, WORKDIR, USER
type TemplateStep struct {
	Type      string   `json:"type"`                // COPY, ENV, RUN, WORKDIR, USER
	Args      []string `json:"args"`                // Arguments for the instruction
	Force     *bool    `json:"force,omitempty"`     // Force rebuild this step
	FilesHash *string  `json:"filesHash,omitempty"` // Hash of files (for COPY steps)
}

// FromImageRegistry contains registry credentials for pulling images
type FromImageRegistry struct {
	URL      string  `json:"url"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

// TemplateBuildStartV2 is the request body for POST /v2/templates/{id}/builds/{buildID}
type TemplateBuildStartV2 struct {
	FromImage         *string            `json:"fromImage,omitempty"`
	FromTemplate      *string            `json:"fromTemplate,omitempty"`
	FromImageRegistry *FromImageRegistry `json:"fromImageRegistry,omitempty"`
	Force             *bool              `json:"force,omitempty"`
	Steps             []TemplateStep     `json:"steps,omitempty"`
	StartCmd          *string            `json:"startCmd,omitempty"`
	ReadyCmd          *string            `json:"readyCmd,omitempty"`
}

// TemplateBuildFileUpload is the response for GET /templates/{id}/files/{hash}
type TemplateBuildFileUpload struct {
	Present bool    `json:"present"`
	URL     *string `json:"url,omitempty"`
}
