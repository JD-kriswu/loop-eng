// Package server implements the Loopany HTTP gateway.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yourorg/loopany-go/internal/protocol"
	"github.com/yourorg/loopany-go/internal/server/prompts"
	"github.com/yourorg/loopany-go/internal/store"
	"github.com/yourorg/loopany-go/pkg/token"
)

// Gateway is the HTTP gateway for daemon and owner API.
type Gateway struct {
	store    *store.Store
	tokenGen *token.Generator
}

// NewGateway creates a new gateway.
func NewGateway(store *store.Store, tokenGen *token.Generator) *Gateway {
	return &Gateway{
		store:    store,
		tokenGen: tokenGen,
	}
}

// ============================================================
// Machine Gateway (daemon API)
// ============================================================

// HandlePoll handles POST /api/machine/poll
func (g *Gateway) HandlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	token := extractBearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	// Validate device token
	machineID, err := g.tokenGen.ValidateDeviceToken(token)
	if err != nil {
		http.Error(w, "invalid token: "+err.Error(), 401)
		return
	}
	w.Header().Set("X-Machine-Id", machineID) // Debug header
	fmt.Fprintf(os.Stderr, "[POLL] validated token=%s machineID=%s\n", token[:20]+"...", machineID)

	// Parse request
	var req protocol.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	ctx := r.Context()

	// Register/update machine
	ownerID := "admin" // In production, extracted from token
	g.store.RegisterMachine(ctx, machineID, ownerID, req.MachineInfo)

	// Check for pending runs (long-poll support)
	fmt.Fprintf(os.Stderr, "[POLL] req.Wait=%v\n", req.Wait)
	var runs []*store.Run
	if req.Wait {
		runs = g.waitForPendingRuns(ctx, machineID, 20*time.Second)
	} else {
		var err error
		runs, err = g.store.ListPendingRuns(ctx, machineID)
		fmt.Fprintf(os.Stderr, "[POLL] machineID=%s pending_runs=%d error=%v\n", machineID, len(runs), err)
		if len(runs) > 0 {
			for _, r := range runs {
				fmt.Fprintf(os.Stderr, "[POLL] run: id=%s status=%s machine=%s\n", r.ID, r.Status, r.MachineID)
			}
		}
	}

	// Claim runs and build deliveries
	deliveries := make([]protocol.Delivery, 0, len(runs))
	log.Printf("[POLL] claiming %d runs for machineID=%s", len(runs), machineID)
	for _, run := range runs {
		claimedRun, err := g.store.ClaimRun(ctx, run.ID, machineID)
		if err != nil {
			log.Printf("[POLL] claim failed: %v", err)
			continue // Already claimed by another poll
		}

		// Get loop for this run
		loop, err := g.store.GetLoop(ctx, claimedRun.LoopID)
		if err != nil {
			continue
		}

		// Generate run token
		runToken, _ := g.tokenGen.GenerateRunToken(run.ID, machineID)

		// Build exec-core task prompt
		taskFile := loop.TaskFile
		if taskFile == "" {
			taskFile = "(none — this loop has no task file yet)"
		}

		goalLine := ""
		if loop.Goal != "" {
			goalLine = "Goal (finish line): " + loop.Goal
		}

		task := prompts.BuildExecTask(prompts.ExecCoreData{
			Name:      loop.Name,
			TaskFile:  taskFile,
			GoalLine:  goalLine,
			StateLine: "loopany report --status new --message \"<message>\"",
		})

		// Build delivery
		delivery := protocol.Delivery{
			RunID:    run.ID,
			RunToken: runToken,
			Role:     run.Role,
			Loop: protocol.LoopInfo{
				ID:           loop.ID,
				Name:         loop.Name,
				Workdir:      loop.Workdir,
				TaskFile:     loop.TaskFile,
				Workflow:     loop.Workflow,
				Model:        loop.Model,
				Agent:        loop.Agent,
				AllowControl: loop.AllowControl,
				Goal:         loop.Goal,
			},
			PrevState:    loop.State,
			SystemPrompt: "",
			Task:         task,
			CanFinish:    run.Role == protocol.RoleExec && loop.Goal != "",
		}
		deliveries = append(deliveries, delivery)
	}

	// Build watch set for this machine's loops
	loops, _ := g.store.ListLoopsByMachine(ctx, machineID)
	watch := make([]protocol.WatchSpec, len(loops))
	for i, loop := range loops {
		watch[i] = protocol.WatchSpec{
			LoopID:   loop.ID,
			Workdir:  loop.Workdir,
			TaskFile: loop.TaskFile,
		}
	}

	// Response
	resp := protocol.PollResponse{
		Deliveries: deliveries,
		Watch:      watch,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleReport handles POST /machine/report
func (g *Gateway) HandleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	token := extractBearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	// Validate run token
	runID, _, err := g.tokenGen.ValidateRunToken(token)
	if err != nil {
		http.Error(w, "invalid token: "+err.Error(), 401)
		return
	}

	// Parse report
	var req protocol.ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	req.RunID = runID

	// Update run
	ctx := r.Context()
	if err := g.store.ReportRun(ctx, runID, &req); err != nil {
		http.Error(w, "report failed: "+err.Error(), 500)
		return
	}

	// Update loop state if cursor provided
	if req.Cursor != nil {
		parts := strings.Split(runID, ":")
		if len(parts) > 0 {
			loopID := parts[0]
			g.store.UpdateLoop(ctx, loopID, map[string]interface{}{
				"state": req.Cursor,
			})
		}
	}

	w.WriteHeader(http.StatusOK)
}

// HandleCLI handles POST /api/machine/cli (callback from agent)
func (g *Gateway) HandleCLI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	token := extractBearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	// Validate run token
	runID, _, err := g.tokenGen.ValidateRunToken(token)
	if err != nil {
		http.Error(w, "invalid token: "+err.Error(), 401)
		return
	}

	// Parse CLI request
	var req protocol.CLIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	// Dispatch verb
	resp := g.dispatchCLI(r.Context(), runID, req.Argv)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleSync handles POST /api/machine/sync (artifact sync)
func (g *Gateway) HandleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	token := extractBearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", 401)
		return
	}

	// Validate device token
	_, err := g.tokenGen.ValidateDeviceToken(token)
	if err != nil {
		http.Error(w, "invalid token: "+err.Error(), 401)
		return
	}

	// Parse manifest
	var manifest protocol.WatchManifest
	if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
		http.Error(w, "invalid manifest", 400)
		return
	}

	// Store manifest
	// In production: store in blob storage, return missing hashes

	// For now, return empty (assume all blobs are new)
	needHashes := []string{}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(needHashes)
}

// ============================================================
// Owner API
// ============================================================

// HandleListLoops handles GET /api/loops
func (g *Gateway) HandleListLoops(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// In production: validate owner token
	ownerID := "admin"

	ctx := r.Context()

	// Get all machines for owner
	machines, err := g.store.ListMachinesByOwner(ctx, ownerID)
	if err != nil {
		http.Error(w, "failed to list machines", 500)
		return
	}

	// Get loops for each machine
	var allLoops []*store.Loop
	for _, machine := range machines {
		loops, err := g.store.ListLoopsByMachine(ctx, machine.ID)
		if err != nil {
			continue
		}
		allLoops = append(allLoops, loops...)
	}

	// Convert to response
	resp := make([]protocol.LoopResponse, len(allLoops))
	for i, loop := range allLoops {
		resp[i] = loopToResponse(loop)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleLoopOps handles /api/loops/{id}
func (g *Gateway) HandleLoopOps(w http.ResponseWriter, r *http.Request) {
	// Extract loop ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/loops/")
	loopID := strings.TrimSuffix(path, "/")

	ctx := r.Context()

	switch r.Method {
	case "GET":
		loop, err := g.store.GetLoop(ctx, loopID)
		if err != nil {
			http.Error(w, "loop not found", 404)
			return
		}
		resp := loopToResponse(loop)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)

	case "PATCH", "PUT":
		var req protocol.UpdateLoopRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		updates := make(map[string]interface{})
		if req.Name != "" {
			updates["name"] = req.Name
		}
		if req.Cron != "" {
			updates["cron"] = req.Cron
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
			if req.Timezone != "" {
				updates["timezone"] = req.Timezone
			}
			if req.Workdir != "" {
				updates["workdir"] = req.Workdir
			}
			if req.TaskFile != "" {
				updates["task_file"] = req.TaskFile
			}
		}
		if req.Goal != "" {
			updates["goal"] = req.Goal
		}
		if req.Workflow != "" {
			updates["workflow"] = req.Workflow
		}
		if req.Model != "" {
			updates["model"] = req.Model
		}
		if req.Agent != "" {
			updates["agent"] = req.Agent
		}

		if err := g.store.UpdateLoop(ctx, loopID, updates); err != nil {
			http.Error(w, "update failed", 500)
			return
		}

		// Return updated loop
		loop, _ := g.store.GetLoop(ctx, loopID)
		resp := loopToResponse(loop)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)

	case "DELETE":
		// Disable the loop (soft delete)
		g.store.UpdateLoop(ctx, loopID, map[string]interface{}{
			"enabled": false,
		})
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// HandleCreateLoop handles POST /api/loops
func (g *Gateway) HandleCreateLoop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req protocol.CreateLoopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}

	// In production: validate owner token
	ownerID := "admin"

	ctx := r.Context()

	// Create machine if not exists
	machineID := token.GenerateID()
	g.store.RegisterMachine(ctx, machineID, ownerID, protocol.MachineInfo{})

	// Create loop
	loop := &store.Loop{
		ID:           token.GenerateID(),
		MachineID:    machineID,
		Name:         req.Name,
		Cron:         req.Cron,
		Timezone:     req.Timezone,
		Workdir:      req.Workdir,
		TaskFile:     req.TaskFile,
		Workflow:     req.Workflow,
		Model:        req.Model,
		Agent:        req.Agent,
		Notify:       req.Notify,
		Enabled:      req.Enabled,
		Goal:         req.Goal,
		AllowControl: req.AllowControl,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := g.store.CreateLoop(ctx, loop); err != nil {
		http.Error(w, "create failed", 500)
		return
	}

	// Generate device token for this machine
	deviceToken, _ := g.tokenGen.GenerateDeviceToken(machineID)

	resp := loopToResponse(loop)
	respMap, _ := json.Marshal(resp)
	var respWithToken map[string]interface{}
	json.Unmarshal(respMap, &respWithToken)
	respWithToken["deviceToken"] = deviceToken

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(respWithToken)
}

// HandleListRuns handles GET /api/loops/{id}/runs
func (g *Gateway) HandleListRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Extract loop ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/loops/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "invalid path", 400)
		return
	}
	loopID := parts[0]

	ctx := r.Context()

	runs, err := g.store.ListRunsByLoop(ctx, loopID, 50)
	if err != nil {
		http.Error(w, "failed to list runs", 500)
		return
	}

	resp := make([]protocol.RunResponse, len(runs))
	for i, run := range runs {
		resp[i] = runToResponse(run)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleTriggerRun handles POST /api/loops/{id}/runs
func (g *Gateway) HandleTriggerRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Extract loop ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/loops/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "invalid path", 400)
		return
	}
	loopID := parts[0]

	// Parse request body (optional goal override)
	var req struct {
		Goal string `json:"goal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Body is optional, ignore decode errors
	}

	ctx := r.Context()

	// Get loop
	loop, err := g.store.GetLoop(ctx, loopID)
	if err != nil {
		http.Error(w, "loop not found", 404)
		return
	}

	// Create pending run
	runID := fmt.Sprintf("run-%s", token.GenerateID())
	now := time.Now()
	run := &store.Run{
		ID:        runID,
		LoopID:    loopID,
		MachineID: loop.MachineID,
		Role:      protocol.RoleExec,
		Status:    "pending",
		StartedAt: now,
		CreatedAt: now,
	}

	if err := g.store.CreatePendingRun(ctx, run); err != nil {
		http.Error(w, "failed to create run", 500)
		return
	}

	// Return run ID
	resp := map[string]interface{}{
		"run_id": runID,
		"loop_id": loopID,
		"status": "pending",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleStats handles GET /api/stats
func (g *Gateway) HandleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	ctx := r.Context()

	// Get aggregated stats
	stats, err := g.store.GetStats(ctx)
	if err != nil {
		http.Error(w, "failed to get stats", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleListAllRuns handles GET /api/runs
func (g *Gateway) HandleListAllRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	ctx := r.Context()
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	runs, err := g.store.ListRuns(ctx, limit)
	if err != nil {
		log.Printf("ListRuns error: %v", err)
		http.Error(w, "failed to list runs", 500)
		return
	}

	resp := make([]protocol.RunResponse, len(runs))
	for i, run := range runs {
		resp[i] = runToResponse(run)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleGetRun handles GET /api/runs/{id}
func (g *Gateway) HandleGetRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	runID := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if runID == "" || runID == "/" {
		http.Error(w, "run id required", 400)
		return
	}

	ctx := r.Context()
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		http.Error(w, "run not found", 404)
		return
	}

	resp := runToResponse(run)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ============================================================
// Helpers
// ============================================================

func (g *Gateway) waitForPendingRuns(ctx context.Context, machineID string, timeout time.Duration) []*store.Run {
	// Check immediately first
	runs, err := g.store.ListPendingRuns(ctx, machineID)
	fmt.Fprintf(os.Stderr, "[WAIT] initial check: machineID=%s runs=%d error=%v\n", machineID, len(runs), err)
	if len(runs) > 0 {
		return runs
	}

	// Poll every second until timeout
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(1 * time.Second):
			runs, _ = g.store.ListPendingRuns(ctx, machineID)
			if len(runs) > 0 {
				return runs
			}
		}
	}

	return nil
}

func (g *Gateway) dispatchCLI(ctx context.Context, runID string, argv []string) *protocol.CLIResponse {
	if len(argv) == 0 {
		return &protocol.CLIResponse{Text: "no verb provided", ExitCode: 1}
	}

	verb := argv[0]
	args := argv[1:]

	switch verb {
	case "report":
		return g.cliReport(ctx, runID, args)
	case "show":
		return g.cliShow(ctx, runID, args)
	case "reschedule":
		return g.cliReschedule(ctx, runID, args)
	case "finish":
		return g.cliFinish(ctx, runID, args)
	case "set-cron":
		return g.cliSetCron(ctx, runID, args)
	default:
		return &protocol.CLIResponse{
			Text:     fmt.Sprintf("unknown verb: %s", verb),
			ExitCode: 1,
		}
	}
}

func (g *Gateway) cliReport(ctx context.Context, runID string, args []string) *protocol.CLIResponse {
	message := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--message" && i+1 < len(args) {
			message = args[i+1]
			break
		}
	}

	if message == "" {
		return &protocol.CLIResponse{Text: "--message is required", ExitCode: 1}
	}

	// Update run with message
	g.store.ReportRun(ctx, runID, &protocol.ReportRequest{
		OK:      true,
		Message: message,
	})

	return &protocol.CLIResponse{Text: "reported", ExitCode: 0}
}

func (g *Gateway) cliShow(ctx context.Context, runID string, args []string) *protocol.CLIResponse {
	// Get run details
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		return &protocol.CLIResponse{Text: "run not found", ExitCode: 1}
	}

	// Get loop details
	loop, err := g.store.GetLoop(ctx, run.LoopID)
	if err != nil {
		return &protocol.CLIResponse{Text: "loop not found", ExitCode: 1}
	}

	text := fmt.Sprintf("Loop: %s\nStatus: %s\nOutcome: %s",
		loop.Name, run.Status, run.Outcome)

	return &protocol.CLIResponse{Text: text, ExitCode: 0}
}

func (g *Gateway) cliReschedule(ctx context.Context, runID string, args []string) *protocol.CLIResponse {
	delay := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--delay" && i+1 < len(args) {
			delay = args[i+1]
			break
		}
	}

	if delay == "" {
		return &protocol.CLIResponse{Text: "--delay is required", ExitCode: 1}
	}

	// In production: create new pending run with delay
	return &protocol.CLIResponse{Text: "rescheduled for " + delay, ExitCode: 0}
}

func (g *Gateway) cliFinish(ctx context.Context, runID string, args []string) *protocol.CLIResponse {
	message := ""
	reason := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--message" && i+1 < len(args) {
			message = args[i+1]
			i++
		}
		if args[i] == "--reason" && i+1 < len(args) {
			reason = args[i+1]
			i++
		}
	}

	// Get run and loop
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		return &protocol.CLIResponse{Text: "run not found", ExitCode: 1}
	}

	loop, err := g.store.GetLoop(ctx, run.LoopID)
	if err != nil {
		return &protocol.CLIResponse{Text: "loop not found", ExitCode: 1}
	}

	// Permission check: only exec runs on closed loops can finish
	if run.Role != protocol.RoleExec {
		return &protocol.CLIResponse{
			Text:     fmt.Sprintf("finish only available for exec runs — this run is %q", run.Role),
			ExitCode: 1,
		}
	}

	if loop.Goal == "" {
		return &protocol.CLIResponse{
			Text:     "this loop has no goal to finish (it's an open/monitor loop)",
			ExitCode: 1,
		}
	}

	// Use the completion reason or message
	if reason == "" {
		reason = message
	}

	// Finish the loop (sets completed_at + enabled=false)
	if err := g.store.FinishLoop(ctx, run.LoopID, reason); err != nil {
		return &protocol.CLIResponse{Text: err.Error(), ExitCode: 1}
	}

	return &protocol.CLIResponse{
		Text:     fmt.Sprintf("loop finished: goal met — %s", reason),
		ExitCode: 0,
	}
}

func (g *Gateway) cliSetCron(ctx context.Context, runID string, args []string) *protocol.CLIResponse {
	cron := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--cron" && i+1 < len(args) {
			cron = args[i+1]
			break
		}
	}

	if cron == "" {
		return &protocol.CLIResponse{Text: "--cron is required", ExitCode: 1}
	}

	// Get run and loop
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		return &protocol.CLIResponse{Text: "run not found", ExitCode: 1}
	}

	// Update loop cron
	g.store.UpdateLoop(ctx, run.LoopID, map[string]interface{}{
		"cron": cron,
	})

	return &protocol.CLIResponse{Text: "cron updated to: " + cron, ExitCode: 0}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func loopToResponse(loop *store.Loop) protocol.LoopResponse {
	resp := protocol.LoopResponse{
		ID:           loop.ID,
		Name:         loop.Name,
		MachineID:    loop.MachineID,
		Cron:         loop.Cron,
		Timezone:     loop.Timezone,
		Workdir:      loop.Workdir,
		TaskFile:     loop.TaskFile,
		Workflow:     loop.Workflow,
		Model:        loop.Model,
		Agent:        loop.Agent,
		Notify:       loop.Notify,
		Enabled:      loop.Enabled,
		Goal:         loop.Goal,
		AllowControl: loop.AllowControl,
		State:        loop.State,
		CreatedAt:    loop.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    loop.UpdatedAt.Format(time.RFC3339),
	}
	if loop.CompletedAt != nil {
		// Add completed fields to response (would need to update protocol.LoopResponse)
		// For now, we can add to State or skip
	}
	return resp
}

func runToResponse(run *store.Run) protocol.RunResponse {
	resp := protocol.RunResponse{
		ID:        run.ID,
		LoopID:    run.LoopID,
		Role:      string(run.Role),
		Status:    run.Status,
		Outcome:   run.Outcome,
		Message:   run.Message,
		Duration:  run.Duration,
		StartedAt: run.StartedAt.Format(time.RFC3339),
		CreatedAt: run.CreatedAt.Format(time.RFC3339),
	}

	// Token stats - always return fields even if zero
	resp.CostInputTokens = run.InputTokens
	resp.CostOutputTokens = run.OutputTokens
	resp.CostCacheReadTokens = run.CacheTokens
	resp.CostUsd = run.Cost

	if run.EndedAt != nil {
		resp.EndedAt = run.EndedAt.Format(time.RFC3339)
	}

	resp.LoopName = run.LoopName
	resp.SessionId = run.SessionID
	resp.Error = run.Error
	
	return resp
}

// HTTPServer wraps the gateway with routing.
type HTTPServer struct {
	gateway *Gateway
	server  *http.Server
}

// NewHTTPServer creates an HTTP server.
func NewHTTPServer(addr string, store *store.Store, tokenGen *token.Generator) *HTTPServer {
	gateway := NewGateway(store, tokenGen)

	mux := http.NewServeMux()

	// Machine gateway (daemon API)
	mux.HandleFunc("/api/machine/poll", gateway.HandlePoll)
	mux.HandleFunc("/machine/report", gateway.HandleReport)
	mux.HandleFunc("/api/machine/cli", gateway.HandleCLI)
	mux.HandleFunc("/api/machine/sync", gateway.HandleSync)

	// Owner API
	mux.HandleFunc("/api/loops", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			gateway.HandleListLoops(w, r)
		} else if r.Method == "POST" {
			gateway.HandleCreateLoop(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/loops/", func(w http.ResponseWriter, r *http.Request) {
		// Check if it's /api/loops/{id}/runs
		if strings.HasSuffix(r.URL.Path, "/runs") {
			if r.Method == "GET" {
				gateway.HandleListRuns(w, r)
			} else if r.Method == "POST" {
				gateway.HandleTriggerRun(w, r)
			} else {
				http.Error(w, "method not allowed", 405)
			}
		} else {
			gateway.HandleLoopOps(w, r)
		}
	})

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	// Stats API
	mux.HandleFunc("/api/stats", gateway.HandleStats)

	// Runs API
	mux.HandleFunc("/api/runs", gateway.HandleListAllRuns)
	mux.HandleFunc("/api/runs/", func(w http.ResponseWriter, r *http.Request) {
		// Check if it's a log request: /api/runs/{id}/log
		path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
		if strings.HasSuffix(path, "/log") {
			gateway.HandleGetRunLog(w, r)
		} else if strings.HasSuffix(path, "/artifacts") {
			gateway.HandleListArtifacts(w, r)
		} else if strings.Contains(path, "/artifacts/") {
			gateway.HandleGetArtifact(w, r)
		} else if r.Method == "DELETE" {
			gateway.HandleDeleteRun(w, r)
		} else {
			gateway.HandleGetRun(w, r)
		}
	})

	// Web UI
	mux.Handle("/", http.FileServer(http.Dir("web")))

	return &HTTPServer{
		gateway: gateway,
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start starts the HTTP server.
func (s *HTTPServer) Start() error {
	log.Printf("Loopany server listening on %s", s.server.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// ============================================================
// Log and Artifact APIs
// ============================================================

// HandleGetRunLog handles GET /api/runs/{id}/log
func (g *Gateway) HandleGetRunLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Extract run ID from path: /api/runs/{id}/log
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	runID := strings.TrimSuffix(path, "/log")

	// Get run details to find session_id
	ctx := r.Context()
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		http.Error(w, "run not found", 404)
		return
	}

	// If no session_id, log file doesn't exist
	if run.SessionID == "" {
		http.Error(w, "no session for this run", 404)
		return
	}

	// Search for log file containing this session_id
	logDir := "/tmp/loopany-exec"
	entries, err := os.ReadDir(logDir)
	if err != nil {
		http.Error(w, "log directory not found", 500)
		return
	}

	var logPath string
	var data []byte

	// Search through all log files for the session_id
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}

		filePath := filepath.Join(logDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		// Check if this log file contains the session_id
		if strings.Contains(string(content), run.SessionID) {
			logPath = filePath
			data = content
			break
		}
	}

	if logPath == "" {
		http.Error(w, "log not found", 404)
		return
	}

	// Parse log lines
	lines := strings.Split(string(data), "\n")
	type LogEntry struct {
		Time    string `json:"time"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}

	entries_arr := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			entries_arr = append(entries_arr, entry)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"run_id":     runID,
		"session_id": run.SessionID,
		"path":       logPath,
		"lines":      entries_arr,
	})
}

// HandleListArtifacts handles GET /api/runs/{id}/artifacts
func (g *Gateway) HandleListArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Extract run ID and get run details
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	runID := strings.TrimSuffix(path, "/artifacts")

	ctx := r.Context()
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		http.Error(w, "run not found", 404)
		return
	}

	// Get loop to find workdir
	loop, err := g.store.GetLoop(ctx, run.LoopID)
	if err != nil {
		http.Error(w, "loop not found", 404)
		return
	}

	// List files in workdir (excluding hidden files)
	files := []map[string]interface{}{}
	entries, err := os.ReadDir(loop.Workdir)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"run_id": runID,
				"workdir": loop.Workdir,
				"files": files,
			})
			return
		}
		http.Error(w, "failed to read workdir", 500)
		return
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, map[string]interface{}{
			"name":     entry.Name(),
			"is_dir":   entry.IsDir(),
			"size":     info.Size(),
			"modified": info.ModTime().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"run_id":  runID,
		"workdir": loop.Workdir,
		"files":   files,
	})
}

// HandleGetArtifact handles GET /api/runs/{id}/artifacts/{filename}
func (g *Gateway) HandleGetArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Extract run ID and filename: /api/runs/{id}/artifacts/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.SplitN(path, "/artifacts/", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid path", 400)
		return
	}
	runID := parts[0]
	filename := parts[1]

	ctx := r.Context()
	run, err := g.store.GetRun(ctx, runID)
	if err != nil {
		http.Error(w, "run not found", 404)
		return
	}

	// Get loop to find workdir
	loop, err := g.store.GetLoop(ctx, run.LoopID)
	if err != nil {
		http.Error(w, "loop not found", 404)
		return
	}

	// Read file
	filePath := filepath.Join(loop.Workdir, filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", 404)
			return
		}
		http.Error(w, "failed to read file", 500)
		return
	}

	// Determine content type
	contentType := http.DetectContentType(data)

	// Check if it's a text file
	isText := strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "application/javascript") ||
		strings.Contains(contentType, "application/xml")

	if r.URL.Query().Get("download") == "true" {
		// Force download
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
		w.Write(data)
	} else if isText {
		// Return as JSON with content preview
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":        filename,
			"size":        len(data),
			"content_type": contentType,
			"content":      string(data),
		})
	} else {
		// Return binary data
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

// HandleDeleteRun handles DELETE /api/runs/{id}
func (g *Gateway) HandleDeleteRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "method not allowed", 405)
		return
	}

	runID := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if runID == "" || runID == "/" {
		http.Error(w, "run id required", 400)
		return
	}

	ctx := r.Context()

	// Delete run tokens first
	_, err := g.store.DB().ExecContext(ctx, "DELETE FROM run_tokens WHERE run_id = $1", runID)
	if err != nil {
		http.Error(w, "failed to delete run tokens", 500)
		return
	}

	// Delete run
	_, err = g.store.DB().ExecContext(ctx, "DELETE FROM runs WHERE id = $1", runID)
	if err != nil {
		http.Error(w, "failed to delete run", 500)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}