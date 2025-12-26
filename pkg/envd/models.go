package envd

// EnvVars is a map of environment variables
type EnvVars map[string]string

// Error represents an error response
type Error struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// EntryInfo represents file/directory information
type EntryInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Metrics represents resource usage metrics
type Metrics struct {
	Timestamp  int64   `json:"ts"`
	CPUCount   int     `json:"cpu_count"`
	CPUUsedPct float32 `json:"cpu_used_pct"`
	MemTotal   int64   `json:"mem_total"`
	MemUsed    int64   `json:"mem_used"`
	DiskUsed   int64   `json:"disk_used"`
	DiskTotal  int64   `json:"disk_total"`
}

// InitRequest represents the init request body
type InitRequest struct {
	HyperloopIP    *string `json:"hyperloopIP,omitempty"`
	EnvVars        EnvVars `json:"envVars,omitempty"`
	AccessToken    *string `json:"accessToken,omitempty"`
	Timestamp      *string `json:"timestamp,omitempty"`
	DefaultUser    *string `json:"defaultUser,omitempty"`
	DefaultWorkdir *string `json:"defaultWorkdir,omitempty"`
}
