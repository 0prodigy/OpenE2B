package api

import (
	"context"
	"fmt"
	"time"

	"github.com/0prodigy/OpenE2B/internal/db"
	"github.com/google/uuid"
)

// Store provides storage operations for sandboxes, templates, and nodes.
// This wraps the db.Store interface and provides API-specific operations.
type Store struct {
	db db.Store
}

// SandboxRecord holds the internal state for a sandbox
type SandboxRecord struct {
	SandboxID       string
	TemplateID      string
	Alias           *string
	ClientID        string
	EnvdVersion     string
	EnvdAccessToken string
	Domain          string
	CPUCount        int
	MemoryMB        int
	DiskSizeMB      int
	Metadata        SandboxMetadata
	EnvVars         EnvVars
	State           SandboxState
	StartedAt       time.Time
	EndAt           time.Time
	AutoPause       bool
	NodeID          string
}

// TemplateRecord holds the internal state for a template
type TemplateRecord struct {
	TemplateID    string
	BuildID       string
	CPUCount      int
	MemoryMB      int
	DiskSizeMB    int
	Public        bool
	Aliases       []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CreatedBy     *TeamUser
	LastSpawnedAt *time.Time
	SpawnCount    int64
	BuildCount    int
	EnvdVersion   string
	BuildStatus   TemplateBuildStatus
	TeamID        string
}

// BuildRecord holds build state
type BuildRecord struct {
	BuildID     string
	TemplateID  string
	Status      TemplateBuildStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	FinishedAt  *time.Time
	CPUCount    int
	MemoryMB    int
	DiskSizeMB  *int
	EnvdVersion *string
	Logs        []string
	LogEntries  []BuildLogEntry
}

// NodeRecord holds node state
type NodeRecord struct {
	Node
	Sandboxes    []string
	CachedBuilds []string
}

// TeamRecord holds team state
type TeamRecord struct {
	Team
}

// APIKeyRecord holds API key state
type APIKeyRecord struct {
	TeamAPIKey
	HashedKey string
	TeamID    string
}

// NewStore creates a new API store with the given database store
func NewStore(dbStore db.Store) *Store {
	return &Store{
		db: dbStore,
	}
}

// CreateSandbox creates a new sandbox
func (s *Store) CreateSandbox(req NewSandbox, templateRec *TemplateRecord, domain string) (*SandboxRecord, error) {
	ctx := context.Background()

	sandboxID := generateSandboxID()
	now := time.Now()
	timeout := 15
	if req.Timeout != nil {
		timeout = *req.Timeout
	}

	record := &SandboxRecord{
		SandboxID:       sandboxID,
		TemplateID:      templateRec.TemplateID, // Use actual template ID, not alias
		ClientID:        sandboxID,              // deprecated, same as sandboxID
		EnvdVersion:     templateRec.EnvdVersion,
		EnvdAccessToken: generateToken(),
		Domain:          domain,
		CPUCount:        templateRec.CPUCount,
		MemoryMB:        templateRec.MemoryMB,
		DiskSizeMB:      templateRec.DiskSizeMB,
		Metadata:        req.Metadata,
		EnvVars:         req.EnvVars,
		State:           SandboxStateRunning,
		StartedAt:       now,
		EndAt:           now.Add(time.Duration(timeout) * time.Second),
		AutoPause:       req.AutoPause != nil && *req.AutoPause,
	}

	if len(templateRec.Aliases) > 0 {
		alias := templateRec.Aliases[0]
		record.Alias = &alias
	}

	// Convert to db.Sandbox and save
	dbSandbox := &db.Sandbox{
		SandboxID:       record.SandboxID,
		TemplateID:      record.TemplateID,
		Alias:           record.Alias,
		ClientID:        record.ClientID,
		EnvdVersion:     record.EnvdVersion,
		EnvdAccessToken: record.EnvdAccessToken,
		Domain:          record.Domain,
		CPUCount:        record.CPUCount,
		MemoryMB:        record.MemoryMB,
		DiskSizeMB:      record.DiskSizeMB,
		Metadata:        map[string]string(record.Metadata),
		EnvVars:         map[string]string(record.EnvVars),
		State:           string(record.State),
		StartedAt:       record.StartedAt,
		EndAt:           record.EndAt,
		AutoPause:       record.AutoPause,
		NodeID:          record.NodeID,
	}

	if err := s.db.CreateSandbox(ctx, dbSandbox); err != nil {
		return nil, err
	}

	// Update template spawn stats
	_ = s.db.IncrementTemplateSpawnCount(ctx, templateRec.TemplateID)

	return record, nil
}

// GetSandbox retrieves a sandbox by ID
func (s *Store) GetSandbox(sandboxID string) (*SandboxRecord, bool) {
	ctx := context.Background()

	dbSandbox, err := s.db.GetSandbox(ctx, sandboxID)
	if err != nil || dbSandbox == nil {
		return nil, false
	}

	return sandboxFromDB(dbSandbox), true
}

// ListSandboxes returns all sandboxes, optionally filtered
func (s *Store) ListSandboxes(metadata map[string]string, states []SandboxState) []*SandboxRecord {
	ctx := context.Background()

	filter := &db.SandboxFilter{
		Metadata: metadata,
	}
	for _, state := range states {
		filter.States = append(filter.States, string(state))
	}

	dbSandboxes, err := s.db.ListSandboxes(ctx, filter)
	if err != nil {
		return nil
	}

	var result []*SandboxRecord
	for _, dbSandbox := range dbSandboxes {
		result = append(result, sandboxFromDB(dbSandbox))
	}
	return result
}

// DeleteSandbox removes a sandbox
func (s *Store) DeleteSandbox(sandboxID string) bool {
	ctx := context.Background()

	err := s.db.DeleteSandbox(ctx, sandboxID)
	return err == nil
}

// UpdateSandboxTimeout updates the sandbox expiration time
func (s *Store) UpdateSandboxTimeout(sandboxID string, timeout int) bool {
	ctx := context.Background()

	endAt := time.Now().Add(time.Duration(timeout) * time.Second)
	err := s.db.UpdateSandbox(ctx, sandboxID, &db.SandboxUpdate{
		EndAt: &endAt,
	})
	return err == nil
}

// UpdateSandboxState updates the sandbox state
func (s *Store) UpdateSandboxState(sandboxID string, state SandboxState) bool {
	ctx := context.Background()

	stateStr := string(state)
	err := s.db.UpdateSandbox(ctx, sandboxID, &db.SandboxUpdate{
		State: &stateStr,
	})
	return err == nil
}

// CreateTemplate creates a new template
func (s *Store) CreateTemplate(req TemplateBuildRequestV3) (*TemplateRecord, *BuildRecord, error) {
	ctx := context.Background()

	templateID := generateTemplateID()
	buildID := uuid.New().String()
	now := time.Now()

	cpuCount := 1
	if req.CPUCount != nil {
		cpuCount = *req.CPUCount
	}
	memoryMB := 512
	if req.MemoryMB != nil {
		memoryMB = *req.MemoryMB
	}

	templateRec := &TemplateRecord{
		TemplateID:  templateID,
		BuildID:     buildID,
		CPUCount:    cpuCount,
		MemoryMB:    memoryMB,
		DiskSizeMB:  512,
		Public:      false,
		Aliases:     []string{req.Alias},
		CreatedAt:   now,
		UpdatedAt:   now,
		SpawnCount:  0,
		BuildCount:  1,
		EnvdVersion: "0.1.0",
		BuildStatus: TemplateBuildStatusWaiting,
	}
	if req.TeamID != nil {
		templateRec.TeamID = *req.TeamID
	}

	buildRec := &BuildRecord{
		BuildID:    buildID,
		TemplateID: templateID,
		Status:     TemplateBuildStatusWaiting,
		CreatedAt:  now,
		UpdatedAt:  now,
		CPUCount:   cpuCount,
		MemoryMB:   memoryMB,
		Logs:       []string{},
		LogEntries: []BuildLogEntry{},
	}

	// Convert to db types and save
	dbTemplate := &db.Template{
		TemplateID:  templateRec.TemplateID,
		BuildID:     templateRec.BuildID,
		CPUCount:    templateRec.CPUCount,
		MemoryMB:    templateRec.MemoryMB,
		DiskSizeMB:  templateRec.DiskSizeMB,
		Public:      templateRec.Public,
		Aliases:     templateRec.Aliases,
		CreatedAt:   templateRec.CreatedAt,
		UpdatedAt:   templateRec.UpdatedAt,
		SpawnCount:  templateRec.SpawnCount,
		BuildCount:  templateRec.BuildCount,
		EnvdVersion: templateRec.EnvdVersion,
		BuildStatus: string(templateRec.BuildStatus),
		TeamID:      templateRec.TeamID,
	}

	dbBuild := &db.Build{
		BuildID:    buildRec.BuildID,
		TemplateID: buildRec.TemplateID,
		Status:     string(buildRec.Status),
		CreatedAt:  buildRec.CreatedAt,
		UpdatedAt:  buildRec.UpdatedAt,
		CPUCount:   buildRec.CPUCount,
		MemoryMB:   buildRec.MemoryMB,
		Logs:       buildRec.Logs,
		LogEntries: []db.LogEntry{},
	}

	if err := s.db.CreateTemplate(ctx, dbTemplate, dbBuild); err != nil {
		return nil, nil, err
	}

	return templateRec, buildRec, nil
}

// GetTemplate retrieves a template by ID or alias
func (s *Store) GetTemplate(idOrAlias string) (*TemplateRecord, bool) {
	ctx := context.Background()

	dbTemplate, err := s.db.GetTemplate(ctx, idOrAlias)
	if err != nil || dbTemplate == nil {
		return nil, false
	}

	return templateFromDB(dbTemplate), true
}

// ListTemplates returns all templates
func (s *Store) ListTemplates(teamID *string) []*TemplateRecord {
	ctx := context.Background()

	dbTemplates, err := s.db.ListTemplates(ctx, teamID)
	if err != nil {
		return nil
	}

	var result []*TemplateRecord
	for _, dbTemplate := range dbTemplates {
		result = append(result, templateFromDB(dbTemplate))
	}
	return result
}

// DeleteTemplate removes a template
func (s *Store) DeleteTemplate(templateID string) bool {
	ctx := context.Background()

	err := s.db.DeleteTemplate(ctx, templateID)
	return err == nil
}

// UpdateTemplate updates a template
func (s *Store) UpdateTemplate(templateID string, req TemplateUpdateRequest) bool {
	ctx := context.Background()

	updates := &db.TemplateUpdate{
		Public: req.Public,
	}

	err := s.db.UpdateTemplate(ctx, templateID, updates)
	return err == nil
}

// GetBuild retrieves a build by ID
func (s *Store) GetBuild(buildID string) (*BuildRecord, bool) {
	ctx := context.Background()

	dbBuild, err := s.db.GetBuild(ctx, buildID)
	if err != nil || dbBuild == nil {
		return nil, false
	}

	return buildFromDB(dbBuild), true
}

// CreateBuild creates a new build for a template
func (s *Store) CreateBuild(templateID string, cpuCount, memoryMB int) (*BuildRecord, error) {
	ctx := context.Background()

	// Verify template exists
	template, err := s.db.GetTemplate(ctx, templateID)
	if err != nil || template == nil {
		return nil, fmt.Errorf("template not found: %s", templateID)
	}

	buildID := uuid.New().String()
	now := time.Now()

	buildRec := &BuildRecord{
		BuildID:    buildID,
		TemplateID: templateID,
		Status:     TemplateBuildStatusWaiting,
		CreatedAt:  now,
		UpdatedAt:  now,
		CPUCount:   cpuCount,
		MemoryMB:   memoryMB,
		Logs:       []string{},
		LogEntries: []BuildLogEntry{},
	}

	dbBuild := &db.Build{
		BuildID:    buildRec.BuildID,
		TemplateID: buildRec.TemplateID,
		Status:     string(buildRec.Status),
		CreatedAt:  buildRec.CreatedAt,
		UpdatedAt:  buildRec.UpdatedAt,
		CPUCount:   buildRec.CPUCount,
		MemoryMB:   buildRec.MemoryMB,
		Logs:       buildRec.Logs,
		LogEntries: []db.LogEntry{},
	}

	if err := s.db.CreateBuild(ctx, dbBuild); err != nil {
		return nil, err
	}

	return buildRec, nil
}

// UpdateBuildStatus updates a build's status
func (s *Store) UpdateBuildStatus(buildID string, status TemplateBuildStatus) bool {
	ctx := context.Background()

	statusStr := string(status)
	err := s.db.UpdateBuild(ctx, buildID, &db.BuildUpdate{
		Status: &statusStr,
	})
	return err == nil
}

// GetBuildsForTemplate returns all builds for a template
func (s *Store) GetBuildsForTemplate(templateID string) []*BuildRecord {
	ctx := context.Background()

	dbBuilds, err := s.db.ListBuildsForTemplate(ctx, templateID)
	if err != nil {
		return nil
	}

	var result []*BuildRecord
	for _, dbBuild := range dbBuilds {
		result = append(result, buildFromDB(dbBuild))
	}
	return result
}

// ListNodes returns all nodes
func (s *Store) ListNodes() []*NodeRecord {
	ctx := context.Background()

	dbNodes, err := s.db.ListNodes(ctx)
	if err != nil {
		return nil
	}

	var result []*NodeRecord
	for _, dbNode := range dbNodes {
		result = append(result, nodeFromDB(dbNode))
	}
	return result
}

// GetNode retrieves a node by ID
func (s *Store) GetNode(nodeID string) (*NodeRecord, bool) {
	ctx := context.Background()

	dbNode, err := s.db.GetNode(ctx, nodeID)
	if err != nil || dbNode == nil {
		return nil, false
	}

	return nodeFromDB(dbNode), true
}

// UpdateNodeStatus updates a node's status
func (s *Store) UpdateNodeStatus(nodeID string, status NodeStatus) bool {
	ctx := context.Background()

	err := s.db.UpdateNodeStatus(ctx, nodeID, string(status))
	return err == nil
}

// GetDB returns the underlying database store
func (s *Store) GetDB() db.Store {
	return s.db
}

// Close closes the store
func (s *Store) Close() error {
	return s.db.Close()
}

// ============================================================================
// Conversion Functions
// ============================================================================

func sandboxFromDB(dbSandbox *db.Sandbox) *SandboxRecord {
	return &SandboxRecord{
		SandboxID:       dbSandbox.SandboxID,
		TemplateID:      dbSandbox.TemplateID,
		Alias:           dbSandbox.Alias,
		ClientID:        dbSandbox.ClientID,
		EnvdVersion:     dbSandbox.EnvdVersion,
		EnvdAccessToken: dbSandbox.EnvdAccessToken,
		Domain:          dbSandbox.Domain,
		CPUCount:        dbSandbox.CPUCount,
		MemoryMB:        dbSandbox.MemoryMB,
		DiskSizeMB:      dbSandbox.DiskSizeMB,
		Metadata:        SandboxMetadata(dbSandbox.Metadata),
		EnvVars:         EnvVars(dbSandbox.EnvVars),
		State:           SandboxState(dbSandbox.State),
		StartedAt:       dbSandbox.StartedAt,
		EndAt:           dbSandbox.EndAt,
		AutoPause:       dbSandbox.AutoPause,
		NodeID:          dbSandbox.NodeID,
	}
}

func templateFromDB(dbTemplate *db.Template) *TemplateRecord {
	return &TemplateRecord{
		TemplateID:    dbTemplate.TemplateID,
		BuildID:       dbTemplate.BuildID,
		CPUCount:      dbTemplate.CPUCount,
		MemoryMB:      dbTemplate.MemoryMB,
		DiskSizeMB:    dbTemplate.DiskSizeMB,
		Public:        dbTemplate.Public,
		Aliases:       dbTemplate.Aliases,
		CreatedAt:     dbTemplate.CreatedAt,
		UpdatedAt:     dbTemplate.UpdatedAt,
		LastSpawnedAt: dbTemplate.LastSpawnedAt,
		SpawnCount:    dbTemplate.SpawnCount,
		BuildCount:    dbTemplate.BuildCount,
		EnvdVersion:   dbTemplate.EnvdVersion,
		BuildStatus:   TemplateBuildStatus(dbTemplate.BuildStatus),
		TeamID:        dbTemplate.TeamID,
	}
}

func buildFromDB(dbBuild *db.Build) *BuildRecord {
	var logEntries []BuildLogEntry
	for _, entry := range dbBuild.LogEntries {
		logEntries = append(logEntries, BuildLogEntry{
			Timestamp: entry.Timestamp,
			Message:   entry.Message,
			Level:     LogLevel(entry.Level),
			Step:      entry.Step,
		})
	}

	return &BuildRecord{
		BuildID:     dbBuild.BuildID,
		TemplateID:  dbBuild.TemplateID,
		Status:      TemplateBuildStatus(dbBuild.Status),
		CreatedAt:   dbBuild.CreatedAt,
		UpdatedAt:   dbBuild.UpdatedAt,
		FinishedAt:  dbBuild.FinishedAt,
		CPUCount:    dbBuild.CPUCount,
		MemoryMB:    dbBuild.MemoryMB,
		DiskSizeMB:  dbBuild.DiskSizeMB,
		EnvdVersion: dbBuild.EnvdVersion,
		Logs:        dbBuild.Logs,
		LogEntries:  logEntries,
	}
}

func nodeFromDB(dbNode *db.Node) *NodeRecord {
	var metrics NodeMetrics
	if dbNode.Metrics != nil {
		var disks []DiskMetrics
		for _, d := range dbNode.Metrics.Disks {
			disks = append(disks, DiskMetrics{
				MountPoint:     d.MountPoint,
				Device:         d.Device,
				FilesystemType: d.FilesystemType,
				UsedBytes:      d.UsedBytes,
				TotalBytes:     d.TotalBytes,
			})
		}
		metrics = NodeMetrics{
			AllocatedCPU:         dbNode.Metrics.AllocatedCPU,
			CPUPercent:           dbNode.Metrics.CPUPercent,
			CPUCount:             dbNode.Metrics.CPUCount,
			AllocatedMemoryBytes: dbNode.Metrics.AllocatedMemoryBytes,
			MemoryUsedBytes:      dbNode.Metrics.MemoryUsedBytes,
			MemoryTotalBytes:     dbNode.Metrics.MemoryTotalBytes,
			Disks:                disks,
		}
	}

	return &NodeRecord{
		Node: Node{
			NodeID:               dbNode.NodeID,
			ID:                   dbNode.ID,
			Version:              dbNode.Version,
			Commit:               dbNode.Commit,
			ServiceInstanceID:    dbNode.ServiceInstanceID,
			ClusterID:            dbNode.ClusterID,
			Status:               NodeStatus(dbNode.Status),
			SandboxCount:         uint32(dbNode.SandboxCount),
			SandboxStartingCount: dbNode.SandboxStartingCount,
			CreateSuccesses:      uint64(dbNode.CreateSuccesses),
			CreateFails:          uint64(dbNode.CreateFails),
			Metrics:              metrics,
		},
		CachedBuilds: dbNode.CachedBuilds,
	}
}

// ============================================================================
// Helper Functions
// ============================================================================

func generateSandboxID() string {
	// Generate a short, URL-safe ID similar to E2B's format
	id := uuid.New().String()
	return id[:8] // Use first 8 chars for brevity
}

func generateTemplateID() string {
	id := uuid.New().String()
	return id[:8]
}

func generateToken() string {
	return uuid.New().String()
}
