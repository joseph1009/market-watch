package relay

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/runcache"
)

var accountTools = Tools{
	Name:    "accounts",
	Command: "/app/market-watch",
	Args:    []string{"-mcp-accounts", "723125"},
	Env:     map[string]string{"USER_AGENT": "someone@example.com"},
	Allowed: []string{"find_concepts", "read_concept", "compute"},
	LogFlag: "-tools-log",
}

// The analysis is offered the service's own tools as an MCP server, given
// in a file and approved by name, and what they were asked is kept beside
// the call.
func TestAWebStageIsOfferedTheServicesTools(t *testing.T) {
	bin := fakeClaude(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	t.Setenv("FAKE_CLAUDE_RECORD", rec)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}
	cache := &runcache.Cache{Root: t.TempDir()}
	ctx, entry := cache.Start(context.Background(), runcache.Analysis, "MU")
	ctx, run, _ := r.Begin(ctx, "analysis-MU")
	ctx = WithTools(ctx, accountTools)

	if _, err := r.Stage(Analysis).Complete(ctx, "You analyse.", "MU's accounts"); err != nil {
		t.Fatal(err)
	}
	got := recorded(t, rec)
	joined := strings.Join(got.Args, " ")
	for _, want := range []string{
		"--tools WebSearch,WebFetch",
		"--allowedTools WebSearch,WebFetch,mcp__accounts__find_concepts,mcp__accounts__read_concept,mcp__accounts__compute",
		"--strict-mcp-config", "--mcp-config ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %v", want, got.Args)
		}
	}
	if strings.Contains(joined, "someone@example.com") {
		t.Error("the server's environment was put on the command line")
	}
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got.MCPConfig), &config); err != nil {
		t.Fatalf("config %q: %v", got.MCPConfig, err)
	}
	server := config.MCPServers["accounts"]
	if server.Command != "/app/market-watch" || server.Type != "stdio" || server.Env["USER_AGENT"] != "someone@example.com" ||
		len(server.Args) != 4 || server.Args[2] != "-tools-log" || !strings.HasSuffix(server.Args[3], "01-analysis-tools.txt") {
		t.Errorf("server = %+v", server)
	}
	if left, _ := filepath.Glob(filepath.Join(run.Dir, "mcp-*.json")); len(left) != 0 {
		t.Errorf("the config file was left behind: %v", left)
	}
	if ledger := read(t, filepath.Join(run.Dir, "ledger.md")); !strings.Contains(ledger, "analysis used its tools 1 times") {
		t.Errorf("the ledger does not say the tools were used:\n%s", ledger)
	}
	entry.Finish(nil)
	if log := read(t, filepath.Join(cache.Root, runcache.Analysis, "model", "01-analysis-tools.txt")); !strings.Contains(log, "compute 1 + 1 = 2") {
		t.Errorf("the tools' log was not kept in the cache: %q", log)
	}
}

// A stage with no tools gets none, whatever its context carries.
func TestAStageWithoutToolsIsNotOfferedTheServer(t *testing.T) {
	bin := fakeClaude(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	t.Setenv("FAKE_CLAUDE_RECORD", rec)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}
	ctx := WithTools(context.Background(), accountTools)
	if _, _, err := r.Plain(Triage).Complete(ctx, "You rate news.", "1. Oil jumps"); err != nil {
		t.Fatal(err)
	}
	got := recorded(t, rec)
	if joined := strings.Join(got.Args, " "); strings.Contains(joined, "--mcp-config") || !strings.Contains(joined, "--disallowedTools *") {
		t.Errorf("sorting was offered tools: %v", got.Args)
	}
}
