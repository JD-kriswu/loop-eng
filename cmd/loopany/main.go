// Package main is the CLI callback entry point (invoked by agent during run).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	verb := os.Args[1]
	args := os.Args[2:]

	// Run token is injected via environment
	runToken := os.Getenv("LOOPANY_RUN_TOKEN")
	serverURL := os.Getenv("LOOPANY_SERVER_URL")

	if runToken == "" || serverURL == "" {
		fmt.Fprintln(os.Stderr, "loopany: not running inside a loop (missing LOOPANY_RUN_TOKEN)")
		os.Exit(2)
	}

	switch verb {
	case "report":
		doReport(serverURL, runToken, args)
	case "show":
		doShow(serverURL, runToken, args)
	case "reschedule":
		doReschedule(serverURL, runToken, args)
	case "finish":
		doFinish(serverURL, runToken, args)
	case "set-cron":
		doSetCron(serverURL, runToken, args)
	case "version":
		fmt.Println(version)
	case "help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "loopany: unknown verb: %s\n", verb)
		printHelp()
		os.Exit(1)
	}
}

func doReport(serverURL, runToken string, args []string) {
	var message string
	var silent bool

	for i := 0; i < len(args); i++ {
		if args[i] == "--message" && i+1 < len(args) {
			message = args[i+1]
			i++
		} else if args[i] == "--silent" {
			silent = true
		}
	}

	if message == "" && !silent {
		fmt.Fprintln(os.Stderr, "loopany report: --message is required (or --silent)")
		os.Exit(1)
	}

	argv := []string{"report"}
	if message != "" {
		argv = append(argv, "--message", message)
	}
	if silent {
		argv = append(argv, "--silent")
	}

	resp, err := postCLI(serverURL, runToken, argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loopany: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

func doShow(serverURL, runToken string, args []string) {
	var what string
	if len(args) > 0 {
		what = args[0]
	}

	argv := []string{"show"}
	if what != "" {
		argv = append(argv, what)
	}

	resp, err := postCLI(serverURL, runToken, argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loopany: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

func doReschedule(serverURL, runToken string, args []string) {
	var delay string
	for i := 0; i < len(args); i++ {
		if args[i] == "--delay" && i+1 < len(args) {
			delay = args[i+1]
			i++
		}
	}

	if delay == "" {
		fmt.Fprintln(os.Stderr, "loopany reschedule: --delay is required")
		os.Exit(1)
	}

	resp, err := postCLI(serverURL, runToken, []string{"reschedule", "--delay", delay})
	if err != nil {
		fmt.Fprintf(os.Stderr, "loopany: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

func doFinish(serverURL, runToken string, args []string) {
	var reason string
	for i := 0; i < len(args); i++ {
		if args[i] == "--reason" && i+1 < len(args) {
			reason = args[i+1]
			i++
		}
	}

	argv := []string{"finish"}
	if reason != "" {
		argv = append(argv, "--reason", reason)
	}

	resp, err := postCLI(serverURL, runToken, argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loopany: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

func doSetCron(serverURL, runToken string, args []string) {
	var cron string
	for i := 0; i < len(args); i++ {
		if args[i] == "--cron" && i+1 < len(args) {
			cron = args[i+1]
			i++
		}
	}

	if cron == "" {
		fmt.Fprintln(os.Stderr, "loopany set-cron: --cron is required")
		os.Exit(1)
	}

	resp, err := postCLI(serverURL, runToken, []string{"set-cron", "--cron", cron})
	if err != nil {
		fmt.Fprintf(os.Stderr, "loopany: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

func postCLI(serverURL, runToken string, argv []string) (map[string]interface{}, error) {
	reqBody := map[string]interface{}{
		"argv": argv,
	}

	jsonBody, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", serverURL+"/api/machine/cli", strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+runToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	return result, nil
}

func printResponse(resp map[string]interface{}) {
	if text, ok := resp["text"].(string); ok {
		fmt.Println(text)
	}
	if exitCode, ok := resp["exitCode"].(float64); ok && exitCode != 0 {
		os.Exit(int(exitCode))
	}
}

func printHelp() {
	fmt.Println("Loopany - Agent callback CLI (invoked inside a run)")
	fmt.Println()
	fmt.Println("Usage: loopany <verb> [options]")
	fmt.Println()
	fmt.Println("Verbs:")
	fmt.Println("  report --message <msg> [--silent]    Report run progress/result")
	fmt.Println("  show [what]                          Show loop state/config")
	fmt.Println("  reschedule --delay <duration>        Reschedule next run")
	fmt.Println("  finish --reason <reason>             Mark closed loop complete")
	fmt.Println("  set-cron --cron <expr>               Update loop schedule")
	fmt.Println("  version                              Show version")
	fmt.Println()
	fmt.Println("Environment (injected by daemon):")
	fmt.Println("  LOOPANY_RUN_TOKEN    Run authentication token")
	fmt.Println("  LOOPANY_SERVER_URL   Server URL")
}