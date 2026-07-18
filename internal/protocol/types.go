// Package protocol defines the HTTP API types shared between daemon and server.
package protocol

import "time"

// ============================================================
// Machine Identity
// ============================================================

// MachineInfo is the machine identity reported on every poll.
type MachineInfo struct {
	Host     string `json:"host"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
}

// ============================================================
// Poll Protocol
// ============================================================

// PollRequest is sent by daemon to claim pending runs.
type PollRequest struct {
	MachineInfo
	// Progress heartbeat for in-flight runs
	Progress []ProgressEntry `json:"progress,omitempty"`
	// Opt-in to server-held long-poll when idle
	Wait bool `json:"wait,omitempty"`
	// Echo last watch digest (incremental sync)
	WatchDigest string `json:"watchDigest,omitempty"`
}

// ProgressEntry is a single run's progress heartbeat.
type ProgressEntry struct {
	RunID string `json:"runId"`
	Step  int    `json:"step"`
	Label string `json:"label"`
}

// PollResponse is returned by server with claimed deliveries.
type PollResponse struct {
	// Runs claimed by this machine
	Deliveries []Delivery `json:"deliveries,omitempty"`
	// Watch set for this machine (server-authoritative)
	Watch []WatchSpec `json:"watch,omitempty"`
	// Watch digest to echo on next poll
	WatchDigest string `json:"watchDigest,omitempty"`
}

// ============================================================
// Delivery (Run to Execute)
// ============================================================

// Delivery represents one run to execute.
type Delivery struct {
	RunID        string   `json:"runId"`
	RunToken     string   `json:"runToken"`
	Role         RunRole  `json:"role"`
	Loop         LoopInfo `json:"loop"`
	PrevState    any      `json:"prevState"`
	Roots        []string `json:"roots,omitempty"`
	SystemPrompt string   `json:"systemPrompt"`
	Task         string   `json:"task"`
	CanFinish    bool     `json:"canFinish"` // true for exec run on closed loop
}

// RunRole distinguishes run types.
type RunRole string

const (
	RoleExec   RunRole = "exec"   // scheduled run
	RoleEvolve RunRole = "evolve" // self-improvement pass
	RoleEdit   RunRole = "edit"   // owner-requested change
)

// LoopInfo describes the loop to run.
type LoopInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Task         string `json:"task"`
	Workdir      string `json:"workdir"`
	TaskFile     string `json:"taskFile"`
	Workflow     string `json:"workflow"`
	Model        string `json:"model"`
	AllowControl bool   `json:"allowControl"`
	Agent        string `json:"agent,omitempty"` // claude-code | codex | grok
	Goal         string `json:"goal,omitempty"`
}

// WatchSpec is one loop folder to watch.
type WatchSpec struct {
	LoopID   string `json:"loopId"`
	Workdir  string `json:"workdir"`
	TaskFile string `json:"taskFile"`
}

// ============================================================
// Report Protocol
// ============================================================

// ReportRequest finalizes a run.
type ReportRequest struct {
	RunID           string           `json:"runId"`
	OK              bool             `json:"ok"`
	DurationMs      int64            `json:"durationMs"`
	Outcome         string           `json:"outcome,omitempty"`
	Message         string           `json:"message,omitempty"`
	Cursor          any              `json:"cursor,omitempty"`
	SessionID       string           `json:"sessionId,omitempty"`
	Cost            *RunCost         `json:"cost,omitempty"`
	Attempts        int              `json:"attempts,omitempty"`
	Artifacts       []RunArtifact    `json:"artifacts,omitempty"`
	Transcript      []TranscriptStep `json:"transcript,omitempty"`
	TaskFileContent string           `json:"taskFileContent,omitempty"`
	Error           string           `json:"error,omitempty"`
	FinalText       string           `json:"finalText,omitempty"`
}

// RunCost is claude-reported spend/usage.
type RunCost struct {
	USD                 float64 `json:"usd,omitempty"`
	InputTokens         int64   `json:"inputTokens,omitempty"`
	OutputTokens        int64   `json:"outputTokens,omitempty"`
	CacheReadTokens     int64   `json:"cacheReadTokens,omitempty"`
	CacheCreationTokens int64   `json:"cacheCreationTokens,omitempty"`
	NumTurns            int     `json:"numTurns,omitempty"`
}

// RunArtifact is a file created/edited during a run.
type RunArtifact struct {
	Path string       `json:"path"`
	Kind ArtifactKind `json:"kind"`
}

type ArtifactKind string

const (
	ArtifactCreated ArtifactKind = "created"
	ArtifactEdited  ArtifactKind = "edited"
)

// TranscriptStep is one slimmed step of the run trace.
type TranscriptStep struct {
	Kind  string `json:"kind"` // text | tool | result
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`
	Input string `json:"input,omitempty"`
}

// ============================================================
// CLI Protocol
// ============================================================

// CLIRequest is the unified callback request from agent.
type CLIRequest struct {
	Argv []string `json:"argv"`
}

// CLIResponse is the unified callback response.
type CLIResponse struct {
	Text     string `json:"text"`
	ExitCode int    `json:"exitCode"`
}

// ============================================================
// Sync Protocol
// ============================================================

// WatchManifest is the file manifest for sync.
type WatchManifest struct {
	LoopID string               `json:"loopId"`
	Files  []WatchManifestEntry `json:"files"`
	Digest string               `json:"digest"`
}

type WatchManifestEntry struct {
	Path    string    `json:"path"`
	Hash    string    `json:"hash"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Bytes   []byte    `json:"bytes,omitempty"` // inlined small files
}

// ============================================================
// Owner API Types
// ============================================================

// CreateLoopRequest creates a new loop.
type CreateLoopRequest struct {
	Name         string   `json:"name"`
	Cron         string   `json:"cron"`
	Timezone     string   `json:"timezone,omitempty"`
	Workdir      string   `json:"workdir"`
	TaskFile     string   `json:"taskFile,omitempty"`
	Workflow     string   `json:"workflow,omitempty"`
	Model        string   `json:"model,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	Notify       []string `json:"notify,omitempty"`
	Enabled      bool     `json:"enabled"`
	Goal         string   `json:"goal,omitempty"`
	AllowControl bool     `json:"allowControl"`
}

// UpdateLoopRequest updates a loop.
type UpdateLoopRequest struct {
	Name         string   `json:"name,omitempty"`
	Cron         string   `json:"cron,omitempty"`
	Timezone     string   `json:"timezone,omitempty"`
	Workdir      string   `json:"workdir,omitempty"`
	TaskFile     string   `json:"taskFile,omitempty"`
	Workflow     string   `json:"workflow,omitempty"`
	Model        string   `json:"model,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	Notify       []string `json:"notify,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
	Goal         string   `json:"goal,omitempty"`
	AllowControl *bool    `json:"allowControl,omitempty"`
}

// LoopResponse is the loop API response.
type LoopResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Task         string   `json:"task"`
	MachineID    string   `json:"machineId"`
	Cron         string   `json:"cron"`
	Timezone     string   `json:"timezone"`
	Workdir      string   `json:"workdir"`
	TaskFile     string   `json:"taskFile"`
	Workflow     string   `json:"workflow"`
	Model        string   `json:"model"`
	Agent        string   `json:"agent"`
	Notify       []string `json:"notify"`
	Enabled      bool     `json:"enabled"`
	Goal         string   `json:"goal"`
	AllowControl bool     `json:"allowControl"`
	State        any      `json:"state,omitempty"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

// RunResponse is the run API response.
type RunResponse struct {
	ID                  string       `json:"id"`
	LoopID              string       `json:"loopId"`
	LoopName            string       `json:"loop_name,omitempty"`
	Role                string       `json:"role"`
	Status              string       `json:"status"`
	Outcome             string       `json:"outcome,omitempty"`
	Message             string       `json:"message,omitempty"`
	Error               string       `json:"error,omitempty"`
	SessionId           string       `json:"session_id,omitempty"`
	Duration            int64        `json:"durationMs,omitempty"`
	CostUsd             float64      `json:"cost_usd,omitempty"`
	CostInputTokens     int64        `json:"cost_input_tokens,omitempty"`
	CostOutputTokens    int64        `json:"cost_output_tokens,omitempty"`
	CostCacheReadTokens int64        `json:"cost_cache_read_tokens,omitempty"`
	Cost                *RunCost     `json:"cost,omitempty"`
	Artifacts           []RunArtifact `json:"artifacts,omitempty"`
	StartedAt           string       `json:"startedAt"`
	EndedAt             string       `json:"endedAt,omitempty"`
	CreatedAt           string       `json:"createdAt"`
}