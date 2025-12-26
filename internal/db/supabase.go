package db

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// SupabaseStore implements Store using Supabase REST API
type SupabaseStore struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewSupabaseStore creates a new Supabase REST API store
func NewSupabaseStore(supabaseURL, apiKey string) (*SupabaseStore, error) {
	if supabaseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("supabaseURL and apiKey are required")
	}

	store := &SupabaseStore{
		baseURL: supabaseURL + "/rest/v1",
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Test connection
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to Supabase: %w", err)
	}

	return store, nil
}

// doRequest performs an HTTP request to Supabase
func (s *SupabaseStore) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("apikey", s.apiKey)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=representation")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// Ping checks Supabase connectivity
func (s *SupabaseStore) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", s.apiKey)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the store (no-op for REST API)
func (s *SupabaseStore) Close() error {
	return nil
}

// ============================================================================
// Sandbox Operations
// ============================================================================

// supabaseSandbox represents a sandbox row in Supabase
type supabaseSandbox struct {
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
	NodeID          *string           `json:"node_id,omitempty"`
	TeamID          *string           `json:"team_id,omitempty"`
}

func (s *SupabaseStore) CreateSandbox(ctx context.Context, sandbox *Sandbox) error {
	sbx := supabaseSandbox{
		SandboxID:       sandbox.SandboxID,
		TemplateID:      sandbox.TemplateID,
		Alias:           sandbox.Alias,
		ClientID:        sandbox.ClientID,
		EnvdVersion:     sandbox.EnvdVersion,
		EnvdAccessToken: sandbox.EnvdAccessToken,
		Domain:          sandbox.Domain,
		CPUCount:        sandbox.CPUCount,
		MemoryMB:        sandbox.MemoryMB,
		DiskSizeMB:      sandbox.DiskSizeMB,
		Metadata:        sandbox.Metadata,
		EnvVars:         sandbox.EnvVars,
		State:           sandbox.State,
		StartedAt:       sandbox.StartedAt,
		EndAt:           sandbox.EndAt,
		AutoPause:       sandbox.AutoPause,
	}
	if sandbox.NodeID != "" {
		sbx.NodeID = &sandbox.NodeID
	}
	if sandbox.TeamID != "" {
		sbx.TeamID = &sandbox.TeamID
	}

	return s.doRequest(ctx, "POST", "/sandboxes", sbx, nil)
}

func (s *SupabaseStore) GetSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	path := fmt.Sprintf("/sandboxes?sandbox_id=eq.%s&select=*", url.QueryEscape(sandboxID))

	var results []supabaseSandbox
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	sbx := results[0]
	sandbox := &Sandbox{
		SandboxID:       sbx.SandboxID,
		TemplateID:      sbx.TemplateID,
		Alias:           sbx.Alias,
		ClientID:        sbx.ClientID,
		EnvdVersion:     sbx.EnvdVersion,
		EnvdAccessToken: sbx.EnvdAccessToken,
		Domain:          sbx.Domain,
		CPUCount:        sbx.CPUCount,
		MemoryMB:        sbx.MemoryMB,
		DiskSizeMB:      sbx.DiskSizeMB,
		Metadata:        sbx.Metadata,
		EnvVars:         sbx.EnvVars,
		State:           sbx.State,
		StartedAt:       sbx.StartedAt,
		EndAt:           sbx.EndAt,
		AutoPause:       sbx.AutoPause,
	}
	if sbx.NodeID != nil {
		sandbox.NodeID = *sbx.NodeID
	}
	if sbx.TeamID != nil {
		sandbox.TeamID = *sbx.TeamID
	}
	return sandbox, nil
}

func (s *SupabaseStore) ListSandboxes(ctx context.Context, filter *SandboxFilter) ([]*Sandbox, error) {
	path := "/sandboxes?select=*"

	if filter != nil {
		if len(filter.States) > 0 {
			// Use 'in' filter for multiple states
			path += "&state=in.(" + joinStrings(filter.States) + ")"
		}
		if filter.TeamID != nil {
			path += "&team_id=eq." + url.QueryEscape(*filter.TeamID)
		}
		if filter.NodeID != nil {
			path += "&node_id=eq." + url.QueryEscape(*filter.NodeID)
		}
	}

	var results []supabaseSandbox
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	var sandboxes []*Sandbox
	for _, sbx := range results {
		sandbox := &Sandbox{
			SandboxID:       sbx.SandboxID,
			TemplateID:      sbx.TemplateID,
			Alias:           sbx.Alias,
			ClientID:        sbx.ClientID,
			EnvdVersion:     sbx.EnvdVersion,
			EnvdAccessToken: sbx.EnvdAccessToken,
			Domain:          sbx.Domain,
			CPUCount:        sbx.CPUCount,
			MemoryMB:        sbx.MemoryMB,
			DiskSizeMB:      sbx.DiskSizeMB,
			Metadata:        sbx.Metadata,
			EnvVars:         sbx.EnvVars,
			State:           sbx.State,
			StartedAt:       sbx.StartedAt,
			EndAt:           sbx.EndAt,
			AutoPause:       sbx.AutoPause,
		}
		if sbx.NodeID != nil {
			sandbox.NodeID = *sbx.NodeID
		}
		if sbx.TeamID != nil {
			sandbox.TeamID = *sbx.TeamID
		}

		// Filter by metadata if specified (done client-side)
		if filter != nil && len(filter.Metadata) > 0 {
			match := true
			for k, v := range filter.Metadata {
				if sandbox.Metadata[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		sandboxes = append(sandboxes, sandbox)
	}

	return sandboxes, nil
}

func (s *SupabaseStore) UpdateSandbox(ctx context.Context, sandboxID string, updates *SandboxUpdate) error {
	if updates == nil {
		return nil
	}

	updateMap := make(map[string]interface{})
	if updates.State != nil {
		updateMap["state"] = *updates.State
	}
	if updates.EndAt != nil {
		updateMap["end_at"] = *updates.EndAt
	}
	if updates.NodeID != nil {
		updateMap["node_id"] = *updates.NodeID
	}

	if len(updateMap) == 0 {
		return nil
	}

	path := fmt.Sprintf("/sandboxes?sandbox_id=eq.%s", url.QueryEscape(sandboxID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

func (s *SupabaseStore) DeleteSandbox(ctx context.Context, sandboxID string) error {
	path := fmt.Sprintf("/sandboxes?sandbox_id=eq.%s", url.QueryEscape(sandboxID))
	return s.doRequest(ctx, "DELETE", path, nil, nil)
}

// ============================================================================
// Template Operations
// ============================================================================

type supabaseTemplate struct {
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
	TeamID        *string    `json:"team_id,omitempty"`
}

type supabaseBuild struct {
	BuildID     string      `json:"build_id"`
	TemplateID  string      `json:"template_id"`
	Status      string      `json:"status"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	FinishedAt  *time.Time  `json:"finished_at,omitempty"`
	CPUCount    int         `json:"cpu_count"`
	MemoryMB    int         `json:"memory_mb"`
	DiskSizeMB  *int        `json:"disk_size_mb,omitempty"`
	EnvdVersion *string     `json:"envd_version,omitempty"`
	Logs        []string    `json:"logs"`
	LogEntries  []LogEntry  `json:"log_entries"`
}

func (s *SupabaseStore) CreateTemplate(ctx context.Context, template *Template, build *Build) error {
	tmpl := supabaseTemplate{
		TemplateID:    template.TemplateID,
		BuildID:       template.BuildID,
		CPUCount:      template.CPUCount,
		MemoryMB:      template.MemoryMB,
		DiskSizeMB:    template.DiskSizeMB,
		Public:        template.Public,
		Aliases:       template.Aliases,
		CreatedAt:     template.CreatedAt,
		UpdatedAt:     template.UpdatedAt,
		CreatedByID:   template.CreatedByID,
		LastSpawnedAt: template.LastSpawnedAt,
		SpawnCount:    template.SpawnCount,
		BuildCount:    template.BuildCount,
		EnvdVersion:   template.EnvdVersion,
		BuildStatus:   template.BuildStatus,
	}
	if template.TeamID != "" {
		tmpl.TeamID = &template.TeamID
	}

	if err := s.doRequest(ctx, "POST", "/templates", tmpl, nil); err != nil {
		return err
	}

	if build != nil {
		bld := supabaseBuild{
			BuildID:     build.BuildID,
			TemplateID:  build.TemplateID,
			Status:      build.Status,
			CreatedAt:   build.CreatedAt,
			UpdatedAt:   build.UpdatedAt,
			FinishedAt:  build.FinishedAt,
			CPUCount:    build.CPUCount,
			MemoryMB:    build.MemoryMB,
			DiskSizeMB:  build.DiskSizeMB,
			EnvdVersion: build.EnvdVersion,
			Logs:        build.Logs,
			LogEntries:  build.LogEntries,
		}
		if err := s.doRequest(ctx, "POST", "/builds", bld, nil); err != nil {
			return err
		}
	}

	return nil
}

func (s *SupabaseStore) GetTemplate(ctx context.Context, idOrAlias string) (*Template, error) {
	// Try by ID first
	path := fmt.Sprintf("/templates?template_id=eq.%s&select=*", url.QueryEscape(idOrAlias))

	var results []supabaseTemplate
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		// Try by alias
		path = fmt.Sprintf("/templates?aliases=cs.{%s}&select=*", url.QueryEscape(idOrAlias))
		if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
			return nil, err
		}
	}

	if len(results) == 0 {
		return nil, nil
	}

	tmpl := results[0]
	template := &Template{
		TemplateID:    tmpl.TemplateID,
		BuildID:       tmpl.BuildID,
		CPUCount:      tmpl.CPUCount,
		MemoryMB:      tmpl.MemoryMB,
		DiskSizeMB:    tmpl.DiskSizeMB,
		Public:        tmpl.Public,
		Aliases:       tmpl.Aliases,
		CreatedAt:     tmpl.CreatedAt,
		UpdatedAt:     tmpl.UpdatedAt,
		CreatedByID:   tmpl.CreatedByID,
		LastSpawnedAt: tmpl.LastSpawnedAt,
		SpawnCount:    tmpl.SpawnCount,
		BuildCount:    tmpl.BuildCount,
		EnvdVersion:   tmpl.EnvdVersion,
		BuildStatus:   tmpl.BuildStatus,
	}
	if tmpl.TeamID != nil {
		template.TeamID = *tmpl.TeamID
	}
	return template, nil
}

func (s *SupabaseStore) ListTemplates(ctx context.Context, teamID *string) ([]*Template, error) {
	path := "/templates?select=*&order=created_at.desc"
	if teamID != nil {
		path += "&team_id=eq." + url.QueryEscape(*teamID)
	}

	var results []supabaseTemplate
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	var templates []*Template
	for _, tmpl := range results {
		template := &Template{
			TemplateID:    tmpl.TemplateID,
			BuildID:       tmpl.BuildID,
			CPUCount:      tmpl.CPUCount,
			MemoryMB:      tmpl.MemoryMB,
			DiskSizeMB:    tmpl.DiskSizeMB,
			Public:        tmpl.Public,
			Aliases:       tmpl.Aliases,
			CreatedAt:     tmpl.CreatedAt,
			UpdatedAt:     tmpl.UpdatedAt,
			CreatedByID:   tmpl.CreatedByID,
			LastSpawnedAt: tmpl.LastSpawnedAt,
			SpawnCount:    tmpl.SpawnCount,
			BuildCount:    tmpl.BuildCount,
			EnvdVersion:   tmpl.EnvdVersion,
			BuildStatus:   tmpl.BuildStatus,
		}
		if tmpl.TeamID != nil {
			template.TeamID = *tmpl.TeamID
		}
		templates = append(templates, template)
	}

	return templates, nil
}

func (s *SupabaseStore) UpdateTemplate(ctx context.Context, templateID string, updates *TemplateUpdate) error {
	if updates == nil {
		return nil
	}

	updateMap := make(map[string]interface{})
	if updates.Public != nil {
		updateMap["public"] = *updates.Public
	}
	if updates.BuildStatus != nil {
		updateMap["build_status"] = *updates.BuildStatus
	}
	if updates.BuildID != nil {
		updateMap["build_id"] = *updates.BuildID
	}

	if len(updateMap) == 0 {
		return nil
	}

	path := fmt.Sprintf("/templates?template_id=eq.%s", url.QueryEscape(templateID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

func (s *SupabaseStore) DeleteTemplate(ctx context.Context, templateID string) error {
	path := fmt.Sprintf("/templates?template_id=eq.%s", url.QueryEscape(templateID))
	return s.doRequest(ctx, "DELETE", path, nil, nil)
}

func (s *SupabaseStore) IncrementTemplateSpawnCount(ctx context.Context, templateID string) error {
	// Get current template first
	template, err := s.GetTemplate(ctx, templateID)
	if err != nil || template == nil {
		return nil
	}

	updateMap := map[string]interface{}{
		"spawn_count":     template.SpawnCount + 1,
		"last_spawned_at": time.Now(),
	}

	path := fmt.Sprintf("/templates?template_id=eq.%s", url.QueryEscape(templateID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

// ============================================================================
// Build Operations
// ============================================================================

func (s *SupabaseStore) CreateBuild(ctx context.Context, build *Build) error {
	bld := supabaseBuild{
		BuildID:     build.BuildID,
		TemplateID:  build.TemplateID,
		Status:      build.Status,
		CreatedAt:   build.CreatedAt,
		UpdatedAt:   build.UpdatedAt,
		FinishedAt:  build.FinishedAt,
		CPUCount:    build.CPUCount,
		MemoryMB:    build.MemoryMB,
		DiskSizeMB:  build.DiskSizeMB,
		EnvdVersion: build.EnvdVersion,
		Logs:        build.Logs,
		LogEntries:  build.LogEntries,
	}

	if err := s.doRequest(ctx, "POST", "/builds", bld, nil); err != nil {
		return fmt.Errorf("failed to create build: %w", err)
	}

	return nil
}

func (s *SupabaseStore) GetBuild(ctx context.Context, buildID string) (*Build, error) {
	path := fmt.Sprintf("/builds?build_id=eq.%s&select=*", url.QueryEscape(buildID))

	var results []supabaseBuild
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	bld := results[0]
	return &Build{
		BuildID:     bld.BuildID,
		TemplateID:  bld.TemplateID,
		Status:      bld.Status,
		CreatedAt:   bld.CreatedAt,
		UpdatedAt:   bld.UpdatedAt,
		FinishedAt:  bld.FinishedAt,
		CPUCount:    bld.CPUCount,
		MemoryMB:    bld.MemoryMB,
		DiskSizeMB:  bld.DiskSizeMB,
		EnvdVersion: bld.EnvdVersion,
		Logs:        bld.Logs,
		LogEntries:  bld.LogEntries,
	}, nil
}

func (s *SupabaseStore) ListBuildsForTemplate(ctx context.Context, templateID string) ([]*Build, error) {
	path := fmt.Sprintf("/builds?template_id=eq.%s&select=*&order=created_at.desc", url.QueryEscape(templateID))

	var results []supabaseBuild
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	var builds []*Build
	for _, bld := range results {
		builds = append(builds, &Build{
			BuildID:     bld.BuildID,
			TemplateID:  bld.TemplateID,
			Status:      bld.Status,
			CreatedAt:   bld.CreatedAt,
			UpdatedAt:   bld.UpdatedAt,
			FinishedAt:  bld.FinishedAt,
			CPUCount:    bld.CPUCount,
			MemoryMB:    bld.MemoryMB,
			DiskSizeMB:  bld.DiskSizeMB,
			EnvdVersion: bld.EnvdVersion,
			Logs:        bld.Logs,
			LogEntries:  bld.LogEntries,
		})
	}

	return builds, nil
}

func (s *SupabaseStore) UpdateBuild(ctx context.Context, buildID string, updates *BuildUpdate) error {
	if updates == nil {
		return nil
	}

	updateMap := make(map[string]interface{})
	if updates.Status != nil {
		updateMap["status"] = *updates.Status
	}
	if updates.FinishedAt != nil {
		updateMap["finished_at"] = *updates.FinishedAt
	}
	if updates.Logs != nil {
		updateMap["logs"] = updates.Logs
	}
	if updates.LogEntries != nil {
		updateMap["log_entries"] = updates.LogEntries
	}

	if len(updateMap) == 0 {
		return nil
	}

	path := fmt.Sprintf("/builds?build_id=eq.%s", url.QueryEscape(buildID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

// ============================================================================
// Node Operations
// ============================================================================

type supabaseNode struct {
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

func (s *SupabaseStore) CreateNode(ctx context.Context, node *Node) error {
	n := supabaseNode{
		NodeID:               node.NodeID,
		ID:                   node.ID,
		Version:              node.Version,
		Commit:               node.Commit,
		ServiceInstanceID:    node.ServiceInstanceID,
		ClusterID:            node.ClusterID,
		Status:               node.Status,
		SandboxCount:         node.SandboxCount,
		SandboxStartingCount: node.SandboxStartingCount,
		CreateSuccesses:      node.CreateSuccesses,
		CreateFails:          node.CreateFails,
		Metrics:              node.Metrics,
		CachedBuilds:         node.CachedBuilds,
		CreatedAt:            node.CreatedAt,
		UpdatedAt:            node.UpdatedAt,
	}
	return s.doRequest(ctx, "POST", "/nodes", n, nil)
}

func (s *SupabaseStore) GetNode(ctx context.Context, nodeID string) (*Node, error) {
	path := fmt.Sprintf("/nodes?node_id=eq.%s&select=*", url.QueryEscape(nodeID))

	var results []supabaseNode
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	n := results[0]
	return &Node{
		NodeID:               n.NodeID,
		ID:                   n.ID,
		Version:              n.Version,
		Commit:               n.Commit,
		ServiceInstanceID:    n.ServiceInstanceID,
		ClusterID:            n.ClusterID,
		Status:               n.Status,
		SandboxCount:         n.SandboxCount,
		SandboxStartingCount: n.SandboxStartingCount,
		CreateSuccesses:      n.CreateSuccesses,
		CreateFails:          n.CreateFails,
		Metrics:              n.Metrics,
		CachedBuilds:         n.CachedBuilds,
		CreatedAt:            n.CreatedAt,
		UpdatedAt:            n.UpdatedAt,
	}, nil
}

func (s *SupabaseStore) ListNodes(ctx context.Context) ([]*Node, error) {
	path := "/nodes?select=*&order=created_at.desc"

	var results []supabaseNode
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	var nodes []*Node
	for _, n := range results {
		nodes = append(nodes, &Node{
			NodeID:               n.NodeID,
			ID:                   n.ID,
			Version:              n.Version,
			Commit:               n.Commit,
			ServiceInstanceID:    n.ServiceInstanceID,
			ClusterID:            n.ClusterID,
			Status:               n.Status,
			SandboxCount:         n.SandboxCount,
			SandboxStartingCount: n.SandboxStartingCount,
			CreateSuccesses:      n.CreateSuccesses,
			CreateFails:          n.CreateFails,
			Metrics:              n.Metrics,
			CachedBuilds:         n.CachedBuilds,
			CreatedAt:            n.CreatedAt,
			UpdatedAt:            n.UpdatedAt,
		})
	}

	return nodes, nil
}

func (s *SupabaseStore) UpdateNodeStatus(ctx context.Context, nodeID string, status string) error {
	updateMap := map[string]interface{}{
		"status": status,
	}
	path := fmt.Sprintf("/nodes?node_id=eq.%s", url.QueryEscape(nodeID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

func (s *SupabaseStore) UpdateNodeMetrics(ctx context.Context, nodeID string, metrics *NodeMetrics) error {
	updateMap := map[string]interface{}{
		"metrics": metrics,
	}
	path := fmt.Sprintf("/nodes?node_id=eq.%s", url.QueryEscape(nodeID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

// ============================================================================
// Team Operations
// ============================================================================

type supabaseTeam struct {
	TeamID    string    `json:"team_id"`
	Name      string    `json:"name"`
	APIKey    string    `json:"api_key"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *SupabaseStore) GetTeam(ctx context.Context, teamID string) (*Team, error) {
	path := fmt.Sprintf("/teams?team_id=eq.%s&select=*", url.QueryEscape(teamID))

	var results []supabaseTeam
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	t := results[0]
	return &Team{
		TeamID:    t.TeamID,
		Name:      t.Name,
		APIKey:    t.APIKey,
		IsDefault: t.IsDefault,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}, nil
}

func (s *SupabaseStore) ListTeams(ctx context.Context) ([]*Team, error) {
	path := "/teams?select=*&order=created_at.desc"

	var results []supabaseTeam
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	var teams []*Team
	for _, t := range results {
		teams = append(teams, &Team{
			TeamID:    t.TeamID,
			Name:      t.Name,
			APIKey:    t.APIKey,
			IsDefault: t.IsDefault,
			CreatedAt: t.CreatedAt,
			UpdatedAt: t.UpdatedAt,
		})
	}

	return teams, nil
}

func (s *SupabaseStore) CreateTeam(ctx context.Context, team *Team) error {
	t := supabaseTeam{
		TeamID:    team.TeamID,
		Name:      team.Name,
		APIKey:    team.APIKey,
		IsDefault: team.IsDefault,
		CreatedAt: team.CreatedAt,
		UpdatedAt: team.UpdatedAt,
	}
	return s.doRequest(ctx, "POST", "/teams", t, nil)
}

// ============================================================================
// API Key Operations
// ============================================================================

type supabaseAPIKey struct {
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

func (s *SupabaseStore) CreateAPIKey(ctx context.Context, apiKey *APIKey) error {
	k := supabaseAPIKey{
		ID:                apiKey.ID,
		Name:              apiKey.Name,
		HashedKey:         apiKey.HashedKey,
		TeamID:            apiKey.TeamID,
		MaskPrefix:        apiKey.MaskPrefix,
		MaskValueLength:   apiKey.MaskValueLength,
		MaskedValuePrefix: apiKey.MaskedValuePrefix,
		MaskedValueSuffix: apiKey.MaskedValueSuffix,
		CreatedAt:         apiKey.CreatedAt,
		CreatedByID:       apiKey.CreatedByID,
		LastUsed:          apiKey.LastUsed,
	}
	return s.doRequest(ctx, "POST", "/api_keys", k, nil)
}

func (s *SupabaseStore) GetAPIKeyByHash(ctx context.Context, hashedKey string) (*APIKey, error) {
	path := fmt.Sprintf("/api_keys?hashed_key=eq.%s&select=*", url.QueryEscape(hashedKey))

	var results []supabaseAPIKey
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	k := results[0]
	return &APIKey{
		ID:                k.ID,
		Name:              k.Name,
		HashedKey:         k.HashedKey,
		TeamID:            k.TeamID,
		MaskPrefix:        k.MaskPrefix,
		MaskValueLength:   k.MaskValueLength,
		MaskedValuePrefix: k.MaskedValuePrefix,
		MaskedValueSuffix: k.MaskedValueSuffix,
		CreatedAt:         k.CreatedAt,
		CreatedByID:       k.CreatedByID,
		LastUsed:          k.LastUsed,
	}, nil
}

func (s *SupabaseStore) ListAPIKeys(ctx context.Context, teamID string) ([]*APIKey, error) {
	path := fmt.Sprintf("/api_keys?team_id=eq.%s&select=*&order=created_at.desc", url.QueryEscape(teamID))

	var results []supabaseAPIKey
	if err := s.doRequest(ctx, "GET", path, nil, &results); err != nil {
		return nil, err
	}

	var apiKeys []*APIKey
	for _, k := range results {
		apiKeys = append(apiKeys, &APIKey{
			ID:                k.ID,
			Name:              k.Name,
			HashedKey:         k.HashedKey,
			TeamID:            k.TeamID,
			MaskPrefix:        k.MaskPrefix,
			MaskValueLength:   k.MaskValueLength,
			MaskedValuePrefix: k.MaskedValuePrefix,
			MaskedValueSuffix: k.MaskedValueSuffix,
			CreatedAt:         k.CreatedAt,
			CreatedByID:       k.CreatedByID,
			LastUsed:          k.LastUsed,
		})
	}

	return apiKeys, nil
}

func (s *SupabaseStore) DeleteAPIKey(ctx context.Context, keyID string) error {
	path := fmt.Sprintf("/api_keys?id=eq.%s", url.QueryEscape(keyID))
	return s.doRequest(ctx, "DELETE", path, nil, nil)
}

func (s *SupabaseStore) UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error {
	updateMap := map[string]interface{}{
		"last_used": time.Now(),
	}
	path := fmt.Sprintf("/api_keys?id=eq.%s", url.QueryEscape(keyID))
	return s.doRequest(ctx, "PATCH", path, updateMap, nil)
}

// ============================================================================
// Helper Functions
// ============================================================================

func joinStrings(strs []string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}
