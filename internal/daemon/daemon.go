// Package daemon implements the main poll loop.
package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/loopany-go/internal/artifact"
	"github.com/yourorg/loopany-go/internal/protocol"
	"github.com/yourorg/loopany-go/internal/runner"
	"github.com/yourorg/loopany-go/internal/watcher"
	"github.com/yourorg/loopany-go/internal/workflow"
	"github.com/yourorg/loopany-go/pkg/config"
	"github.com/yourorg/loopany-go/pkg/poll"
)

// Daemon polls for deliveries and executes them.
type Daemon struct {
	cfg          *config.Config
	pollClient   *poll.Client
	workflow     *workflow.Runner
	artifact     *artifact.Parser
	watchManager *WatchManager
	inFlight     sync.Map // runId -> *runState
	version      string
	roots        []string
}

type runState struct {
	cancel   context.CancelFunc
	done     chan struct{}
	progress *protocol.ProgressEntry
}

// New creates a new daemon.
func New(version string) (*Daemon, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if cfg.Token == "" || cfg.Server == "" {
		return nil, fmt.Errorf("missing config: set LOOPANY_SERVER_URL and LOOPANY_TOKEN or pass --server-url and --api-key")
	}

	return &Daemon{
		cfg:      cfg,
		pollClient: poll.NewClient(cfg.Server, cfg.Token),
		workflow: workflow.NewRunner(),
		artifact: artifact.NewParser(),
		version:  version,
		roots:    cfg.Roots,
	}, nil
}

// Run starts the daemon loop.
func (d *Daemon) Run(ctx context.Context) error {
	// Write PID file
	if err := config.WritePIDFile(); err != nil {
		log.Printf("Warning: could not write PID file: %v", err)
	}
	defer config.ClearPIDFile()

	// Start watch manager
	d.watchManager = NewWatchManager(d.cfg.Server, d.cfg.Token, d.roots)

	// Main poll loop
	info := config.LoadMachineInfo(d.version)
	var watchDigest string

	log.Printf("Daemon polling %s (poll interval: 3s, roots: %v)",
		d.cfg.Server, d.roots)

	for {
		select {
		case <-ctx.Done():
			d.drainInFlight()
			return nil
		default:
		}

		// Collect progress for in-flight runs
		progress := d.collectProgress()

		// Poll for deliveries
		start := time.Now()
		idle := len(progress) == 0

		resp, err := d.pollClient.Poll(ctx, info, progress, idle, watchDigest)
		if err != nil {
			log.Printf("Poll error: %v", err)
			time.Sleep(poll.NextPollDelay(time.Since(start)))
			continue
		}

		// Update watch set
		if len(resp.Watch) > 0 {
			d.watchManager.Reconcile(ctx, resp.Watch)
		}
		watchDigest = resp.WatchDigest

		// Execute deliveries in background
		for _, delivery := range resp.Deliveries {
			if _, loaded := d.inFlight.Load(delivery.RunID); !loaded {
				state := &runState{
					done:     make(chan struct{}),
					progress: &protocol.ProgressEntry{RunID: delivery.RunID},
				}
				d.inFlight.Store(delivery.RunID, state)

				runCtx, runCancel := context.WithCancel(ctx)
				state.cancel = runCancel

				go func(dlv protocol.Delivery) {
					defer func() {
						d.inFlight.Delete(dlv.RunID)
						close(state.done)
					}()
					d.executeDelivery(runCtx, dlv, state)
				}(delivery)
			}
		}

		// Adjust poll cadence based on elapsed time
		time.Sleep(poll.NextPollDelay(time.Since(start)))
	}
}

// executeDelivery runs one delivery (workflow gate + agent).
func (d *Daemon) executeDelivery(ctx context.Context, dlv protocol.Delivery, state *runState) {
	start := time.Now()

	// Enhanced logging
	log.Printf("════════════════════════════════════════════════════════════")
	log.Printf("🚀 Starting Run: %s", dlv.RunID)
	log.Printf("   Loop: %s (%s)", dlv.Loop.Name, dlv.Loop.ID)
	log.Printf("   Workdir: %s", dlv.Loop.Workdir)
	log.Printf("   Agent: %s", dlv.Loop.Agent)
	if dlv.Loop.Model != "" {
		log.Printf("   Model: %s", dlv.Loop.Model)
	}
	log.Printf("════════════════════════════════════════════════════════════")

	var result *ReportResult

	// Run workflow gate if present
	if dlv.Loop.Workflow != "" {
		log.Printf("🔄 Running workflow gate...")
		log.Printf("   Workflow: %s", dlv.Loop.Workflow)

		wfResult, err := d.workflow.Run(ctx, dlv.Loop.Workflow, dlv.PrevState, dlv.Loop.Workdir)
		if err != nil {
			log.Printf("❌ Workflow error: %v", err)
			result = &ReportResult{
				OK:    false,
				Error: err.Error(),
			}
		} else if !wfResult.OK {
			log.Printf("❌ Workflow returned error: %s", wfResult.Error)
			result = &ReportResult{
				OK:    false,
				Error: wfResult.Error,
			}
		} else if len(wfResult.AgentCalls) == 0 {
			// Direct message, no agent needed
			log.Printf("✅ Workflow returned direct message")
			log.Printf("   Message: %s", wfResult.Message)
			result = &ReportResult{
				OK:      true,
				Outcome: "direct",
				Message: wfResult.Message,
				Cursor:  wfResult.State,
			}
		} else {
			// Workflow escalated to agent
			log.Printf("⬆️  Workflow escalated to agent")
		}
	}

	// Run agent if workflow escalated or no workflow
	if result == nil {
		agentType := runner.AgentType(dlv.Loop.Agent)
		if agentType == "" {
			agentType = runner.AgentClaude // default
		}

		log.Printf("🤖 Starting agent execution...")
		log.Printf("   Agent type: %s", agentType)
		log.Printf("   Workdir: %s", dlv.Loop.Workdir)

		agentRunner := runner.NewRunner(agentType, dlv.Loop.Model, dlv.Loop.Workdir)

		// Set progress callback with more detailed status
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			lastStep := ""
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// Would extract progress from agent output
					if state.progress.Label != lastStep {
						log.Printf("📍 Progress: step %d - %s", state.progress.Step, state.progress.Label)
						lastStep = state.progress.Label
					}
					state.progress.Step++
					state.progress.Label = "executing"
				}
			}
		}()

		// Set run token for agent CLI
		os.Setenv("LOOPANY_RUN_TOKEN", dlv.RunToken)
		os.Setenv("LOOPANY_SERVER_URL", d.cfg.Server)

		prompt := d.buildPrompt(dlv)

		log.Printf("📝 Prompt length: %d characters", len(prompt))
		log.Printf("────────────────────────────────────────────────────────────────")

		runResult, err := agentRunner.Run(ctx, prompt, "")
		if err != nil {
			log.Printf("❌ Agent execution failed: %v", err)
			result = &ReportResult{
				OK:    false,
				Error: err.Error(),
			}
		} else {
			// Parse artifacts from session
			log.Printf("📦 Parsing artifacts from session...")
			artifacts, transcript := d.parseArtifacts(runResult.SessionID, dlv.Loop.Workdir)

			log.Printf("   Session ID: %s", runResult.SessionID)
			log.Printf("   Exit code: %d", runResult.ExitCode)
			log.Printf("   Duration: %s", runResult.Duration)
			if runResult.Cost != nil {
				log.Printf("   Cost: $%.4f (in: %d, out: %d, cache: %d/%d)",
					runResult.Cost.USD,
					runResult.Cost.InputTokens,
					runResult.Cost.OutputTokens,
					runResult.Cost.CacheReadTokens,
					runResult.Cost.CacheCreationTokens)
			}
			if len(artifacts) > 0 {
				log.Printf("   Artifacts: %d files", len(artifacts))
				for _, a := range artifacts {
					log.Printf("      - %s (%s)", a.Path, a.Kind)
				}
			}

			result = &ReportResult{
				OK:         runResult.ExitCode == 0,
				Outcome:    "exec",
				SessionID:  runResult.SessionID,
				Cost:       runResult.Cost,
				Artifacts:  artifacts,
				Transcript: transcript,
				DurationMs: runResult.Duration.Milliseconds(),
			}
		}
	}

	result.DurationMs = time.Since(start).Milliseconds()

	// Report back to server
	log.Printf("────────────────────────────────────────────────────────────────")
	log.Printf("📡 Reporting to server...")

	report := &protocol.ReportRequest{
		RunID:      dlv.RunID,
		OK:         result.OK,
		DurationMs: result.DurationMs,
		Outcome:    result.Outcome,
		Message:    result.Message,
		Cursor:     result.Cursor,
		SessionID:  result.SessionID,
		Cost:       result.Cost,
		Artifacts:  result.Artifacts,
		Transcript: result.Transcript,
		Error:      result.Error,
	}

	if err := d.pollClient.Report(ctx, dlv.RunToken, report); err != nil {
		log.Printf("❌ Report error for %s: %v", dlv.RunID, err)
	} else {
		if result.OK {
			log.Printf("✅ Run completed successfully in %dms", result.DurationMs)
		} else {
			log.Printf("❌ Run failed after %dms: %s", result.DurationMs, result.Error)
		}
	}
	log.Printf("════════════════════════════════════════════════════════════")

	// Flush watcher for this loop
	if d.watchManager != nil {
		d.watchManager.FlushLoop(dlv.Loop.ID)
	}

	// Save loop state locally
	if result.Cursor != nil {
		config.SaveLoopState(dlv.Loop.ID, result.Cursor)
	}
}

func (d *Daemon) buildPrompt(dlv protocol.Delivery) string {
	// Server already built the full exec-core prompt in dlv.Task
	// Only add system prompt if present
	prompt := dlv.Task

	if dlv.SystemPrompt != "" {
		prompt = dlv.SystemPrompt + "\n\n" + prompt
	}

	return prompt
}

func (d *Daemon) parseArtifacts(sessionID, workdir string) ([]protocol.RunArtifact, []protocol.TranscriptStep) {
	if sessionID == "" {
		return nil, nil
	}

	result, err := d.artifact.ParseSession(sessionID, workdir)
	if err != nil {
		log.Printf("Parse artifacts error: %v", err)
		return nil, nil
	}
	return result.Artifacts, result.Transcript
}

func (d *Daemon) collectProgress() []protocol.ProgressEntry {
	var progress []protocol.ProgressEntry
	d.inFlight.Range(func(key, value interface{}) bool {
		if state, ok := value.(*runState); ok && state.progress != nil {
			progress = append(progress, *state.progress)
		}
		return true
	})
	return progress
}

func (d *Daemon) drainInFlight() {
	// Wait for in-flight runs to complete
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		d.inFlight.Range(func(key, value interface{}) bool {
			if state, ok := value.(*runState); ok && state.done != nil {
				select {
				case <-state.done:
				case <-ctx.Done():
				}
			}
			return true
		})
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// ReportResult wraps the report request for internal use.
type ReportResult struct {
	OK         bool
	Outcome    string
	Message    string
	Cursor     interface{}
	SessionID  string
	Cost       *protocol.RunCost
	Artifacts  []protocol.RunArtifact
	Transcript []protocol.TranscriptStep
	DurationMs int64
	Error      string
}

// ============================================================
// Watch Manager
// ============================================================

// WatchManager manages multiple loop watchers.
type WatchManager struct {
	server string
	token  string
	roots  []string
	mu     sync.Mutex
	active map[string]*watcher.Watcher
}

// NewWatchManager creates a watch manager.
func NewWatchManager(server, token string, roots []string) *WatchManager {
	return &WatchManager{
		server: server,
		token:  token,
		roots:  roots,
		active: make(map[string]*watcher.Watcher),
	}
}

// Reconcile updates the watch set.
func (m *WatchManager) Reconcile(ctx context.Context, specs []protocol.WatchSpec) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Track which loops we've seen
	seen := make(map[string]bool)

	for _, spec := range specs {
		seen[spec.LoopID] = true

		// Skip if already watching
		if _, exists := m.active[spec.LoopID]; exists {
			continue
		}

		// Resolve loop directory
		path := resolveLoopDir(spec)
		if path == "" {
			continue
		}

		// Check if within roots jail
		if !m.isWithinRoots(path) {
			log.Printf("Loop %s workdir %s outside roots jail", spec.LoopID, path)
			continue
		}

		// Start watcher
		w, err := watcher.NewWatcher(spec.LoopID, path, m.server, m.token)
		if err != nil {
			log.Printf("Watch error for loop %s: %v", spec.LoopID, err)
			continue
		}

		m.active[spec.LoopID] = w
		go w.Start(ctx)

		log.Printf("Watching loop %s: %s", spec.LoopID, path)
	}

	// Stop watchers for removed loops
	for id, w := range m.active {
		if !seen[id] {
			w.Stop()
			delete(m.active, id)
			log.Printf("Stopped watching loop %s", id)
		}
	}
}

// FlushLoop flushes a specific loop's watcher.
func (m *WatchManager) FlushLoop(loopID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if w, exists := m.active[loopID]; exists {
		w.FlushNow(context.Background())
	}
}

func (m *WatchManager) isWithinRoots(path string) bool {
	if len(m.roots) == 0 {
		return true
	}
	absPath, _ := filepath.Abs(path)
	for _, root := range m.roots {
		absRoot, _ := filepath.Abs(root)
		if strings.HasPrefix(absPath, absRoot) {
			return true
		}
	}
	return false
}

func resolveLoopDir(spec protocol.WatchSpec) string {
	if spec.Workdir != "" {
		return spec.Workdir
	}
	if spec.TaskFile != "" {
		return filepath.Dir(spec.TaskFile)
	}
	return ""
}