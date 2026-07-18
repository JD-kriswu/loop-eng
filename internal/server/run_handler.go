// Package server provides HTTP handlers for run-related endpoints.
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleRunOps handles /api/runs/{id} and /api/runs/{id}/log
func (s *Server) handleRunOps(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/runs/"), "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "run ID required", 400)
		return
	}

	runID := parts[0]

	// GET /api/runs/{id}/log - Get execution log
	if len(parts) > 1 && parts[1] == "log" {
		s.handleRunLog(w, r, runID)
		return
	}

	// GET /api/runs/{id} - Get run details
	if r.Method == "GET" {
		run, err := s.store.GetRun(r.Context(), runID)
		if err != nil {
			http.Error(w, "run not found", 404)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(run)
		return
	}

	http.Error(w, "method not allowed", 405)
}

// handleRunLog returns the execution log file content
func (s *Server) handleRunLog(w http.ResponseWriter, r *http.Request, runID string) {
	// Log directory
	logDir := "/tmp/loopany-exec"
	logFile := filepath.Join(logDir, runID+".log")

	// Check if file exists
	info, err := os.Stat(logFile)
	if err != nil {
		// Log file doesn't exist yet, return empty
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"run_id": runID,
			"exists": false,
			"message": "Log file not found. The run may not have started yet.",
		})
		return
	}

	// Open and read file
	file, err := os.Open(logFile)
	if err != nil {
		http.Error(w, "failed to open log file", 500)
		return
	}
	defer file.Close()

	// Parse log lines
	var steps []map[string]interface{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var step map[string]interface{}
		if err := json.Unmarshal([]byte(line), &step); err == nil {
			steps = append(steps, step)
		}
	}

	// Return response
	response := map[string]interface{}{
		"run_id":     runID,
		"exists":     true,
		"file_size":  info.Size(),
		"modified":   info.ModTime().Format("2006-01-02 15:04:05"),
		"step_count": len(steps),
		"steps":      steps,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleRunStream streams the log file in real-time (for future use)
func (s *Server) handleRunStream(w http.ResponseWriter, r *http.Request, runID string) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	logDir := "/tmp/loopany-exec"
	logFile := filepath.Join(logDir, runID+".log")

	// Wait for file to exist
	for i := 0; i < 30; i++ { // Wait up to 30 seconds
		if _, err := os.Stat(logFile); err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Open file
	file, err := os.Open(logFile)
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", `{"error": "Log file not found"}`)
		return
	}
	defer file.Close()

	// Stream file
	reader := bufio.NewReader(file)
	for {
		select {
		case <-r.Context().Done():
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					// Wait for more data
					time.Sleep(500 * time.Millisecond)
					continue
				}
				break
			}

			if line != "" {
				fmt.Fprintf(w, "data: %s\n\n", strings.TrimSpace(line))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	}
}