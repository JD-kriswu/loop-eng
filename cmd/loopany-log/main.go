package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ExecutionStep struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}

func main() {
	logDir := "/tmp/loopany-exec"

	// List all logs
	if len(os.Args) == 1 || os.Args[1] == "list" {
		fmt.Println("📁 Execution Logs")
		fmt.Println("════════════════════════════════════════════════════════════")

		files, err := filepath.Glob(filepath.Join(logDir, "*.log"))
		if err != nil {
			log.Fatal(err)
		}

		if len(files) == 0 {
			fmt.Println("No execution logs found.")
			fmt.Println("\nUsage:")
			fmt.Println("  loopany-log list          - List all logs")
			fmt.Println("  loopany-log show <run-id> - Show specific log")
			fmt.Println("  loopany-log tail          - Tail most recent log")
			return
		}

		// Sort by modification time (newest first)
		for i := len(files) - 1; i >= 0; i-- {
			file := files[i]
			info, err := os.Stat(file)
			if err != nil {
				continue
			}

			runID := strings.TrimSuffix(filepath.Base(file), ".log")
			fmt.Printf("Run: %s\n", runID)
			fmt.Printf("  Time: %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
			fmt.Printf("  Path: %s\n", file)
			fmt.Println()
		}

		return
	}

	// Show specific log
	if os.Args[1] == "show" {
		if len(os.Args) < 3 {
			fmt.Println("Usage: loopany-log show <run-id>")
			os.Exit(1)
		}

		runID := os.Args[2]
		logFile := filepath.Join(logDir, runID+".log")

		showLog(logFile)
		return
	}

	// Tail most recent log
	if os.Args[1] == "tail" {
		files, err := filepath.Glob(filepath.Join(logDir, "*.log"))
		if err != nil {
			log.Fatal(err)
		}

		if len(files) == 0 {
			fmt.Println("No logs found")
			return
		}

		// Find most recent
		var recentFile string
		var recentTime time.Time
		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil {
				continue
			}
			if info.ModTime().After(recentTime) {
				recentTime = info.ModTime()
				recentFile = file
			}
		}

		if recentFile == "" {
			fmt.Println("No logs found")
			return
		}

		fmt.Printf("📡 Tailing: %s\n", recentFile)
		fmt.Println("════════════════════════════════════════════════════════════")

		showLog(recentFile)
		return
	}

	// Watch mode (follow new logs)
	if os.Args[1] == "watch" {
		fmt.Println("👀 Watching for new execution logs...")
		fmt.Println("Press Ctrl+C to stop")

		ticker := time.NewTicker(1 * time.Second)
		var lastFile string

		for range ticker.C {
			files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
			for _, file := range files {
				if file != lastFile {
					info, _ := os.Stat(file)
					if info.ModTime().After(time.Now().Add(-2 * time.Second)) {
						fmt.Printf("\n🆕 New log: %s\n", filepath.Base(file))
						showLog(file)
						lastFile = file
					}
				}
			}
		}
	}
}

func showLog(logFile string) {
	file, err := os.Open(logFile)
	if err != nil {
		fmt.Printf("Error opening log: %v\n", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		var step ExecutionStep
		if err := json.Unmarshal([]byte(line), &step); err != nil {
			fmt.Println(line)
			continue
		}

		// Color-coded output
		icon := getIcon(step.Type)
		timeStr := step.Time.Format("15:04:05.000")

		switch step.Type {
		case "cmd":
			fmt.Printf("%s [%s] 📋 %s\n", icon, timeStr, step.Message)
		case "tool_call":
			fmt.Printf("%s [%s] 🔧 %s\n", icon, timeStr, step.Message)
		case "tool_result":
			fmt.Printf("%s [%s] ✅ %s\n", icon, timeStr, step.Message)
		case "output":
			// Truncate long outputs
			msg := step.Message
			if len(msg) > 100 {
				msg = msg[:100] + "..."
			}
			fmt.Printf("   [%s] %s\n", timeStr, msg)
		case "error":
			fmt.Printf("%s [%s] ❌ %s\n", icon, timeStr, step.Message)
		case "status":
			fmt.Printf("%s [%s] 📍 %s\n", icon, timeStr, step.Message)
		default:
			fmt.Printf("%s [%s] %s\n", icon, timeStr, step.Message)
		}
	}
}

func getIcon(stepType string) string {
	switch stepType {
	case "start":
		return "🚀"
	case "end":
		return "🏁"
	case "cmd":
		return "📝"
	case "tool_call":
		return "🔧"
	case "tool_result":
		return "✅"
	case "output":
		return "📄"
	case "error":
		return "❌"
	case "status":
		return "📍"
	default:
		return "•"
	}
}