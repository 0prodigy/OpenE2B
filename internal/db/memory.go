package db

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryStore implements Store using in-memory maps
// This is useful for development and testing without a database
type MemoryStore struct {
	mu sync.RWMutex

	sandboxes map[string]*Sandbox
	templates map[string]*Template
	builds    map[string]*Build
	nodes     map[string]*Node
	teams     map[string]*Team
	apiKeys   map[string]*APIKey
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *MemoryStore {
	store := &MemoryStore{
		sandboxes: make(map[string]*Sandbox),
		templates: make(map[string]*Template),
		builds:    make(map[string]*Build),
		nodes:     make(map[string]*Node),
		teams:     make(map[string]*Team),
		apiKeys:   make(map[string]*APIKey),
	}

	// Seed with default team
	store.teams["default"] = &Team{
		TeamID:    "default",
		Name:      "Default Team",
		APIKey:    "e2b_default_key",
		IsDefault: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return store
}

// Ping checks store availability
func (s *MemoryStore) Ping(ctx context.Context) error {
	return nil
}

// Close closes the store (no-op for memory store)
func (s *MemoryStore) Close() error {
	return nil
}

// ============================================================================
// Sandbox Operations
// ============================================================================

func (s *MemoryStore) CreateSandbox(ctx context.Context, sandbox *Sandbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sandboxes[sandbox.SandboxID] = sandbox
	return nil
}

func (s *MemoryStore) GetSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sandbox, ok := s.sandboxes[sandboxID]
	if !ok {
		return nil, nil
	}
	return sandbox, nil
}

func (s *MemoryStore) ListSandboxes(ctx context.Context, filter *SandboxFilter) ([]*Sandbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Sandbox
	for _, sandbox := range s.sandboxes {
		// Filter by state if specified
		if filter != nil && len(filter.States) > 0 {
			found := false
			for _, state := range filter.States {
				if sandbox.State == state {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by metadata if specified
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

		// Filter by team if specified
		if filter != nil && filter.TeamID != nil && sandbox.TeamID != *filter.TeamID {
			continue
		}

		// Filter by node if specified
		if filter != nil && filter.NodeID != nil && sandbox.NodeID != *filter.NodeID {
			continue
		}

		result = append(result, sandbox)
	}

	return result, nil
}

func (s *MemoryStore) UpdateSandbox(ctx context.Context, sandboxID string, updates *SandboxUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sandbox, ok := s.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found")
	}

	if updates.State != nil {
		sandbox.State = *updates.State
	}
	if updates.EndAt != nil {
		sandbox.EndAt = *updates.EndAt
	}
	if updates.NodeID != nil {
		sandbox.NodeID = *updates.NodeID
	}

	return nil
}

func (s *MemoryStore) DeleteSandbox(ctx context.Context, sandboxID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sandboxes[sandboxID]; !ok {
		return fmt.Errorf("sandbox not found")
	}

	delete(s.sandboxes, sandboxID)
	return nil
}

// ============================================================================
// Template Operations
// ============================================================================

func (s *MemoryStore) CreateTemplate(ctx context.Context, template *Template, build *Build) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.templates[template.TemplateID] = template
	if build != nil {
		s.builds[build.BuildID] = build
	}
	return nil
}

func (s *MemoryStore) GetTemplate(ctx context.Context, idOrAlias string) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// First try by ID
	if template, ok := s.templates[idOrAlias]; ok {
		return template, nil
	}

	// Then search by alias
	for _, template := range s.templates {
		for _, alias := range template.Aliases {
			if alias == idOrAlias {
				return template, nil
			}
		}
	}

	return nil, nil
}

func (s *MemoryStore) ListTemplates(ctx context.Context, teamID *string) ([]*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Template
	for _, template := range s.templates {
		if teamID != nil && template.TeamID != *teamID {
			continue
		}
		result = append(result, template)
	}

	return result, nil
}

func (s *MemoryStore) UpdateTemplate(ctx context.Context, templateID string, updates *TemplateUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	template, ok := s.templates[templateID]
	if !ok {
		return fmt.Errorf("template not found")
	}

	if updates.Public != nil {
		template.Public = *updates.Public
	}
	if updates.BuildStatus != nil {
		template.BuildStatus = *updates.BuildStatus
	}
	if updates.BuildID != nil {
		template.BuildID = *updates.BuildID
	}
	template.UpdatedAt = time.Now()

	return nil
}

func (s *MemoryStore) DeleteTemplate(ctx context.Context, templateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.templates[templateID]; !ok {
		return fmt.Errorf("template not found")
	}

	delete(s.templates, templateID)
	return nil
}

func (s *MemoryStore) IncrementTemplateSpawnCount(ctx context.Context, templateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	template, ok := s.templates[templateID]
	if !ok {
		return nil // Silently ignore if template doesn't exist
	}

	template.SpawnCount++
	now := time.Now()
	template.LastSpawnedAt = &now

	return nil
}

// ============================================================================
// Build Operations
// ============================================================================

func (s *MemoryStore) CreateBuild(ctx context.Context, build *Build) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.builds[build.BuildID] = build

	// Increment template build count
	if template, ok := s.templates[build.TemplateID]; ok {
		template.BuildCount++
		template.UpdatedAt = time.Now()
	}

	return nil
}

func (s *MemoryStore) GetBuild(ctx context.Context, buildID string) (*Build, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	build, ok := s.builds[buildID]
	if !ok {
		return nil, nil
	}
	return build, nil
}

func (s *MemoryStore) ListBuildsForTemplate(ctx context.Context, templateID string) ([]*Build, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Build
	for _, build := range s.builds {
		if build.TemplateID == templateID {
			result = append(result, build)
		}
	}

	return result, nil
}

func (s *MemoryStore) UpdateBuild(ctx context.Context, buildID string, updates *BuildUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	build, ok := s.builds[buildID]
	if !ok {
		return fmt.Errorf("build not found")
	}

	if updates.Status != nil {
		build.Status = *updates.Status
	}
	if updates.FinishedAt != nil {
		build.FinishedAt = updates.FinishedAt
	}
	if updates.Logs != nil {
		build.Logs = updates.Logs
	}
	if updates.LogEntries != nil {
		build.LogEntries = updates.LogEntries
	}
	build.UpdatedAt = time.Now()

	return nil
}

// ============================================================================
// Node Operations
// ============================================================================

func (s *MemoryStore) CreateNode(ctx context.Context, node *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nodes[node.NodeID] = node
	return nil
}

func (s *MemoryStore) GetNode(ctx context.Context, nodeID string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return nil, nil
	}
	return node, nil
}

func (s *MemoryStore) ListNodes(ctx context.Context) ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Node
	for _, node := range s.nodes {
		result = append(result, node)
	}

	return result, nil
}

func (s *MemoryStore) UpdateNodeStatus(ctx context.Context, nodeID string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return fmt.Errorf("node not found")
	}

	node.Status = status
	node.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) UpdateNodeMetrics(ctx context.Context, nodeID string, metrics *NodeMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return fmt.Errorf("node not found")
	}

	node.Metrics = metrics
	node.UpdatedAt = time.Now()
	return nil
}

// ============================================================================
// Team Operations
// ============================================================================

func (s *MemoryStore) GetTeam(ctx context.Context, teamID string) (*Team, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	team, ok := s.teams[teamID]
	if !ok {
		return nil, nil
	}
	return team, nil
}

func (s *MemoryStore) ListTeams(ctx context.Context) ([]*Team, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Team
	for _, team := range s.teams {
		result = append(result, team)
	}

	return result, nil
}

func (s *MemoryStore) CreateTeam(ctx context.Context, team *Team) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.teams[team.TeamID] = team
	return nil
}

// ============================================================================
// API Key Operations
// ============================================================================

func (s *MemoryStore) CreateAPIKey(ctx context.Context, apiKey *APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.apiKeys[apiKey.ID] = apiKey
	return nil
}

func (s *MemoryStore) GetAPIKeyByHash(ctx context.Context, hashedKey string) (*APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, apiKey := range s.apiKeys {
		if apiKey.HashedKey == hashedKey {
			return apiKey, nil
		}
	}
	return nil, nil
}

func (s *MemoryStore) ListAPIKeys(ctx context.Context, teamID string) ([]*APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*APIKey
	for _, apiKey := range s.apiKeys {
		if apiKey.TeamID == teamID {
			result = append(result, apiKey)
		}
	}

	return result, nil
}

func (s *MemoryStore) DeleteAPIKey(ctx context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.apiKeys[keyID]; !ok {
		return fmt.Errorf("API key not found")
	}

	delete(s.apiKeys, keyID)
	return nil
}

func (s *MemoryStore) UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	apiKey, ok := s.apiKeys[keyID]
	if !ok {
		return fmt.Errorf("API key not found")
	}

	now := time.Now()
	apiKey.LastUsed = &now
	return nil
}
