-- Loopany Schema
-- PostgreSQL database schema

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================
-- Users (owner of machines and loops)
-- ============================================================

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Device Tokens (authentication)
-- ============================================================

CREATE TABLE device_tokens (
    token_hash TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    machine_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_device_tokens_owner ON device_tokens(owner_id);
CREATE INDEX idx_device_tokens_machine ON device_tokens(machine_id);

-- ============================================================
-- Machines
-- ============================================================

CREATE TABLE machines (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    host TEXT,
    platform TEXT,
    arch TEXT,
    version TEXT,
    last_seen TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_machines_owner ON machines(owner_id);
CREATE INDEX idx_machines_last_seen ON machines(last_seen);

-- ============================================================
-- Loops
-- ============================================================

CREATE TABLE loops (
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
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_loops_machine ON loops(machine_id);
CREATE INDEX idx_loops_enabled ON loops(enabled);

-- ============================================================
-- Runs
-- ============================================================

CREATE TABLE runs (
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

CREATE INDEX idx_runs_loop ON runs(loop_id);
CREATE INDEX idx_runs_machine ON runs(machine_id);
CREATE INDEX idx_runs_status ON runs(status);
CREATE INDEX idx_runs_created ON runs(created_at DESC);

-- ============================================================
-- Run Tokens (temporary auth for callbacks)
-- ============================================================

CREATE TABLE run_tokens (
    token_hash TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

CREATE INDEX idx_run_tokens_run ON run_tokens(run_id);

-- ============================================================
-- Blobs (content-addressed storage)
-- ============================================================

CREATE TABLE blobs (
    hash TEXT PRIMARY KEY,
    size BIGINT NOT NULL,
    content_type TEXT,
    stored_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Artifacts (files created/edited in runs)
-- ============================================================

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id TEXT NOT NULL REFERENCES runs(id),
    path TEXT NOT NULL,
    kind TEXT NOT NULL, -- 'created' or 'edited'
    hash TEXT REFERENCES blobs(hash),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_artifacts_run ON artifacts(run_id);

-- ============================================================
-- Watch Manifests (for sync)
-- ============================================================

CREATE TABLE watch_manifests (
    loop_id TEXT PRIMARY KEY REFERENCES loops(id),
    digest TEXT NOT NULL,
    manifest JSONB NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- ============================================================
-- Functions
-- ============================================================

-- Update timestamp trigger
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_loops_updated
    BEFORE UPDATE ON loops
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ============================================================
-- Initial Data
-- ============================================================

-- Create a default admin user for local dev
INSERT INTO users (id, email, name)
VALUES ('admin', 'admin@localhost', 'Admin')
ON CONFLICT (id) DO NOTHING;