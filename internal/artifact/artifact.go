// Package artifact parses Claude session transcripts to extract file changes.
package artifact

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourorg/loopany-go/internal/protocol"
)

const (
	StepTextMax = 1500
	MaxSteps    = 80
)

// Parser extracts artifacts and transcripts from Claude sessions.
type Parser struct{}

// ParseResult contains extracted artifacts and transcript.
type ParseResult struct {
	Artifacts  []protocol.RunArtifact
	Transcript []protocol.TranscriptStep
}

// NewParser creates a transcript parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseSession reads a Claude session JSONL and extracts artifacts.
func (p *Parser) ParseSession(sessionID, workdir string) (*ParseResult, error) {
	// Find transcript file
	transcriptPath := p.findTranscript(sessionID)
	if transcriptPath == "" {
		return &ParseResult{}, nil
	}

	file, err := os.Open(transcriptPath)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	result := &ParseResult{
		Artifacts:  []protocol.RunArtifact{},
		Transcript: []protocol.TranscriptStep{},
	}

	kinds := make(map[string]protocol.ArtifactKind)
	writePending := make(map[string]string) // tool_use_id -> file_path

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		lineBytes := scanner.Bytes()

		var line map[string]json.RawMessage
		if err := json.Unmarshal(lineBytes, &line); err != nil {
			continue
		}

		var lineType string
		json.Unmarshal(line["type"], &lineType)

		switch lineType {
		case "assistant":
			p.parseAssistantLine(line, kinds, writePending, result, workdir)
		case "user":
			p.parseUserLine(line, writePending, kinds, result)
		}
	}

	// Convert kinds to artifacts
	for path, kind := range kinds {
		result.Artifacts = append(result.Artifacts, protocol.RunArtifact{
			Path: path,
			Kind: kind,
		})
	}

	return result, nil
}

// findTranscript locates the session JSONL file.
func (p *Parser) findTranscript(sessionID string) string {
	// Check CLAUDE_CONFIG_DIR override
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".claude")
	}

	projectsDir := filepath.Join(base, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	// Search across all project dirs
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, entry.Name(), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func (p *Parser) parseAssistantLine(line map[string]json.RawMessage, kinds map[string]protocol.ArtifactKind, writePending map[string]string, result *ParseResult, workdir string) {
	var message struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(line["message"], &message); err != nil {
		return
	}

	for _, content := range message.Content {
		var block struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
			Input struct {
				FilePath string `json:"file_path"`
			} `json:"input"`
		}
		if err := json.Unmarshal(content, &block); err != nil {
			continue
		}

		if block.Type == "tool_use" {
			// Add to transcript (cap length)
			if len(result.Transcript) < MaxSteps {
				result.Transcript = append(result.Transcript, protocol.TranscriptStep{
					Kind:  "tool",
					Name:  block.Name,
					Input: truncate(block.Input.FilePath, StepTextMax),
				})
			}

			// Track write operations
			if block.Name == "Write" && block.Input.FilePath != "" {
				relPath := p.relativePath(block.Input.FilePath, workdir)
				if relPath != "" {
					writePending[block.ID] = relPath
				}
			} else if p.isEditTool(block.Name) && block.Input.FilePath != "" {
				relPath := p.relativePath(block.Input.FilePath, workdir)
				if relPath != "" {
					kinds[relPath] = protocol.ArtifactEdited
				}
			}
		} else if block.Type == "text" {
			var textBlock struct {
				Text string `json:"text"`
			}
			json.Unmarshal(content, &textBlock)

			// Add to transcript (cap length)
			if len(result.Transcript) < MaxSteps {
				result.Transcript = append(result.Transcript, protocol.TranscriptStep{
					Kind: "text",
					Text: truncate(textBlock.Text, StepTextMax),
				})
			}
		}
	}
}

func (p *Parser) parseUserLine(line map[string]json.RawMessage, writePending map[string]string, kinds map[string]protocol.ArtifactKind, result *ParseResult) {
	var message struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(line["message"], &message); err != nil {
		return
	}

	for _, content := range message.Content {
		var block struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
		}
		if err := json.Unmarshal(content, &block); err != nil {
			continue
		}

		if block.Type == "tool_result" {
			// Check if this is a Write result (new file)
			if path, ok := writePending[block.ToolUseID]; ok {
				if strings.Contains(block.Content, "File created successfully") {
					kinds[path] = protocol.ArtifactCreated
				} else {
					// Was edited, not created
					if kinds[path] != protocol.ArtifactCreated {
						kinds[path] = protocol.ArtifactEdited
					}
				}
				delete(writePending, block.ToolUseID)
			}

			// Add to transcript (cap length)
			if len(result.Transcript) < MaxSteps {
				result.Transcript = append(result.Transcript, protocol.TranscriptStep{
					Kind: "result",
					Text: truncate(block.Content, StepTextMax),
				})
			}
		}
	}
}

func (p *Parser) isEditTool(name string) bool {
	editTools := map[string]bool{
		"Edit":         true,
		"MultiEdit":    true,
		"NotebookEdit": true,
	}
	return editTools[name]
}

func (p *Parser) relativePath(absPath, workdir string) string {
	rel, err := filepath.Rel(workdir, absPath)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(rel, "..") {
		return "" // Outside workdir
	}
	return rel
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}