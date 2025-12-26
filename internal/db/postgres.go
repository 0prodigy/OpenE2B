package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements Store using PostgreSQL/Supabase
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgreSQL store
func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresStore{pool: pool}, nil
}

// Ping checks database connectivity
func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close closes the database connection pool
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// ============================================================================
// Sandbox Operations
// ============================================================================

func (s *PostgresStore) CreateSandbox(ctx context.Context, sandbox *Sandbox) error {
	metadata, err := json.Marshal(sandbox.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	envVars, err := json.Marshal(sandbox.EnvVars)
	if err != nil {
		return fmt.Errorf("failed to marshal env vars: %w", err)
	}

	query := `
		INSERT INTO sandboxes (
			sandbox_id, template_id, alias, client_id, envd_version, envd_access_token,
			domain, cpu_count, memory_mb, disk_size_mb, metadata, env_vars,
			state, started_at, end_at, auto_pause, node_id, team_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`

	_, err = s.pool.Exec(ctx, query,
		sandbox.SandboxID, sandbox.TemplateID, sandbox.Alias, sandbox.ClientID,
		sandbox.EnvdVersion, sandbox.EnvdAccessToken, sandbox.Domain,
		sandbox.CPUCount, sandbox.MemoryMB, sandbox.DiskSizeMB,
		metadata, envVars, sandbox.State, sandbox.StartedAt, sandbox.EndAt,
		sandbox.AutoPause, nullIfEmpty(sandbox.NodeID), nullIfEmpty(sandbox.TeamID),
	)
	if err != nil {
		return fmt.Errorf("failed to create sandbox: %w", err)
	}

	return nil
}

func (s *PostgresStore) GetSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	query := `
		SELECT sandbox_id, template_id, alias, client_id, envd_version, envd_access_token,
			domain, cpu_count, memory_mb, disk_size_mb, metadata, env_vars,
			state, started_at, end_at, auto_pause, node_id, team_id
		FROM sandboxes
		WHERE sandbox_id = $1
	`

	var sandbox Sandbox
	var metadata, envVars []byte
	var nodeID, teamID sql.NullString

	err := s.pool.QueryRow(ctx, query, sandboxID).Scan(
		&sandbox.SandboxID, &sandbox.TemplateID, &sandbox.Alias, &sandbox.ClientID,
		&sandbox.EnvdVersion, &sandbox.EnvdAccessToken, &sandbox.Domain,
		&sandbox.CPUCount, &sandbox.MemoryMB, &sandbox.DiskSizeMB,
		&metadata, &envVars, &sandbox.State, &sandbox.StartedAt, &sandbox.EndAt,
		&sandbox.AutoPause, &nodeID, &teamID,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if err := json.Unmarshal(metadata, &sandbox.Metadata); err != nil {
		sandbox.Metadata = make(map[string]string)
	}
	if err := json.Unmarshal(envVars, &sandbox.EnvVars); err != nil {
		sandbox.EnvVars = make(map[string]string)
	}

	sandbox.NodeID = nodeID.String
	sandbox.TeamID = teamID.String

	return &sandbox, nil
}

func (s *PostgresStore) ListSandboxes(ctx context.Context, filter *SandboxFilter) ([]*Sandbox, error) {
	query := `
		SELECT sandbox_id, template_id, alias, client_id, envd_version, envd_access_token,
			domain, cpu_count, memory_mb, disk_size_mb, metadata, env_vars,
			state, started_at, end_at, auto_pause, node_id, team_id
		FROM sandboxes
		WHERE 1=1
	`
	args := []any{}
	argNum := 1

	if filter != nil {
		if len(filter.States) > 0 {
			query += fmt.Sprintf(" AND state = ANY($%d)", argNum)
			args = append(args, filter.States)
			argNum++
		}
		if filter.TeamID != nil {
			query += fmt.Sprintf(" AND team_id = $%d", argNum)
			args = append(args, *filter.TeamID)
			argNum++
		}
		if filter.NodeID != nil {
			query += fmt.Sprintf(" AND node_id = $%d", argNum)
			args = append(args, *filter.NodeID)
			argNum++
		}
	}

	query += " ORDER BY started_at DESC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}
	defer rows.Close()

	var sandboxes []*Sandbox
	for rows.Next() {
		var sandbox Sandbox
		var metadata, envVars []byte
		var nodeID, teamID sql.NullString

		err := rows.Scan(
			&sandbox.SandboxID, &sandbox.TemplateID, &sandbox.Alias, &sandbox.ClientID,
			&sandbox.EnvdVersion, &sandbox.EnvdAccessToken, &sandbox.Domain,
			&sandbox.CPUCount, &sandbox.MemoryMB, &sandbox.DiskSizeMB,
			&metadata, &envVars, &sandbox.State, &sandbox.StartedAt, &sandbox.EndAt,
			&sandbox.AutoPause, &nodeID, &teamID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan sandbox: %w", err)
		}

		if err := json.Unmarshal(metadata, &sandbox.Metadata); err != nil {
			sandbox.Metadata = make(map[string]string)
		}
		if err := json.Unmarshal(envVars, &sandbox.EnvVars); err != nil {
			sandbox.EnvVars = make(map[string]string)
		}

		sandbox.NodeID = nodeID.String
		sandbox.TeamID = teamID.String

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

		sandboxes = append(sandboxes, &sandbox)
	}

	return sandboxes, nil
}

func (s *PostgresStore) UpdateSandbox(ctx context.Context, sandboxID string, updates *SandboxUpdate) error {
	if updates == nil {
		return nil
	}

	query := "UPDATE sandboxes SET "
	args := []any{}
	argNum := 1
	setClauses := []string{}

	if updates.State != nil {
		setClauses = append(setClauses, fmt.Sprintf("state = $%d", argNum))
		args = append(args, *updates.State)
		argNum++
	}
	if updates.EndAt != nil {
		setClauses = append(setClauses, fmt.Sprintf("end_at = $%d", argNum))
		args = append(args, *updates.EndAt)
		argNum++
	}
	if updates.NodeID != nil {
		setClauses = append(setClauses, fmt.Sprintf("node_id = $%d", argNum))
		args = append(args, *updates.NodeID)
		argNum++
	}

	if len(setClauses) == 0 {
		return nil
	}

	for i, clause := range setClauses {
		if i > 0 {
			query += ", "
		}
		query += clause
	}

	query += fmt.Sprintf(" WHERE sandbox_id = $%d", argNum)
	args = append(args, sandboxID)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update sandbox: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sandbox not found")
	}

	return nil
}

func (s *PostgresStore) DeleteSandbox(ctx context.Context, sandboxID string) error {
	query := "DELETE FROM sandboxes WHERE sandbox_id = $1"
	result, err := s.pool.Exec(ctx, query, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sandbox not found")
	}

	return nil
}

// ============================================================================
// Template Operations
// ============================================================================

func (s *PostgresStore) CreateTemplate(ctx context.Context, template *Template, build *Build) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Insert template
	templateQuery := `
		INSERT INTO templates (
			template_id, build_id, cpu_count, memory_mb, disk_size_mb, public,
			aliases, created_at, updated_at, created_by_id, last_spawned_at,
			spawn_count, build_count, envd_version, build_status, team_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`

	_, err = tx.Exec(ctx, templateQuery,
		template.TemplateID, template.BuildID, template.CPUCount, template.MemoryMB,
		template.DiskSizeMB, template.Public, template.Aliases,
		template.CreatedAt, template.UpdatedAt, template.CreatedByID,
		template.LastSpawnedAt, template.SpawnCount, template.BuildCount,
		template.EnvdVersion, template.BuildStatus, nullIfEmpty(template.TeamID),
	)
	if err != nil {
		return fmt.Errorf("failed to create template: %w", err)
	}

	// Insert build
	if build != nil {
		logEntries, err := json.Marshal(build.LogEntries)
		if err != nil {
			return fmt.Errorf("failed to marshal log entries: %w", err)
		}

		buildQuery := `
			INSERT INTO builds (
				build_id, template_id, status, created_at, updated_at, finished_at,
				cpu_count, memory_mb, disk_size_mb, envd_version, logs, log_entries
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`

		_, err = tx.Exec(ctx, buildQuery,
			build.BuildID, build.TemplateID, build.Status, build.CreatedAt,
			build.UpdatedAt, build.FinishedAt, build.CPUCount, build.MemoryMB,
			build.DiskSizeMB, build.EnvdVersion, build.Logs, logEntries,
		)
		if err != nil {
			return fmt.Errorf("failed to create build: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (s *PostgresStore) GetTemplate(ctx context.Context, idOrAlias string) (*Template, error) {
	query := `
		SELECT template_id, build_id, cpu_count, memory_mb, disk_size_mb, public,
			aliases, created_at, updated_at, created_by_id, last_spawned_at,
			spawn_count, build_count, envd_version, build_status, team_id
		FROM templates
		WHERE template_id = $1 OR $1 = ANY(aliases)
	`

	var template Template
	var teamID sql.NullString

	err := s.pool.QueryRow(ctx, query, idOrAlias).Scan(
		&template.TemplateID, &template.BuildID, &template.CPUCount, &template.MemoryMB,
		&template.DiskSizeMB, &template.Public, &template.Aliases,
		&template.CreatedAt, &template.UpdatedAt, &template.CreatedByID,
		&template.LastSpawnedAt, &template.SpawnCount, &template.BuildCount,
		&template.EnvdVersion, &template.BuildStatus, &teamID,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	template.TeamID = teamID.String

	return &template, nil
}

func (s *PostgresStore) ListTemplates(ctx context.Context, teamID *string) ([]*Template, error) {
	query := `
		SELECT template_id, build_id, cpu_count, memory_mb, disk_size_mb, public,
			aliases, created_at, updated_at, created_by_id, last_spawned_at,
			spawn_count, build_count, envd_version, build_status, team_id
		FROM templates
	`
	args := []any{}

	if teamID != nil {
		query += " WHERE team_id = $1"
		args = append(args, *teamID)
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	defer rows.Close()

	var templates []*Template
	for rows.Next() {
		var template Template
		var tid sql.NullString

		err := rows.Scan(
			&template.TemplateID, &template.BuildID, &template.CPUCount, &template.MemoryMB,
			&template.DiskSizeMB, &template.Public, &template.Aliases,
			&template.CreatedAt, &template.UpdatedAt, &template.CreatedByID,
			&template.LastSpawnedAt, &template.SpawnCount, &template.BuildCount,
			&template.EnvdVersion, &template.BuildStatus, &tid,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan template: %w", err)
		}

		template.TeamID = tid.String
		templates = append(templates, &template)
	}

	return templates, nil
}

func (s *PostgresStore) UpdateTemplate(ctx context.Context, templateID string, updates *TemplateUpdate) error {
	if updates == nil {
		return nil
	}

	query := "UPDATE templates SET updated_at = NOW()"
	args := []any{}
	argNum := 1

	if updates.Public != nil {
		query += fmt.Sprintf(", public = $%d", argNum)
		args = append(args, *updates.Public)
		argNum++
	}
	if updates.BuildStatus != nil {
		query += fmt.Sprintf(", build_status = $%d", argNum)
		args = append(args, *updates.BuildStatus)
		argNum++
	}
	if updates.BuildID != nil {
		query += fmt.Sprintf(", build_id = $%d", argNum)
		args = append(args, *updates.BuildID)
		argNum++
	}

	query += fmt.Sprintf(" WHERE template_id = $%d", argNum)
	args = append(args, templateID)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update template: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("template not found")
	}

	return nil
}

func (s *PostgresStore) DeleteTemplate(ctx context.Context, templateID string) error {
	query := "DELETE FROM templates WHERE template_id = $1"
	result, err := s.pool.Exec(ctx, query, templateID)
	if err != nil {
		return fmt.Errorf("failed to delete template: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("template not found")
	}

	return nil
}

func (s *PostgresStore) IncrementTemplateSpawnCount(ctx context.Context, templateID string) error {
	query := `
		UPDATE templates
		SET spawn_count = spawn_count + 1, last_spawned_at = NOW()
		WHERE template_id = $1
	`
	_, err := s.pool.Exec(ctx, query, templateID)
	if err != nil {
		return fmt.Errorf("failed to increment spawn count: %w", err)
	}
	return nil
}

// ============================================================================
// Build Operations
// ============================================================================

func (s *PostgresStore) CreateBuild(ctx context.Context, build *Build) error {
	logEntriesJSON, err := json.Marshal(build.LogEntries)
	if err != nil {
		logEntriesJSON = []byte("[]")
	}

	query := `
		INSERT INTO builds (build_id, template_id, status, created_at, updated_at, finished_at,
			cpu_count, memory_mb, disk_size_mb, envd_version, logs, log_entries)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = s.pool.Exec(ctx, query,
		build.BuildID, build.TemplateID, build.Status, build.CreatedAt,
		build.UpdatedAt, build.FinishedAt, build.CPUCount, build.MemoryMB,
		build.DiskSizeMB, build.EnvdVersion, build.Logs, logEntriesJSON,
	)
	if err != nil {
		return fmt.Errorf("failed to create build: %w", err)
	}

	// Increment template build count
	updateQuery := `UPDATE templates SET build_count = build_count + 1, updated_at = NOW() WHERE template_id = $1`
	_, _ = s.pool.Exec(ctx, updateQuery, build.TemplateID)

	return nil
}

func (s *PostgresStore) GetBuild(ctx context.Context, buildID string) (*Build, error) {
	query := `
		SELECT build_id, template_id, status, created_at, updated_at, finished_at,
			cpu_count, memory_mb, disk_size_mb, envd_version, logs, log_entries
		FROM builds
		WHERE build_id = $1
	`

	var build Build
	var logEntries []byte

	err := s.pool.QueryRow(ctx, query, buildID).Scan(
		&build.BuildID, &build.TemplateID, &build.Status, &build.CreatedAt,
		&build.UpdatedAt, &build.FinishedAt, &build.CPUCount, &build.MemoryMB,
		&build.DiskSizeMB, &build.EnvdVersion, &build.Logs, &logEntries,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get build: %w", err)
	}

	if err := json.Unmarshal(logEntries, &build.LogEntries); err != nil {
		build.LogEntries = []LogEntry{}
	}

	return &build, nil
}

func (s *PostgresStore) ListBuildsForTemplate(ctx context.Context, templateID string) ([]*Build, error) {
	query := `
		SELECT build_id, template_id, status, created_at, updated_at, finished_at,
			cpu_count, memory_mb, disk_size_mb, envd_version, logs, log_entries
		FROM builds
		WHERE template_id = $1
		ORDER BY created_at DESC
	`

	rows, err := s.pool.Query(ctx, query, templateID)
	if err != nil {
		return nil, fmt.Errorf("failed to list builds: %w", err)
	}
	defer rows.Close()

	var builds []*Build
	for rows.Next() {
		var build Build
		var logEntries []byte

		err := rows.Scan(
			&build.BuildID, &build.TemplateID, &build.Status, &build.CreatedAt,
			&build.UpdatedAt, &build.FinishedAt, &build.CPUCount, &build.MemoryMB,
			&build.DiskSizeMB, &build.EnvdVersion, &build.Logs, &logEntries,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan build: %w", err)
		}

		if err := json.Unmarshal(logEntries, &build.LogEntries); err != nil {
			build.LogEntries = []LogEntry{}
		}

		builds = append(builds, &build)
	}

	return builds, nil
}

func (s *PostgresStore) UpdateBuild(ctx context.Context, buildID string, updates *BuildUpdate) error {
	if updates == nil {
		return nil
	}

	query := "UPDATE builds SET updated_at = NOW()"
	args := []any{}
	argNum := 1

	if updates.Status != nil {
		query += fmt.Sprintf(", status = $%d", argNum)
		args = append(args, *updates.Status)
		argNum++
	}
	if updates.FinishedAt != nil {
		query += fmt.Sprintf(", finished_at = $%d", argNum)
		args = append(args, *updates.FinishedAt)
		argNum++
	}
	if updates.Logs != nil {
		query += fmt.Sprintf(", logs = $%d", argNum)
		args = append(args, updates.Logs)
		argNum++
	}
	if updates.LogEntries != nil {
		logEntries, _ := json.Marshal(updates.LogEntries)
		query += fmt.Sprintf(", log_entries = $%d", argNum)
		args = append(args, logEntries)
		argNum++
	}

	query += fmt.Sprintf(" WHERE build_id = $%d", argNum)
	args = append(args, buildID)

	_, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update build: %w", err)
	}

	return nil
}

// ============================================================================
// Node Operations
// ============================================================================

func (s *PostgresStore) CreateNode(ctx context.Context, node *Node) error {
	metrics, err := json.Marshal(node.Metrics)
	if err != nil {
		metrics = []byte("{}")
	}

	query := `
		INSERT INTO nodes (
			node_id, id, version, commit, service_instance_id, cluster_id,
			status, sandbox_count, sandbox_starting_count, create_successes,
			create_fails, metrics, cached_builds
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err = s.pool.Exec(ctx, query,
		node.NodeID, node.ID, node.Version, node.Commit, node.ServiceInstanceID,
		node.ClusterID, node.Status, node.SandboxCount, node.SandboxStartingCount,
		node.CreateSuccesses, node.CreateFails, metrics, node.CachedBuilds,
	)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	return nil
}

func (s *PostgresStore) GetNode(ctx context.Context, nodeID string) (*Node, error) {
	query := `
		SELECT node_id, id, version, commit, service_instance_id, cluster_id,
			status, sandbox_count, sandbox_starting_count, create_successes,
			create_fails, metrics, cached_builds, created_at, updated_at
		FROM nodes
		WHERE node_id = $1
	`

	var node Node
	var metricsJSON []byte

	err := s.pool.QueryRow(ctx, query, nodeID).Scan(
		&node.NodeID, &node.ID, &node.Version, &node.Commit, &node.ServiceInstanceID,
		&node.ClusterID, &node.Status, &node.SandboxCount, &node.SandboxStartingCount,
		&node.CreateSuccesses, &node.CreateFails, &metricsJSON, &node.CachedBuilds,
		&node.CreatedAt, &node.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	if len(metricsJSON) > 0 {
		var metrics NodeMetrics
		if err := json.Unmarshal(metricsJSON, &metrics); err == nil {
			node.Metrics = &metrics
		}
	}

	return &node, nil
}

func (s *PostgresStore) ListNodes(ctx context.Context) ([]*Node, error) {
	query := `
		SELECT node_id, id, version, commit, service_instance_id, cluster_id,
			status, sandbox_count, sandbox_starting_count, create_successes,
			create_fails, metrics, cached_builds, created_at, updated_at
		FROM nodes
		ORDER BY created_at DESC
	`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*Node
	for rows.Next() {
		var node Node
		var metricsJSON []byte

		err := rows.Scan(
			&node.NodeID, &node.ID, &node.Version, &node.Commit, &node.ServiceInstanceID,
			&node.ClusterID, &node.Status, &node.SandboxCount, &node.SandboxStartingCount,
			&node.CreateSuccesses, &node.CreateFails, &metricsJSON, &node.CachedBuilds,
			&node.CreatedAt, &node.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}

		if len(metricsJSON) > 0 {
			var metrics NodeMetrics
			if err := json.Unmarshal(metricsJSON, &metrics); err == nil {
				node.Metrics = &metrics
			}
		}

		nodes = append(nodes, &node)
	}

	return nodes, nil
}

func (s *PostgresStore) UpdateNodeStatus(ctx context.Context, nodeID string, status string) error {
	query := "UPDATE nodes SET status = $1, updated_at = NOW() WHERE node_id = $2"
	result, err := s.pool.Exec(ctx, query, status, nodeID)
	if err != nil {
		return fmt.Errorf("failed to update node status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("node not found")
	}

	return nil
}

func (s *PostgresStore) UpdateNodeMetrics(ctx context.Context, nodeID string, metrics *NodeMetrics) error {
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics: %w", err)
	}

	query := "UPDATE nodes SET metrics = $1, updated_at = NOW() WHERE node_id = $2"
	_, err = s.pool.Exec(ctx, query, metricsJSON, nodeID)
	if err != nil {
		return fmt.Errorf("failed to update node metrics: %w", err)
	}

	return nil
}

// ============================================================================
// Team Operations
// ============================================================================

func (s *PostgresStore) GetTeam(ctx context.Context, teamID string) (*Team, error) {
	query := `
		SELECT team_id, name, api_key, is_default, created_at, updated_at
		FROM teams
		WHERE team_id = $1
	`

	var team Team
	err := s.pool.QueryRow(ctx, query, teamID).Scan(
		&team.TeamID, &team.Name, &team.APIKey, &team.IsDefault,
		&team.CreatedAt, &team.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get team: %w", err)
	}

	return &team, nil
}

func (s *PostgresStore) ListTeams(ctx context.Context) ([]*Team, error) {
	query := `
		SELECT team_id, name, api_key, is_default, created_at, updated_at
		FROM teams
		ORDER BY created_at DESC
	`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	defer rows.Close()

	var teams []*Team
	for rows.Next() {
		var team Team
		err := rows.Scan(
			&team.TeamID, &team.Name, &team.APIKey, &team.IsDefault,
			&team.CreatedAt, &team.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan team: %w", err)
		}
		teams = append(teams, &team)
	}

	return teams, nil
}

func (s *PostgresStore) CreateTeam(ctx context.Context, team *Team) error {
	query := `
		INSERT INTO teams (team_id, name, api_key, is_default, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	_, err := s.pool.Exec(ctx, query,
		team.TeamID, team.Name, team.APIKey, team.IsDefault,
		team.CreatedAt, team.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create team: %w", err)
	}

	return nil
}

// ============================================================================
// API Key Operations
// ============================================================================

func (s *PostgresStore) CreateAPIKey(ctx context.Context, apiKey *APIKey) error {
	query := `
		INSERT INTO api_keys (
			id, name, hashed_key, team_id, mask_prefix, mask_value_length,
			masked_value_prefix, masked_value_suffix, created_at, created_by_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := s.pool.Exec(ctx, query,
		apiKey.ID, apiKey.Name, apiKey.HashedKey, apiKey.TeamID,
		apiKey.MaskPrefix, apiKey.MaskValueLength, apiKey.MaskedValuePrefix,
		apiKey.MaskedValueSuffix, apiKey.CreatedAt, apiKey.CreatedByID,
	)
	if err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}

	return nil
}

func (s *PostgresStore) GetAPIKeyByHash(ctx context.Context, hashedKey string) (*APIKey, error) {
	query := `
		SELECT id, name, hashed_key, team_id, mask_prefix, mask_value_length,
			masked_value_prefix, masked_value_suffix, created_at, created_by_id, last_used
		FROM api_keys
		WHERE hashed_key = $1
	`

	var apiKey APIKey
	err := s.pool.QueryRow(ctx, query, hashedKey).Scan(
		&apiKey.ID, &apiKey.Name, &apiKey.HashedKey, &apiKey.TeamID,
		&apiKey.MaskPrefix, &apiKey.MaskValueLength, &apiKey.MaskedValuePrefix,
		&apiKey.MaskedValueSuffix, &apiKey.CreatedAt, &apiKey.CreatedByID, &apiKey.LastUsed,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get API key: %w", err)
	}

	return &apiKey, nil
}

func (s *PostgresStore) ListAPIKeys(ctx context.Context, teamID string) ([]*APIKey, error) {
	query := `
		SELECT id, name, hashed_key, team_id, mask_prefix, mask_value_length,
			masked_value_prefix, masked_value_suffix, created_at, created_by_id, last_used
		FROM api_keys
		WHERE team_id = $1
		ORDER BY created_at DESC
	`

	rows, err := s.pool.Query(ctx, query, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}
	defer rows.Close()

	var apiKeys []*APIKey
	for rows.Next() {
		var apiKey APIKey
		err := rows.Scan(
			&apiKey.ID, &apiKey.Name, &apiKey.HashedKey, &apiKey.TeamID,
			&apiKey.MaskPrefix, &apiKey.MaskValueLength, &apiKey.MaskedValuePrefix,
			&apiKey.MaskedValueSuffix, &apiKey.CreatedAt, &apiKey.CreatedByID, &apiKey.LastUsed,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		apiKeys = append(apiKeys, &apiKey)
	}

	return apiKeys, nil
}

func (s *PostgresStore) DeleteAPIKey(ctx context.Context, keyID string) error {
	query := "DELETE FROM api_keys WHERE id = $1"
	result, err := s.pool.Exec(ctx, query, keyID)
	if err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("API key not found")
	}

	return nil
}

func (s *PostgresStore) UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error {
	query := "UPDATE api_keys SET last_used = NOW() WHERE id = $1"
	_, err := s.pool.Exec(ctx, query, keyID)
	if err != nil {
		return fmt.Errorf("failed to update API key last used: %w", err)
	}
	return nil
}

// ============================================================================
// Helper Functions
// ============================================================================

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
