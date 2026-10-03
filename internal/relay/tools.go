package relay

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

// Tools is an MCP server of the service's own, offered to the calls made
// with a context that carries it (WithTools). Claude Code starts it for the
// call and stops it after. Only a web stage takes it: the others have no
// tools at all, and that does not change.
type Tools struct {
	// Name is the server's name. Claude Code calls its tools
	// mcp__<Name>__<tool>.
	Name string

	// Command and Args start the server, and Env is set for it.
	Command string
	Args    []string
	Env     map[string]string

	// Allowed are the tools the call is approved for, without the prefix: a
	// headless call has nobody to ask.
	Allowed []string

	// LogFlag, when set, is a flag the server takes a file to write its
	// calls to. The relay gives it a file beside the call's request and
	// reply, so what the tools were asked is kept with the rest.
	LogFlag string
}

type toolsKey struct{}

// WithTools returns a context whose calls are offered the tools.
func WithTools(ctx context.Context, t Tools) context.Context {
	return context.WithValue(ctx, toolsKey{}, t)
}

func toolsFrom(ctx context.Context) (Tools, bool) {
	t, ok := ctx.Value(toolsKey{}).(Tools)
	return t, ok && t.Name != "" && t.Command != ""
}

// ToolsLog is where a call's tool server writes what it was asked.
func (q Question) ToolsLog() string { return q.Base + "-tools.txt" }

// mcpConfig writes the server's configuration to a file in the run's
// directory for --mcp-config, and returns its path and the tools to approve.
// A file rather than an argument, because Env can hold what should not sit
// on a command line.
func (t Tools) mcpConfig(q Question) (path string, allowed []string, err error) {
	args := append([]string(nil), t.Args...)
	if t.LogFlag != "" {
		args = append(args, t.LogFlag, q.ToolsLog())
	}
	server := map[string]any{"type": "stdio", "command": t.Command, "args": args}
	if len(t.Env) > 0 {
		server["env"] = t.Env
	}
	data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{t.Name: server}})
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(q.Dir, "mcp-*.json")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	for _, name := range t.Allowed {
		allowed = append(allowed, "mcp__"+t.Name+"__"+name)
	}
	return f.Name(), allowed, nil
}

// allowList joins the built-in tools and a server's into the one list
// --allowedTools takes.
func allowList(builtIn string, more []string) string {
	if len(more) == 0 {
		return builtIn
	}
	return builtIn + "," + strings.Join(more, ",")
}
