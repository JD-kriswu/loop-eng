// Package runner executes coding agents (claude, codex, grok).
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/loopany-go/internal/protocol"
)

const (
	DefaultTimeout         = 0 // no timeout by default
	TransientRetries       = 2
	TransientRetryBaseMs   = 15000
	SelfSchedulingTools    = "ScheduleWakeup,CronCreate,CronList,CronDelete"
)

// AgentType identifies which coding agent to use.
type AgentType string

const (
	AgentClaude AgentType = "claude-code"
	AgentCodex  AgentType = "codex"
	AgentGrok   AgentType = "grok"
)

// AgentSpawn defines how to spawn a coding agent.
type AgentSpawn struct {
	Bin  string
	Args []string
}

// ExecutionLogger tracks execution details for visibility
type ExecutionLogger struct {
	mu         sync.Mutex
	runID      string
	startTime  time.Time
	steps      []ExecutionStep
	currentCmd string
	outputFile *os.File
}

// ExecutionStep represents a single execution step
type ExecutionStep struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // "cmd", "output", "tool_call", "error"
	Message string    `json:"message"`
}

// NewExecutionLogger creates a new execution logger
func NewExecutionLogger(runID string) *ExecutionLogger {
	logger := &ExecutionLogger{
		runID:     runID,
		startTime: time.Now(),
		steps:     make([]ExecutionStep, 0),
	}

	// Create log file
	logDir := "/tmp/loopany-exec"
	os.MkdirAll(logDir, 0755)
	if f, err := os.Create(fmt.Sprintf("%s/%s.log", logDir, runID)); err == nil {
		logger.outputFile = f
	}

	return logger
}

// LogStep logs an execution step
func (l *ExecutionLogger) LogStep(stepType, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	step := ExecutionStep{
		Time:    time.Now(),
		Type:    stepType,
		Message: message,
	}
	l.steps = append(l.steps, step)

	elapsed := time.Since(l.startTime).Round(time.Millisecond)
	log.Printf("[RUN-%s][%s][%s] %s", l.runID, elapsed, stepType, message)

	if l.outputFile != nil {
		jsonStep, _ := json.Marshal(step)
		l.outputFile.WriteString(string(jsonStep) + "\n")
	}
}

// LogCommand logs command execution
func (l *ExecutionLogger) LogCommand(bin string, args []string) {
	cmd := fmt.Sprintf("%s %s", bin, strings.Join(args, " "))
	l.currentCmd = cmd
	l.LogStep("cmd", cmd)
}

// LogOutput logs output lines
func (l *ExecutionLogger) LogOutput(line string) {
	l.LogStep("output", line)
}

// LogToolCall logs tool call
func (l *ExecutionLogger) LogToolCall(toolName, input string) {
	l.LogStep("tool_call", fmt.Sprintf("%s: %s", toolName, input))
}

// LogError logs error
func (l *ExecutionLogger) LogError(err error) {
	l.LogStep("error", err.Error())
}

// Close closes the logger
func (l *ExecutionLogger) Close() {
	if l.outputFile != nil {
		l.outputFile.Close()
	}
}

// Runner executes coding agent runs.
type Runner struct {
	agent     AgentType
	model     string
	workdir   string
	timeout   time.Duration
	retries   int
	retryBase time.Duration
	logger    *ExecutionLogger
}

// NewRunner creates a new runner.
func NewRunner(agent AgentType, model, workdir string) *Runner {
	return &Runner{
		agent:     agent,
		model:     model,
		workdir:   workdir,
		timeout:   getTimeoutFromEnv(),
		retries:   TransientRetries,
		retryBase: TransientRetryBaseMs * time.Millisecond,
	}
}

// Run executes the agent with the given prompt.
func (r *Runner) Run(ctx context.Context, prompt string, resumeSessionID string) (*RunResult, error) {
	var lastErr error

	for attempt := 0; attempt <= r.retries; attempt++ {
		if attempt > 0 {
			// Backoff before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(r.retryBase * time.Duration(1<<attempt)):
			}
			log.Printf("Retrying agent run (attempt %d/%d)", attempt, r.retries)
		}

		spawn := r.buildSpawn(prompt, resumeSessionID)
		result, err := r.runOnce(ctx, spawn)
		if err == nil {
			return result, nil
		}

		if !isTransientError(err) {
			return nil, err
		}

		lastErr = err
		// Resume from session on retry
		if result != nil && result.SessionID != "" {
			resumeSessionID = result.SessionID
		}
	}

	return nil, lastErr
}

// RunResult is the result of a coding agent run.
type RunResult struct {
	ExitCode  int
	SessionID string
	Output    string
	Error     string
	Cost      *protocol.RunCost
	Duration  time.Duration
}

// runOnce executes one agent pass with streaming output.
func (r *Runner) runOnce(ctx context.Context, spawn AgentSpawn) (*RunResult, error) {
	start := time.Now()

	// Initialize logger
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	r.logger = NewExecutionLogger(runID)

	r.logger.LogStep("start", fmt.Sprintf("Agent execution starting in %s", r.workdir))
	r.logger.LogCommand(spawn.Bin, spawn.Args)

	cmd := exec.CommandContext(ctx, spawn.Bin, spawn.Args...)
	cmd.Dir = r.workdir

	// Build environment
	cmd.Env = r.buildEnv()
	r.logger.LogStep("env", fmt.Sprintf("Environment: %d vars", len(cmd.Env)))

	// Create pipes for real-time output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.logger.LogError(err)
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.logger.LogError(err)
		return nil, err
	}

	// Start command
	r.logger.LogStep("exec", "Starting agent process...")
	if err := cmd.Start(); err != nil {
		r.logger.LogError(err)
		return nil, err
	}

	// Stream output in real-time
	var outputBuilder strings.Builder
	var errorBuilder strings.Builder

	// Combined output channel
	outputChan := make(chan string, 100)
	done := make(chan struct{})

	// Reader goroutine for stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			outputChan <- line
			outputBuilder.WriteString(line + "\n")
		}
	}()

	// Reader goroutine for stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			outputChan <- "STDERR: " + line
			errorBuilder.WriteString(line + "\n")
		}
	}()

	// Output processor goroutine
	go func() {
		defer close(done)
		for line := range outputChan {
			r.logger.LogOutput(line)

			// Try to parse as JSON for tool calls
			if strings.HasPrefix(strings.TrimSpace(line), "{") {
				var obj map[string]interface{}
				if err := json.Unmarshal([]byte(line), &obj); err == nil {
					// Check for tool use
					if toolUse, ok := obj["tool_use"].(map[string]interface{}); ok {
						if name, ok := toolUse["name"].(string); ok {
							input, _ := json.Marshal(toolUse["input"])
							r.logger.LogToolCall(name, string(input))
						}
					}
					// Check for tool_result
					if toolResult, ok := obj["tool_result"].(map[string]interface{}); ok {
						if toolUseID, ok := toolResult["tool_use_id"].(string); ok {
							r.logger.LogStep("tool_result", toolUseID)
						}
					}
				}
			}

			// Detect progress indicators
			if strings.Contains(line, "Thinking...") {
				r.logger.LogStep("status", "Agent thinking...")
			} else if strings.Contains(line, "Reading") {
				r.logger.LogStep("status", "Reading file...")
			} else if strings.Contains(line, "Editing") {
				r.logger.LogStep("status", "Editing file...")
			}
		}
	}()

	// Wait for command to complete
	waitErr := cmd.Wait()
	close(outputChan)

	duration := time.Since(start)

	result := &RunResult{
		Duration: duration,
		Output:   outputBuilder.String(),
	}

	// Wait for output processor to finish
	<-done

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		result.Error = errorBuilder.String()
		r.logger.LogError(waitErr)
		r.logger.LogStep("end", fmt.Sprintf("Execution FAILED after %s", duration))
		r.logger.Close()
		return result, waitErr
	}

	result.ExitCode = 0

	// Parse session ID and cost from output
	result.SessionID = parseSessionID(result.Output, r.agent)
	result.Cost = parseCost(result.Output, r.agent)

	r.logger.LogStep("end", fmt.Sprintf("Execution completed: exit=%d, session=%s, duration=%s",
		result.ExitCode, result.SessionID, duration))
	r.logger.Close()

	return result, nil
}

// buildSpawn creates the agent spawn command.
func (r *Runner) buildSpawn(prompt, resumeSessionID string) AgentSpawn {
	switch r.agent {
	case AgentCodex:
		args := []string{"exec"}
		if resumeSessionID != "" {
			args = append(args, "resume", resumeSessionID)
		}
		args = append(args,
			"--json",
			"--dangerously-bypass-approvals-and-sandbox",
			"--skip-git-repo-check",
		)
		if r.model != "" {
			args = append(args, "-m", r.model)
		}
		args = append(args, prompt)
		return AgentSpawn{
			Bin:  getBinPath("LOOPANY_CODEX_BIN", "codex"),
			Args: args,
		}

	case AgentGrok:
		args := []string{"-p", prompt}
		if resumeSessionID != "" {
			args = append(args, "--resume", resumeSessionID)
		}
		args = append(args,
			"--output-format", "streaming-json",
			"--permission-mode", "bypassPermissions",
			"--disallowed-tools", SelfSchedulingTools,
		)
		if r.model != "" {
			args = append(args, "--model", r.model)
		}
		return AgentSpawn{
			Bin:  getBinPath("LOOPANY_GROK_BIN", "grok"),
			Args: args,
		}

	default: // claude-code
		args := []string{"-p", prompt}
		if resumeSessionID != "" {
			args = append(args, "--resume", resumeSessionID)
		}
		args = append(args,
			"--output-format", "stream-json",
			"--verbose",
		)
		// Only use dangerously-skip-permissions for non-root users
		if os.Getuid() != 0 {
			args = append(args, "--dangerously-skip-permissions")
		}
		// Don't use bypassPermissions for root - causes error
		// --permission-mode bypassPermissions
		if r.model != "" {
			args = append(args, "--model", r.model)
		}
		return AgentSpawn{
			Bin:  getBinPath("LOOPANY_CLAUDE_BIN", "claude"),
			Args: args,
		}
	}
}

// buildEnv creates the allowlisted environment for the agent.
func (r *Runner) buildEnv() []string {
	// Allowlist of env vars to pass through
	allowlistPrefixes := []string{
		"HOME=",
		"PATH=",
		"USER=",
		"LANG=",
		"TERM=",
		"LOOPANY_",
		"CLAUDE_",
		"ANTHROPIC_",
		"NO_PROXY=",
		"no_proxy=",
	}

	// First, try to source bashrc for API credentials
	// This is needed when running as a daemon that doesn't inherit shell env
	if os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		// Try to get from bashrc
		if home := os.Getenv("HOME"); home != "" {
			execCmd := exec.Command("bash", "-c", "source "+home+"/.bashrc 2>/dev/null && env")
			if out, err := execCmd.Output(); err == nil {
				lines := strings.Split(string(out), "\n")
				for _, line := range lines {
					if strings.HasPrefix(line, "ANTHROPIC_") {
						parts := strings.SplitN(line, "=", 2)
						if len(parts) == 2 {
							os.Setenv(parts[0], parts[1])
						}
					}
				}
			}
		}
	}

	var env []string
	for _, e := range os.Environ() {
		for _, prefix := range allowlistPrefixes {
			if strings.HasPrefix(e, prefix) {
				env = append(env, e)
				break
			}
		}
	}

	// Add run token if available
	if runToken := os.Getenv("LOOPANY_RUN_TOKEN"); runToken != "" {
		env = append(env, "LOOPANY_RUN_TOKEN="+runToken)
	}
	if serverURL := os.Getenv("LOOPANY_SERVER_URL"); serverURL != "" {
		env = append(env, "LOOPANY_SERVER_URL="+serverURL)
	}

	return env
}

func getBinPath(envKey, defaultBin string) string {
	if bin := os.Getenv(envKey); bin != "" {
		return bin
	}
	return defaultBin
}

func getTimeoutFromEnv() time.Duration {
	ms := os.Getenv("LOOPANY_EXEC_TIMEOUT_MS")
	if ms == "" {
		return DefaultTimeout
	}
	var timeoutMs int
	fmt.Sscanf(ms, "%d", &timeoutMs)
	if timeoutMs <= 0 {
		return 0
	}
	return time.Duration(timeoutMs) * time.Millisecond
}

func isTransientError(err error) bool {
	// Check for network errors, 5xx, rate limits
	errStr := err.Error()
	return strings.Contains(errStr, "ECONNRESET") ||
		strings.Contains(errStr, "5xx") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "Connection closed") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "context deadline")
}

func parseSessionID(output string, agent AgentType) string {
	// Parse session ID from agent output (stream-json)
	// Look for "session_id" or "sessionId" field
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		// Check for session_id field
		if sessionID, ok := obj["session_id"].(string); ok {
			return sessionID
		}
		if sessionID, ok := obj["sessionId"].(string); ok {
			return sessionID
		}

		// Check nested in result
		if result, ok := obj["result"].(map[string]interface{}); ok {
			if sessionID, ok := result["session_id"].(string); ok {
				return sessionID
			}
		}
	}

	return ""
}

func parseCost(output string, agent AgentType) *protocol.RunCost {
	// Parse cost from agent output
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		// Initialize cost object
		cost := &protocol.RunCost{}

		// Extract USD if present
		if usd, ok := obj["total_cost_usd"].(float64); ok {
			cost.USD = usd
		}

		// Extract usage tokens if present
		if usage, ok := obj["usage"].(map[string]interface{}); ok {
			if v, ok := usage["input_tokens"].(float64); ok {
				cost.InputTokens = int64(v)
			}
			if v, ok := usage["output_tokens"].(float64); ok {
				cost.OutputTokens = int64(v)
			}
			if v, ok := usage["cache_read_input_tokens"].(float64); ok {
				cost.CacheReadTokens = int64(v)
			}
			if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
				cost.CacheCreationTokens = int64(v)
			}
		}

		// Check nested in result
		if result, ok := obj["result"].(map[string]interface{}); ok {
			if usd, ok := result["total_cost_usd"].(float64); ok {
				cost.USD = usd
			}
			if usage, ok := result["usage"].(map[string]interface{}); ok {
				if v, ok := usage["input_tokens"].(float64); ok {
					cost.InputTokens = int64(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					cost.OutputTokens = int64(v)
				}
				if v, ok := usage["cache_read_input_tokens"].(float64); ok {
					cost.CacheReadTokens = int64(v)
				}
				if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
					cost.CacheCreationTokens = int64(v)
				}
			}
		}

		// Return if we found any cost data
		if cost.USD > 0 || cost.InputTokens > 0 || cost.OutputTokens > 0 {
			return cost
		}
	}

	return nil
}

// StreamOutput writes execution output to an io.Writer in real-time
func (r *Runner) StreamOutput(w io.Writer) {
	// This would be called by daemon to stream output to a web UI or log aggregator
	// Implementation depends on daemon integration
}