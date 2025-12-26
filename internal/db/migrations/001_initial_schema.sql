-- E2B Server Database Schema
-- Initial migration: Create all core tables

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Teams table
CREATE TABLE IF NOT EXISTS teams (
    team_id TEXT PRIMARY KEY DEFAULT uuid_generate_v4()::text,
    name TEXT NOT NULL,
    api_key TEXT NOT NULL,
    is_default BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Create index on api_key for lookups
CREATE INDEX IF NOT EXISTS idx_teams_api_key ON teams(api_key);

-- Team users table
CREATE TABLE IF NOT EXISTS team_users (
    id TEXT PRIMARY KEY DEFAULT uuid_generate_v4()::text,
    team_id TEXT NOT NULL REFERENCES teams(team_id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(team_id, email)
);

-- Templates table
CREATE TABLE IF NOT EXISTS templates (
    template_id TEXT PRIMARY KEY,
    build_id TEXT NOT NULL,
    cpu_count INTEGER NOT NULL DEFAULT 1,
    memory_mb INTEGER NOT NULL DEFAULT 512,
    disk_size_mb INTEGER NOT NULL DEFAULT 512,
    public BOOLEAN DEFAULT FALSE,
    aliases TEXT[] DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    created_by_id TEXT REFERENCES team_users(id),
    last_spawned_at TIMESTAMPTZ,
    spawn_count BIGINT DEFAULT 0,
    build_count INTEGER DEFAULT 1,
    envd_version TEXT DEFAULT '0.1.0',
    build_status TEXT DEFAULT 'waiting' CHECK (build_status IN ('building', 'waiting', 'ready', 'error')),
    team_id TEXT REFERENCES teams(team_id) ON DELETE SET NULL
);

-- Create indexes for common queries
CREATE INDEX IF NOT EXISTS idx_templates_team_id ON templates(team_id);
CREATE INDEX IF NOT EXISTS idx_templates_aliases ON templates USING GIN(aliases);

-- Builds table
CREATE TABLE IF NOT EXISTS builds (
    build_id TEXT PRIMARY KEY,
    template_id TEXT NOT NULL REFERENCES templates(template_id) ON DELETE CASCADE,
    status TEXT DEFAULT 'waiting' CHECK (status IN ('building', 'waiting', 'ready', 'error')),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    cpu_count INTEGER NOT NULL DEFAULT 1,
    memory_mb INTEGER NOT NULL DEFAULT 512,
    disk_size_mb INTEGER,
    envd_version TEXT,
    logs TEXT[] DEFAULT '{}',
    log_entries JSONB DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_builds_template_id ON builds(template_id);

-- Sandboxes table
CREATE TABLE IF NOT EXISTS sandboxes (
    sandbox_id TEXT PRIMARY KEY,
    template_id TEXT NOT NULL REFERENCES templates(template_id),
    alias TEXT,
    client_id TEXT NOT NULL,
    envd_version TEXT NOT NULL,
    envd_access_token TEXT NOT NULL,
    domain TEXT NOT NULL,
    cpu_count INTEGER NOT NULL,
    memory_mb INTEGER NOT NULL,
    disk_size_mb INTEGER NOT NULL,
    metadata JSONB DEFAULT '{}',
    env_vars JSONB DEFAULT '{}',
    state TEXT DEFAULT 'running' CHECK (state IN ('running', 'paused')),
    started_at TIMESTAMPTZ DEFAULT NOW(),
    end_at TIMESTAMPTZ NOT NULL,
    auto_pause BOOLEAN DEFAULT FALSE,
    node_id TEXT,
    team_id TEXT REFERENCES teams(team_id) ON DELETE SET NULL
);

-- Create indexes for common queries
CREATE INDEX IF NOT EXISTS idx_sandboxes_state ON sandboxes(state);
CREATE INDEX IF NOT EXISTS idx_sandboxes_template_id ON sandboxes(template_id);
CREATE INDEX IF NOT EXISTS idx_sandboxes_team_id ON sandboxes(team_id);
CREATE INDEX IF NOT EXISTS idx_sandboxes_node_id ON sandboxes(node_id);
CREATE INDEX IF NOT EXISTS idx_sandboxes_end_at ON sandboxes(end_at);

-- Nodes table
CREATE TABLE IF NOT EXISTS nodes (
    node_id TEXT PRIMARY KEY,
    id TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '0.1.0',
    commit TEXT DEFAULT '',
    service_instance_id TEXT NOT NULL,
    cluster_id TEXT NOT NULL,
    status TEXT DEFAULT 'ready' CHECK (status IN ('ready', 'draining', 'connecting', 'unhealthy')),
    sandbox_count INTEGER DEFAULT 0,
    sandbox_starting_count INTEGER DEFAULT 0,
    create_successes BIGINT DEFAULT 0,
    create_fails BIGINT DEFAULT 0,
    metrics JSONB DEFAULT '{}',
    cached_builds TEXT[] DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
CREATE INDEX IF NOT EXISTS idx_nodes_cluster_id ON nodes(cluster_id);

-- API Keys table
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY DEFAULT uuid_generate_v4()::text,
    name TEXT NOT NULL,
    hashed_key TEXT NOT NULL,
    team_id TEXT NOT NULL REFERENCES teams(team_id) ON DELETE CASCADE,
    mask_prefix TEXT DEFAULT 'e2b_',
    mask_value_length INTEGER DEFAULT 32,
    masked_value_prefix TEXT DEFAULT '',
    masked_value_suffix TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by_id TEXT REFERENCES team_users(id),
    last_used TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_team_id ON api_keys(team_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_hashed_key ON api_keys(hashed_key);

-- Access Tokens table
CREATE TABLE IF NOT EXISTS access_tokens (
    id TEXT PRIMARY KEY DEFAULT uuid_generate_v4()::text,
    name TEXT NOT NULL,
    hashed_token TEXT NOT NULL,
    team_id TEXT NOT NULL REFERENCES teams(team_id) ON DELETE CASCADE,
    mask_prefix TEXT DEFAULT '',
    mask_value_length INTEGER DEFAULT 32,
    masked_value_prefix TEXT DEFAULT '',
    masked_value_suffix TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_access_tokens_team_id ON access_tokens(team_id);
CREATE INDEX IF NOT EXISTS idx_access_tokens_hashed_token ON access_tokens(hashed_token);

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Triggers for auto-updating updated_at
CREATE TRIGGER update_teams_updated_at BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_templates_updated_at BEFORE UPDATE ON templates
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_builds_updated_at BEFORE UPDATE ON builds
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_nodes_updated_at BEFORE UPDATE ON nodes
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Insert default team
INSERT INTO teams (team_id, name, api_key, is_default)
VALUES ('default', 'Default Team', 'e2b_default_key', TRUE)
ON CONFLICT (team_id) DO NOTHING;
