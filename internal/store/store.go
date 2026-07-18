// Package store provides database persistence.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/yourorg/loopany-go/internal/protocol"
	"github.com/yourorg/loopany-go/pkg/token"
)

// Store implements database operations.
type Store struct {
	db *sql.DB
}

// DB returns the underlying database connection.
func (s *Store) DB() *sql.DB {
	return s.db
}

// New creates a new store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// ============================================================
// Machine Operations
// ============================================================

// Machine represents a registered daemon.
type Machine struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"ownerId"`
	Token     string    `json:"token"`
	Host      string    `json:"host"`
	Platform  string    `json:"platform"`
	Arch      string    `json:"arch"`
	Version   string    `json:"version"`
	LastSeen  time.Time `json:"lastSeen"`
	CreatedAt time.Time `json:"createdAt"`
}

// RegisterMachine creates or updates a machine.
func (s *Store) RegisterMachine(ctx context.Context, machineID, ownerID string, info protocol.MachineInfo) error {
	query := `
		INSERT INTO machines (id, owner_id, host, platform, arch, version, last_seen, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			host = EXCLUDED.host,
			platform = EXCLUDED.platform,
			arch = EXCLUDED.arch,
			version = EXCLUDED.version,
			last_seen = EXCLUDED.last_seen
	`
	now := time.Now()
	_, err := s.db.ExecContext(ctx, query,
		machineID, ownerID, info.Host, info.Platform, info.Arch, info.Version, now, now)
	return err
}

// GetMachine retrieves a machine by ID.
func (s *Store) GetMachine(ctx context.Context, machineID string) (*Machine, error) {
	query := `
		SELECT id, owner_id, host, platform, arch, version, last_seen, created_at
		FROM machines WHERE id = $1
	`
	m := &Machine{}
	err := s.db.QueryRowContext(ctx, query, machineID).Scan(
		&m.ID, &m.OwnerID, &m.Host, &m.Platform, &m.Arch, &m.Version, &m.LastSeen, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ListMachinesByOwner retrieves all machines for an owner.
func (s *Store) ListMachinesByOwner(ctx context.Context, ownerID string) ([]*Machine, error) {
	query := `
		SELECT id, owner_id, host, platform, arch, version, last_seen, created_at
		FROM machines WHERE owner_id = $1 ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var machines []*Machine
	for rows.Next() {
		m := &Machine{}
		if err := rows.Scan(
			&m.ID, &m.OwnerID, &m.Host, &m.Platform, &m.Arch, &m.Version, &m.LastSeen, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	return machines, nil
}

// GetDeviceOwner retrieves owner ID from device token.
func (s *Store) GetDeviceOwner(ctx context.Context, tokenHash string) (string, error) {
	query := `SELECT owner_id FROM device_tokens WHERE token_hash = $1`
	var ownerID string
	err := s.db.QueryRowContext(ctx, query, tokenHash).Scan(&ownerID)
	return ownerID, err
}

// ============================================================
// Loop Operations
// ============================================================

// Loop represents a scheduled agent loop.
type Loop struct {
	ID               string     `json:"id"`
	MachineID        string     `json:"machineId"`
	Name             string     `json:"name"`
	Task             string     `json:"task"`             // 任务描述：每次执行要做什么
	Cron             string     `json:"cron"`
	Timezone         string     `json:"timezone"`
	Workdir          string     `json:"workdir"`
	TaskFile         string     `json:"taskFile"`
	Workflow         string     `json:"workflow"`
	Model            string     `json:"model"`
	Agent            string     `json:"agent"`
	Notify           []string   `json:"notify"`
	Enabled          bool       `json:"enabled"`
	Goal             string     `json:"goal"`             // 完成条件：何时结束 Loop（可选）
	AllowControl     bool       `json:"allowControl"`
	State            any        `json:"state"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
	CompletionReason string     `json:"completionReason,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

const loopFields = "id, machine_id, name, task, cron, timezone, workdir, task_file, workflow, model, agent, notify, enabled, goal, allow_control, state, completed_at, completion_reason, created_at, updated_at"

// scanLoop is a helper to scan loop fields
func scanLoop(rows *sql.Rows) (*Loop, error) {
	loop := &Loop{}
	var stateJSON []byte
	var completedAt sql.NullTime
	var completionReason sql.NullString

	err := rows.Scan(
		&loop.ID, &loop.MachineID, &loop.Name, &loop.Task, &loop.Cron, &loop.Timezone,
		&loop.Workdir, &loop.TaskFile, &loop.Workflow, &loop.Model, &loop.Agent,
		pq.Array(&loop.Notify), &loop.Enabled, &loop.Goal, &loop.AllowControl,
		&stateJSON, &completedAt, &completionReason, &loop.CreatedAt, &loop.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(stateJSON) > 0 {
		json.Unmarshal(stateJSON, &loop.State)
	}
	if completionReason.Valid {
		loop.CompletionReason = completionReason.String
	}
	if completedAt.Valid {
		loop.CompletedAt = &completedAt.Time
	}

	return loop, nil
}

// CreateLoop creates a new loop.
func (s *Store) CreateLoop(ctx context.Context, loop *Loop) error {
	query := `
		INSERT INTO loops (` + loopFields + `)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`
	stateJSON, _ := json.Marshal(loop.State)
	var completedAt interface{}
	if loop.CompletedAt != nil {
		completedAt = *loop.CompletedAt
	}
	_, err := s.db.ExecContext(ctx, query,
		loop.ID, loop.MachineID, loop.Name, loop.Task, loop.Cron, loop.Timezone, loop.Workdir,
		loop.TaskFile, loop.Workflow, loop.Model, loop.Agent,
		pq.Array(loop.Notify), loop.Enabled, loop.Goal, loop.AllowControl,
		stateJSON, completedAt, loop.CompletionReason, loop.CreatedAt, loop.UpdatedAt,
	)
	return err
}

// GetLoop retrieves a loop by ID.
func (s *Store) GetLoop(ctx context.Context, loopID string) (*Loop, error) {
	query := `SELECT ` + loopFields + ` FROM loops WHERE id = $1`
	rows, err := s.db.QueryContext(ctx, query, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	return scanLoop(rows)
}

// UpdateLoop updates a loop.
func (s *Store) UpdateLoop(ctx context.Context, loopID string, updates map[string]interface{}) error {
	args := []interface{}{time.Now()}
	argNum := 2
	updateParts := []string{"updated_at = $1"}

	for key, value := range updates {
		switch key {
		case "name":
			updateParts = append(updateParts, fmt.Sprintf("name = $%d", argNum))
			args = append(args, value)
			argNum++
		case "task":
			updateParts = append(updateParts, fmt.Sprintf("task = $%d", argNum))
			args = append(args, value)
			argNum++
		case "cron":
			updateParts = append(updateParts, fmt.Sprintf("cron = $%d", argNum))
			args = append(args, value)
			argNum++
		case "timezone":
			updateParts = append(updateParts, fmt.Sprintf("timezone = $%d", argNum))
			args = append(args, value)
			argNum++
		case "workdir":
			updateParts = append(updateParts, fmt.Sprintf("workdir = $%d", argNum))
			args = append(args, value)
			argNum++
		case "task_file":
			updateParts = append(updateParts, fmt.Sprintf("task_file = $%d", argNum))
			args = append(args, value)
			argNum++
		case "enabled":
			updateParts = append(updateParts, fmt.Sprintf("enabled = $%d", argNum))
			args = append(args, value)
			argNum++
		case "goal":
			updateParts = append(updateParts, fmt.Sprintf("goal = $%d", argNum))
			args = append(args, value)
			argNum++
		case "state":
			stateJSON, _ := json.Marshal(value)
			updateParts = append(updateParts, fmt.Sprintf("state = $%d", argNum))
			args = append(args, stateJSON)
			argNum++
		case "workflow":
			updateParts = append(updateParts, fmt.Sprintf("workflow = $%d", argNum))
			args = append(args, value)
			argNum++
		case "model":
			updateParts = append(updateParts, fmt.Sprintf("model = $%d", argNum))
			args = append(args, value)
			argNum++
		case "agent":
			updateParts = append(updateParts, fmt.Sprintf("agent = $%d", argNum))
			args = append(args, value)
			argNum++
		case "allow_control":
			updateParts = append(updateParts, fmt.Sprintf("allow_control = $%d", argNum))
			args = append(args, value)
			argNum++
		case "completed_at":
			updateParts = append(updateParts, fmt.Sprintf("completed_at = $%d", argNum))
			args = append(args, value)
			argNum++
		case "completion_reason":
			updateParts = append(updateParts, fmt.Sprintf("completion_reason = $%d", argNum))
			args = append(args, value)
			argNum++
		}
	}

	args = append(args, loopID)
	query := fmt.Sprintf("UPDATE loops SET %s WHERE id = $%d",
		join(updateParts, ", "), argNum)

	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// ListLoopsByMachine retrieves all loops for a machine.
func (s *Store) ListLoopsByMachine(ctx context.Context, machineID string) ([]*Loop, error) {
	query := `SELECT ` + loopFields + ` FROM loops WHERE machine_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, machineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loops []*Loop
	for rows.Next() {
		loop, err := scanLoop(rows)
		if err != nil {
			return nil, err
		}
		loops = append(loops, loop)
	}
	return loops, nil
}

// ListEnabledLoops retrieves all enabled loops for scheduling.
func (s *Store) ListEnabledLoops(ctx context.Context) ([]*Loop, error) {
	query := `SELECT ` + loopFields + ` FROM loops WHERE enabled = true ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loops []*Loop
	for rows.Next() {
		loop, err := scanLoop(rows)
		if err != nil {
			return nil, err
		}
		loops = append(loops, loop)
	}
	return loops, nil
}

// FinishLoop marks a loop as completed.
func (s *Store) FinishLoop(ctx context.Context, loopID, reason string) error {
	loop, err := s.GetLoop(ctx, loopID)
	if err != nil {
		return fmt.Errorf("loop not found: %w", err)
	}

	if loop.Goal == "" {
		return fmt.Errorf("loop has no goal to finish")
	}

	if loop.CompletedAt != nil {
		return fmt.Errorf("loop already completed")
	}

	now := time.Now()
	query := `
		UPDATE loops SET
			enabled = false,
			completed_at = $2,
			completion_reason = $3,
			updated_at = $4
		WHERE id = $1
	`
	_, err = s.db.ExecContext(ctx, query, loopID, now, reason, now)
	return err
}

// DeleteLoop permanently deletes a loop and all its associated data.
func (s *Store) DeleteLoop(ctx context.Context, loopID string) error {
	// Start a transaction to ensure atomicity
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete run_tokens for all runs of this loop
	_, err = tx.ExecContext(ctx, `
		DELETE FROM run_tokens
		WHERE run_id IN (
			SELECT id FROM runs WHERE loop_id = $1
		)
	`, loopID)
	if err != nil {
		return fmt.Errorf("failed to delete run tokens: %w", err)
	}

	// Delete all runs for this loop
	_, err = tx.ExecContext(ctx, "DELETE FROM runs WHERE loop_id = $1", loopID)
	if err != nil {
		return fmt.Errorf("failed to delete runs: %w", err)
	}

	// Delete the loop itself
	result, err := tx.ExecContext(ctx, "DELETE FROM loops WHERE id = $1", loopID)
	if err != nil {
		return fmt.Errorf("failed to delete loop: %w", err)
	}

	// Check if loop existed
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("loop not found")
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ============================================================
// Run Operations
// ============================================================

// Run represents a single execution.
type Run struct {
	ID             string          `json:"id"`
	LoopID         string          `json:"loopId"`
	MachineID      string          `json:"machineId"`
	Role           protocol.RunRole `json:"role"`
	Status         string          `json:"status"`
	Outcome        string          `json:"outcome"`
	Message        string          `json:"message"`
	Error          string          `json:"error"`
	SessionID      string          `json:"sessionId"`
	Duration       int64           `json:"durationMs"`
	Cost           float64         `json:"costUsd"`
	InputTokens    int64           `json:"costInputTokens"`
	OutputTokens   int64           `json:"costOutputTokens"`
	CacheTokens    int64           `json:"costCacheReadTokens"`
	StartedAt      time.Time       `json:"startedAt"`
	EndedAt        *time.Time      `json:"endedAt"`
	CreatedAt      time.Time       `json:"createdAt"`
	LoopName       string          `json:"loopName,omitempty"`
}

// CreatePendingRun creates a pending run.
func (s *Store) CreatePendingRun(ctx context.Context, run *Run) error {
	query := `
		INSERT INTO runs (id, loop_id, machine_id, role, status, started_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.ExecContext(ctx, query,
		run.ID, run.LoopID, run.MachineID, run.Role, "pending", run.StartedAt, run.CreatedAt)
	return err
}

// ClaimRun claims a pending run for a machine.
func (s *Store) ClaimRun(ctx context.Context, runID, machineID string) (*Run, error) {
	var status string
	err := s.db.QueryRowContext(ctx, "SELECT status FROM runs WHERE id = $1", runID).Scan(&status)
	if err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, fmt.Errorf("run already claimed")
	}

	runToken := token.GenerateID()
	query := `UPDATE runs SET status = 'running', machine_id = $2 WHERE id = $1 AND status = 'pending'`
	_, err = s.db.ExecContext(ctx, query, runID, machineID)
	if err != nil {
		return nil, err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO run_tokens (run_id, token_hash, created_at)
		VALUES ($1, $2, $3)
	`, runID, runToken, time.Now())

	return s.GetRun(ctx, runID)
}

// ReportRun finalizes a run.
func (s *Store) ReportRun(ctx context.Context, runID string, report *protocol.ReportRequest) error {
	now := time.Now()

	outcome := report.Outcome
	if outcome == "" {
		if report.OK {
			outcome = "exec"
		} else {
			outcome = "error"
		}
	}

	var costUSD float64
	var inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int64
	var numTurns int

	if report.Cost != nil {
		costUSD = report.Cost.USD
		inputTokens = report.Cost.InputTokens
		outputTokens = report.Cost.OutputTokens
		cacheReadTokens = report.Cost.CacheReadTokens
		cacheCreationTokens = report.Cost.CacheCreationTokens
		numTurns = report.Cost.NumTurns
	}

	query := `
		UPDATE runs SET
			status = 'done',
			outcome = $2,
			message = $3,
			error = $4,
			session_id = $5,
			duration_ms = $6,
			cost_usd = $7,
			cost_input_tokens = $8,
			cost_output_tokens = $9,
			cost_cache_read_tokens = $10,
			cost_cache_creation_tokens = $11,
			cost_num_turns = $12,
			ended_at = $13
		WHERE id = $1
	`
	_, err := s.db.ExecContext(ctx, query,
		runID, outcome, report.Message, report.Error, report.SessionID, report.DurationMs,
		costUSD, inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens, numTurns, now)
	return err
}

// GetRun retrieves a run by ID.
func (s *Store) GetRun(ctx context.Context, runID string) (*Run, error) {
	query := `
		SELECT id, loop_id, machine_id, role, status, outcome, message, error, session_id,
		       duration_ms, cost_usd, cost_input_tokens, cost_output_tokens,
		       cost_cache_read_tokens, started_at, ended_at, created_at
		FROM runs WHERE id = $1
	`
	run := &Run{}
	var endedAt sql.NullTime
	var outcome, message, errMsg, sessionID sql.NullString
	var duration sql.NullInt64
	var costUSD sql.NullFloat64
	var inputTokens, outputTokens, cacheTokens sql.NullInt64

	err := s.db.QueryRowContext(ctx, query, runID).Scan(
		&run.ID, &run.LoopID, &run.MachineID, &run.Role, &run.Status,
		&outcome, &message, &errMsg, &sessionID, &duration,
		&costUSD, &inputTokens, &outputTokens, &cacheTokens,
		&run.StartedAt, &endedAt, &run.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if outcome.Valid {
		run.Outcome = outcome.String
	}
	if message.Valid {
		run.Message = message.String
	}
	if errMsg.Valid {
		run.Error = errMsg.String
	}
	if sessionID.Valid {
		run.SessionID = sessionID.String
	}
	if duration.Valid {
		run.Duration = duration.Int64
	}
	if costUSD.Valid {
		run.Cost = costUSD.Float64
	}
	if inputTokens.Valid {
		run.InputTokens = inputTokens.Int64
	}
	if outputTokens.Valid {
		run.OutputTokens = outputTokens.Int64
	}
	if cacheTokens.Valid {
		run.CacheTokens = cacheTokens.Int64
	}
	if endedAt.Valid {
		run.EndedAt = &endedAt.Time
	}

	return run, nil
}

// ListRunsByLoop retrieves runs for a loop.
func (s *Store) ListRunsByLoop(ctx context.Context, loopID string, limit int) ([]*Run, error) {
	query := `
		SELECT id, loop_id, machine_id, role, status, outcome, message, error, session_id,
		       duration_ms, started_at, ended_at, created_at
		FROM runs WHERE loop_id = $1 ORDER BY created_at DESC LIMIT $2
	`
	rows, err := s.db.QueryContext(ctx, query, loopID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		run := &Run{}
		var endedAt sql.NullTime
		var outcome, message, errMsg, sessionID sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(
			&run.ID, &run.LoopID, &run.MachineID, &run.Role, &run.Status,
			&outcome, &message, &errMsg, &sessionID, &duration,
			&run.StartedAt, &endedAt, &run.CreatedAt,
		); err != nil {
			return nil, err
		}
		if outcome.Valid {
			run.Outcome = outcome.String
		}
		if message.Valid {
			run.Message = message.String
		}
		if errMsg.Valid {
			run.Error = errMsg.String
		}
		if sessionID.Valid {
			run.SessionID = sessionID.String
		}
		if duration.Valid {
			run.Duration = duration.Int64
		}
		if endedAt.Valid {
			run.EndedAt = &endedAt.Time
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// ListPendingRuns retrieves all pending runs for a machine.
func (s *Store) ListPendingRuns(ctx context.Context, machineID string) ([]*Run, error) {
	query := `
		SELECT id, loop_id, machine_id, role, status, outcome, message, error, session_id,
		       duration_ms, started_at, ended_at, created_at
		FROM runs WHERE machine_id = $1 AND status = 'pending' ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, machineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		run := &Run{}
		var endedAt sql.NullTime
		var outcome, message, errMsg, sessionID sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(
			&run.ID, &run.LoopID, &run.MachineID, &run.Role, &run.Status,
			&outcome, &message, &errMsg, &sessionID, &duration,
			&run.StartedAt, &endedAt, &run.CreatedAt,
		); err != nil {
			return nil, err
		}
		if outcome.Valid {
			run.Outcome = outcome.String
		}
		if message.Valid {
			run.Message = message.String
		}
		if errMsg.Valid {
			run.Error = errMsg.String
		}
		if sessionID.Valid {
			run.SessionID = sessionID.String
		}
		if duration.Valid {
			run.Duration = duration.Int64
		}
		if endedAt.Valid {
			run.EndedAt = &endedAt.Time
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// GetRunTokenOwner validates a run token and returns owner and run ID.
func (s *Store) GetRunTokenOwner(ctx context.Context, tokenHash string) (string, string, error) {
	query := `
		SELECT r.loop_id, rt.run_id
		FROM run_tokens rt
		JOIN runs r ON r.id = rt.run_id
		WHERE rt.token_hash = $1
	`
	var loopID, runID string
	err := s.db.QueryRowContext(ctx, query, tokenHash).Scan(&loopID, &runID)
	return loopID, runID, err
}

// Stats holds aggregated statistics.
type Stats struct {
	TotalRuns          int64   `json:"total_runs"`
	ActiveLoops        int64   `json:"active_loops"`
	TotalCost          float64 `json:"total_cost"`
	TotalInputTokens   int64   `json:"total_input_tokens"`
	TotalOutputTokens  int64   `json:"total_output_tokens"`
	TotalCacheTokens   int64   `json:"total_cache_tokens"`
}

// GetStats returns aggregated statistics.
func (s *Store) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{}

	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs").Scan(&stats.TotalRuns)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM loops WHERE enabled = true").Scan(&stats.ActiveLoops)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(cost_usd), 0) FROM runs").Scan(&stats.TotalCost)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(cost_input_tokens), 0) FROM runs").Scan(&stats.TotalInputTokens)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(cost_output_tokens), 0) FROM runs").Scan(&stats.TotalOutputTokens)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(cost_cache_read_tokens), 0) FROM runs").Scan(&stats.TotalCacheTokens)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// ListRuns returns recent runs across all loops.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]*Run, error) {
	query := `
		SELECT r.id, r.loop_id, r.machine_id, r.role, r.status,
			r.outcome, r.message, r.error, r.session_id, r.duration_ms,
			r.cost_usd, r.cost_input_tokens, r.cost_output_tokens,
			r.cost_cache_read_tokens, r.started_at, r.ended_at, r.created_at,
			l.name as loop_name
		FROM runs r
		LEFT JOIN loops l ON l.id = r.loop_id
		ORDER BY r.created_at DESC
		LIMIT $1
	`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		run := &Run{}
		var outcome, message, errMsg, sessionID sql.NullString
		var duration sql.NullInt64
		var endedAt sql.NullTime
		var loopName sql.NullString
		var cost sql.NullFloat64
		var inputTokens, outputTokens, cacheTokens sql.NullInt64

		err := rows.Scan(
			&run.ID, &run.LoopID, &run.MachineID, &run.Role, &run.Status,
			&outcome, &message, &errMsg, &sessionID, &duration,
			&cost, &inputTokens, &outputTokens, &cacheTokens,
			&run.StartedAt, &endedAt, &run.CreatedAt, &loopName,
		)
		if err != nil {
			return nil, err
		}

		if endedAt.Valid {
			run.EndedAt = &endedAt.Time
		}
		if outcome.Valid {
			run.Outcome = outcome.String
		}
		if message.Valid {
			run.Message = message.String
		}
		if errMsg.Valid {
			run.Error = errMsg.String
		}
		if sessionID.Valid {
			run.SessionID = sessionID.String
		}
		if duration.Valid {
			run.Duration = duration.Int64
		}
		if cost.Valid {
			run.Cost = cost.Float64
		}
		if inputTokens.Valid {
			run.InputTokens = inputTokens.Int64
		}
		if outputTokens.Valid {
			run.OutputTokens = outputTokens.Int64
		}
		if cacheTokens.Valid {
			run.CacheTokens = cacheTokens.Int64
		}
		if loopName.Valid {
			run.LoopName = loopName.String
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func join(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
