// Package workflow implements the workflow gate (pure JS pre-processing).
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const DefaultTimeout = 30 * time.Second

// Result is the workflow execution result.
type Result struct {
	OK         bool
	Message    string
	State      interface{}
	AgentCalls []AgentCall
	Error      string
}

// AgentCall represents an escalation to coding agent.
type AgentCall struct {
	Message string
	Data    interface{}
}

// Runner executes workflow scripts.
type Runner struct {
	timeout time.Duration
}

// NewRunner creates a workflow runner.
func NewRunner() *Runner {
	return &Runner{
		timeout: getWorkflowTimeout(),
	}
}

// Run executes the workflow script with previous state.
func (r *Runner) Run(ctx context.Context, script string, prevState interface{}, workdir string) (*Result, error) {
	// Create temp directory for execution
	tmpDir, err := os.MkdirTemp("", "loopany-workflow-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Build the wrapper script
	wrapper := r.buildWrapper(script, prevState)
	scriptPath := filepath.Join(tmpDir, "workflow.mjs")
	outPath := filepath.Join(tmpDir, "out.json")

	if err := os.WriteFile(scriptPath, []byte(wrapper), 0644); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", scriptPath)
	cmd.Dir = workdir
	cmd.Env = []string{
		"LOOPANY_WORKFLOW_OUT=" + outPath,
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &Result{
			OK:    false,
			Error: fmt.Sprintf("workflow exited: %v\n%s", err, output),
		}, nil
	}

	// Check for timeout
	if ctx.Err() == context.DeadlineExceeded {
		return &Result{
			OK:    false,
			Error: fmt.Sprintf("workflow timed out (>%ds)", int(r.timeout.Seconds())),
		}, nil
	}

	// Read result
	resultJSON, err := os.ReadFile(outPath)
	if err != nil {
		return &Result{
			OK:    false,
			Error: "workflow did not write result",
		}, nil
	}

	var result Result
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return &Result{
			OK:    false,
			Error: fmt.Sprintf("parse result: %v", err),
		}, nil
	}

	return &result, nil
}

// buildWrapper creates the JS wrapper that injects prev and agent().
func (r *Runner) buildWrapper(body string, prevState interface{}) string {
	prevJSON, _ := json.Marshal(prevState)
	if prevJSON == nil {
		prevJSON = []byte("null")
	}

	return fmt.Sprintf(`
import { writeFileSync } from "node:fs";

const __OUT = process.env.LOOPANY_WORKFLOW_OUT;
const prev = %s;
const __agentCalls = [];

const agent = (message, data) => {
  __agentCalls.push(data === undefined ? { message } : { message, data });
};

const tools = {
  call: async (name, args) => {
    // MCP tool bridge would be injected here
    throw new Error("tools.call not implemented - requires MCP bridge");
  }
};

const __run = async (prev) => {
%s
};

__run(prev)
  .then((out) => {
    const result = typeof out === "string" ? { message: out } : (out ?? {});
    writeFileSync(__OUT, JSON.stringify({ ...result, agentCalls: __agentCalls }));
    process.exit(0);
  })
  .catch((e) => {
    console.error(e && e.stack ? e.stack : String(e));
    process.exit(1);
  });
`, string(prevJSON), body)
}

func getWorkflowTimeout() time.Duration {
	sec := os.Getenv("LOOPANY_WORKFLOW_TIMEOUT_SECONDS")
	if sec == "" {
		return DefaultTimeout
	}
	var timeoutSec int
	fmt.Sscanf(sec, "%d", &timeoutSec)
	if timeoutSec <= 0 {
		return DefaultTimeout
	}
	return time.Duration(timeoutSec) * time.Second
}