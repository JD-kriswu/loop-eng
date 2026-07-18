package prompts

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

//go:embed exec-core.md
var execCorePrompt string

// ExecCoreData holds template data for exec-core prompt.
type ExecCoreData struct {
	Name      string
	TaskFile  string
	GoalLine  string
	StateLine string
}

// BuildExecTask builds the exec run task prompt from the template.
func BuildExecTask(data ExecCoreData) string {
	tmpl, err := template.New("exec-core").Parse(execCorePrompt)
	if err != nil {
		// Fallback to simple format if template fails
		return buildSimpleExecTask(data)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return buildSimpleExecTask(data)
	}

	return strings.TrimSpace(buf.String())
}

func buildSimpleExecTask(data ExecCoreData) string {
	var parts []string

	parts = append(parts, "[loop run · "+data.Name+"]")
	parts = append(parts, "")
	parts = append(parts, "You are one scheduled run of a Loopany background loop. Run once to completion, then exit.")
	parts = append(parts, "")
	parts = append(parts, "Rules:")
	parts = append(parts, "- Read the task file first ("+data.TaskFile+")")
	parts = append(parts, "- Do the work described in the Spec")
	parts = append(parts, "- End with exactly ONE terminal call:")
	parts = append(parts, "  - `loopany report --status new --message \"<message>\"`")
	if data.GoalLine != "" {
		parts = append(parts, "  - `loopany finish --message \"<achieved>\" --reason \"<why>\"` when goal is met")
	}
	parts = append(parts, "")
	if data.GoalLine != "" {
		parts = append(parts, data.GoalLine)
		parts = append(parts, "")
	}
	parts = append(parts, "Run now.")

	return strings.Join(parts, "\n")
}