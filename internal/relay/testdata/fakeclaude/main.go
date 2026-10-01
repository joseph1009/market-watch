// Command fakeclaude stands in for Claude Code in the relay's tests. It records
// what it was given -- arguments, system prompt, standard input, and whether an
// API key reached it -- and answers as FAKE_CLAUDE_MODE says.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("9.9.9 (Claude Code)")
		return
	}

	var modelName, system string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--model":
			modelName = args[i+1]
		case "--system-prompt-file":
			b, _ := os.ReadFile(args[i+1])
			system = string(b)
		}
	}
	stdin, _ := io.ReadAll(os.Stdin)
	wd, _ := os.Getwd()

	if path := os.Getenv("FAKE_CLAUDE_RECORD"); path != "" {
		record, _ := json.Marshal(map[string]any{
			"args":       args,
			"model":      modelName,
			"system":     system,
			"stdin":      string(stdin),
			"dir":        wd,
			"api_key":    os.Getenv("ANTHROPIC_API_KEY") != "",
			"auth_token": os.Getenv("ANTHROPIC_AUTH_TOKEN") != "",
		})
		_ = os.WriteFile(path, record, 0o644)
	}

	// The stream opens as Claude Code's does, with the plan's limits.
	fmt.Println(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790849400,"rateLimitType":"five_hour","overageStatus":"rejected","unifiedWindows":{"five_hour":{"utilization":0.43,"resetsAt":1790849400},"seven_day":{"utilization":0.21,"resetsAt":1791345600}}}}`)
	fmt.Println(`{"type":"system","subtype":"init"}`)

	switch os.Getenv("FAKE_CLAUDE_MODE") {
	case "error":
		fmt.Print(`{"type":"result","result":"Not logged in · Please run /login","is_error":true,"subtype":"error_during_execution"}`)
		os.Exit(1)
	case "limit":
		// A run stopped by the plan's limit: the reason is in the result, but
		// the run still calls itself a success and exits 1.
		fmt.Print(`{"type":"result","result":"You've hit your session limit · resets 2:40am","is_error":false,"subtype":"success"}`)
		os.Exit(1)
	case "crash":
		fmt.Fprint(os.Stderr, "unknown option --frobnicate")
		os.Exit(2)
	case "hang":
		time.Sleep(30 * time.Second)
	default:
		out, _ := json.Marshal(map[string]any{
			"type":     "result",
			"result":   "answered: " + string(stdin),
			"is_error": false,
			"subtype":  "success",
			"usage":    map[string]any{"input_tokens": 120, "output_tokens": 30},
			"modelUsage": map[string]any{
				"claude-haiku-4-5-20251001": map[string]any{},
			},
		})
		fmt.Print(string(out))
	}
}
