// Package store provides migration support.
package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Migrate runs database migrations.
func Migrate(db *sql.DB) error {
	// Read schema
	schema := `-- Loopany Schema
-- PostgreSQL database schema

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================
-- Users (owner of machines and loops)
-- ============================================================

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Device Tokens (authentication)
-- ============================================================

CREATE TABLE IF NOT EXISTS device_tokens (
    token_hash TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    machine_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_device_tokens_owner ON device_tokens(owner_id);
CREATE INDEX IF NOT EXISTS idx_device_tokens_machine ON device_tokens(machine_id);

-- ============================================================
-- Machines
-- ============================================================

CREATE TABLE IF NOT EXISTS machines (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    host TEXT,
    platform TEXT,
    arch TEXT,
    version TEXT,
    last_seen TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_machines_owner ON machines(owner_id);
CREATE INDEX IF NOT EXISTS idx_machines_last_seen ON machines(last_seen);

-- ============================================================
-- Loops
-- ============================================================

CREATE TABLE IF NOT EXISTS loops (
    id TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL REFERENCES machines(id),
    name TEXT NOT NULL,
    cron TEXT,
    timezone TEXT DEFAULT 'UTC',
    workdir TEXT,
    task_file TEXT,
    workflow TEXT,
    model TEXT,
    agent TEXT DEFAULT 'claude-code',
    notify TEXT[],
    enabled BOOLEAN NOT NULL DEFAULT true,
    goal TEXT,
    allow_control BOOLEAN NOT NULL DEFAULT true,
    state JSONB,
    completed_at TIMESTAMP,
    completion_reason TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_loops_machine ON loops(machine_id);
CREATE INDEX IF NOT EXISTS idx_loops_enabled ON loops(enabled);

-- ============================================================
-- Runs
-- ============================================================

CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    loop_id TEXT NOT NULL REFERENCES loops(id),
    machine_id TEXT REFERENCES machines(id),
    role TEXT NOT NULL DEFAULT 'exec',
    status TEXT NOT NULL DEFAULT 'pending',
    outcome TEXT,
    message TEXT,
    error TEXT,
    session_id TEXT,
    duration_ms BIGINT,
    cost_usd FLOAT,
    cost_input_tokens BIGINT,
    cost_output_tokens BIGINT,
    cost_cache_read_tokens BIGINT,
    cost_cache_creation_tokens BIGINT,
    cost_num_turns INT,
    started_at TIMESTAMP NOT NULL,
    ended_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_runs_loop ON runs(loop_id);
CREATE INDEX IF NOT EXISTS idx_runs_machine ON runs(machine_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC);

-- ============================================================
-- Run Tokens (temporary auth for callbacks)
-- ============================================================

CREATE TABLE IF NOT EXISTS run_tokens (
    token_hash TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

CREATE INDEX IF NOT EXISTS idx_run_tokens_run ON run_tokens(run_id);

-- ============================================================
-- Blobs (content-addressed storage)
-- ============================================================

CREATE TABLE IF NOT EXISTS blobs (
    hash TEXT PRIMARY KEY,
    size BIGINT NOT NULL,
    content_type TEXT,
    stored_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Artifacts (files created/edited in runs)
-- ============================================================

CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id TEXT NOT NULL REFERENCES runs(id),
    path TEXT NOT NULL,
    kind TEXT NOT NULL,
    hash TEXT REFERENCES blobs(hash),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_artifacts_run ON artifacts(run_id);

-- ============================================================
-- Watch Manifests (for sync)
-- ============================================================

CREATE TABLE IF NOT EXISTS watch_manifests (
    loop_id TEXT PRIMARY KEY REFERENCES loops(id),
    digest TEXT NOT NULL,
    manifest JSONB NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Initial Data
-- ============================================================

INSERT INTO users (id, email, name)
VALUES ('admin', 'admin@localhost', 'Admin')
ON CONFLICT (id) DO NOTHING;`

	// Execute entire schema as one statement (PostgreSQL supports multiple statements)
	if _, err := db.Exec(schema); err != nil {
		// Try statement by statement for better error messages
		statements := strings.Split(schema, ";")
		for i, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				if !isAlreadyExists(err) {
					return fmt.Errorf("migration error at statement %d: %w", i, err)
				}
			}
		}
	}

	// Create trigger function separately (handles dollar quotes)
	triggerFunc := `
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`

	db.Exec(triggerFunc) // Ignore errors, function may already exist

	trigger := `
DROP TRIGGER IF EXISTS update_loops_updated ON loops;
CREATE TRIGGER update_loops_updated
    BEFORE UPDATE ON loops
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();`

	db.Exec(trigger) // Ignore errors

	return nil
}
func isAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), "already exists") ||
		strings.Contains(err.Error(), "duplicate")
}