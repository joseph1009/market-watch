package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func echo() Tool {
	return Tool{
		Name:        "echo",
		Description: "Says it back.",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		Call: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct{ Text string }
			_ = json.Unmarshal(args, &in)
			if in.Text == "" {
				return "", errors.New("nothing to say back")
			}
			return in.Text, nil
		},
	}
}

// A session as Claude Code holds one: the handshake, the list of tools, a
// call that works, one that fails, and a tool that does not exist. A
// notification gets no answer.
func TestASessionIsAnswered(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"shell","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"six","method":"ping"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(in), &out, "accounts", "1", []Tool{echo()}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("%d answers, want 6, the notification unanswered:\n%s", len(lines), out.String())
	}
	for i, want := range []string{
		`"id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2025-06-18","serverInfo":{"name":"accounts","version":"1"}}`,
		`"name":"echo"`,
		`"id":3,"result":{"content":[{"text":"hello","type":"text"}],"isError":false}`,
		`"id":4,"result":{"content":[{"text":"nothing to say back","type":"text"}],"isError":true}`,
		`"id":5,"error":{"code":-32602,"message":"no such tool: shell"}`,
		`"id":"six","result":{}`,
	} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("answer %d = %s, want %s", i+1, lines[i], want)
		}
	}
}
