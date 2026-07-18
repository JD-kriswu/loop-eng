// Package server implements the Loopany server (scheduler + gateway).
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/yourorg/loopany-go/internal/protocol"
	"github.com/yourorg/loopany-go/internal/store"
)

// Server is the Loopany scheduling server.
type Server struct {
	addr       string
	store      *store.Store
	scheduler  *Scheduler
	dispatcher *Dispatcher
	http       *http.Server
}

// Store interface for persistence (implement with Postgres/SQLite).
type Store interface {
	// Machine operations
	RegisterMachine(ctx context.Context, machineID, ownerID string, info protocol.MachineInfo) error
	GetMachine(ctx context.Context, id string) (*Machine, error)
	ListMachinesByOwner(ctx context.Context, ownerID string) ([]*Machine, error)

	// Loop operations
	CreateLoop(ctx context.Context, loop *Loop) error
	GetLoop(ctx context.Context, id string) (*Loop, error)
	ListLoopsByMachine(ctx context.Context, machineID string) ([]*Loop, error)
	UpdateLoop(ctx context.Context, id string, updates map[string]interface{}) error

	// Run operations
	CreatePendingRun(ctx context.Context, run *Run) error
	ClaimRun(ctx context.Context, runID, machineID string) (*Run, error)
	ReportRun(ctx context.Context, runID string, report *protocol.ReportRequest) error
	GetRun(ctx context.Context, runID string) (*Run, error)
	ListRunsByLoop(ctx context.Context, loopID string, limit int) ([]*Run, error)

	// Auth
	GetDeviceOwner(ctx context.Context, token string) (string, error)
	GetRunTokenOwner(ctx context.Context, token string) (string, string, error) // ownerID, runID
}

// Machine represents a registered daemon.
type Machine struct {
	ID        string
	OwnerID   string
	Token     string
	Host      string
	Platform  string
	LastSeen  time.Time
	CreatedAt time.Time
}

// Loop represents a scheduled agent loop.
type Loop struct {
	ID           string
	MachineID    string
	Name         string
	Cron         string
	Timezone     string
	Workdir      string
	TaskFile     string
	Workflow     string
	Model        string
	Agent        string
	Notify       []string
	Enabled      bool
	Goal         string
	AllowControl bool
	State        interface{}
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Run represents a single execution.
type Run struct {
	ID        string
	LoopID    string
	MachineID string
	Role      protocol.RunRole
	Status    string // pending, running, done, error, skipped
	StartedAt time.Time
	EndedAt   time.Time
	Duration  int64
	Outcome   string
	Message   string
	Cost      *protocol.RunCost
}

// Config is server configuration.
type Config struct {
	Addr        string
	DatabaseURL string
	BlobStore   string // "r2" or "local"
}

// New creates a new server.
func New(cfg Config, dbStore *store.Store) *Server {
	dispatcher := NewDispatcher(dbStore)
	scheduler := NewScheduler(dbStore)

	return &Server{
		addr:       cfg.Addr,
		store:      dbStore,
		scheduler:  scheduler,
		dispatcher: dispatcher,
		http: &http.Server{
			Addr: cfg.Addr,
		},
	}
}

// Start begins serving requests.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Machine gateway
	mux.HandleFunc("/api/machine/poll", s.handlePoll)
	mux.HandleFunc("/machine/report", s.handleReport)
	mux.HandleFunc("/api/machine/cli", s.handleCLI)
	mux.HandleFunc("/api/machine/sync", s.handleSync)

	// Owner API
	mux.HandleFunc("/api/loops", s.handleListLoops)
	mux.HandleFunc("/api/loops/", s.handleLoopOps)

	// Run detail API
	mux.HandleFunc("/api/runs/", s.handleRunOps)

	s.http.Handler = mux

	// Start scheduler
	go s.scheduler.Start(ctx)

	// Start HTTP
	return s.http.ListenAndServe()
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.scheduler.Stop()
	return s.http.Shutdown(ctx)
}

// handlePoll handles POST /api/machine/poll.
func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	tok := extractBearerToken(r)
	if tok == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	ownerID, err := s.store.GetDeviceOwner(r.Context(), tok)
	if err != nil {
		http.Error(w, "invalid token", 401)
		return
	}

	var req protocol.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	// Update machine presence
	machineID := machineIDFromToken(tok)
	_ = ownerID // suppress unused variable warning
	s.store.RegisterMachine(r.Context(), machineID, ownerID, protocol.MachineInfo{
		Host:     req.Host,
		Platform: req.Platform,
		Arch:     req.Arch,
		Version:  req.Version,
	})

	// Claim pending runs
	deliveries := s.dispatcher.ClaimPending(machineID, req.Wait)

	// Get watch set for this machine's loops
	loops, _ := s.store.ListLoopsByMachine(r.Context(), machineID)
	watch := make([]protocol.WatchSpec, len(loops))
	for i, loop := range loops {
		watch[i] = protocol.WatchSpec{
			LoopID:   loop.ID,
			Workdir:  loop.Workdir,
			TaskFile: loop.TaskFile,
		}
	}

	resp := protocol.PollResponse{
		Deliveries: deliveries,
		Watch:      watch,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleReport handles POST /machine/report.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	tok := extractBearerToken(r)
	if tok == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	_, runID, err := s.store.GetRunTokenOwner(r.Context(), tok)
	if err != nil {
		http.Error(w, "invalid run token", 401)
		return
	}

	var req protocol.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	req.RunID = runID
	if err := s.store.ReportRun(r.Context(), runID, &req); err != nil {
		http.Error(w, "report failed", 500)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleCLI handles POST /api/machine/cli (callback from agent).
func (s *Server) handleCLI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	token := extractBearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	var req protocol.CLIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	// Dispatch verb
	resp := s.dispatchCLI(token, req.Argv)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleSync handles artifact sync.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	// Content-addressed blob sync
	http.Error(w, "not implemented", 501)
}

// handleListLoops handles GET /api/loops.
func (s *Server) handleListLoops(w http.ResponseWriter, r *http.Request) {
	// Owner listing
	http.Error(w, "not implemented", 501)
}

// handleLoopOps handles /api/loops/{id}.
func (s *Server) handleLoopOps(w http.ResponseWriter, r *http.Request) {
	// CRUD for loops
	http.Error(w, "not implemented", 501)
}

func machineIDFromToken(token string) string {
	// Token format: {machine_id}@device
	parts := strings.SplitN(token, "@", 2)
	return parts[0]
}

func (s *Server) dispatchCLI(token string, argv []string) *protocol.CLIResponse {
	// Implement verb dispatch
	return &protocol.CLIResponse{
		Text:     "not implemented",
		ExitCode: 1,
	}
}