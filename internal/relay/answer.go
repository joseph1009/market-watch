package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Opus is the model every stage is answered by, named in full rather than by
// the "opus" alias so the version cannot change under the service without a
// deploy.
const Opus = "claude-opus-5-5"

// DefaultModels is which model answers each stage. Since 2026-10-01 that is
// Opus 5.5 throughout, at the owner's request: the sorting, the review, the
// names and the themes were Sonnet and Haiku before, chosen for speed over a
// lot of text. MODEL_<STAGE> still puts any stage on another model.
var DefaultModels = map[string]string{
	Triage:   Opus,
	Review:   Opus,
	Names:    Opus,
	Brief:    Opus,
	Themes:   Opus,
	Scout:    Opus,
	Research: Opus,
	Verdicts: Opus,
	Analysis: Opus,
	Industry: Opus,
}

// quickStages answer without extended thinking. Rating a headline or naming the
// company it is about is a mechanical judgment, and left to think, Haiku once spent
// sixty-five seconds and 5,900 tokens reasoning its way to fourteen one-line
// ratings that took it five seconds and ninety tokens without. The old API
// client turned thinking off for the same reason. The brief and the analysis
// keep it: deciding what a day meant, or what a set of accounts says, is the
// judgment it is for.
var quickStages = map[string]bool{Triage: true, Names: true, Review: true}

// noThinking is the settings override that turns thinking off for one call.
const noThinking = `{"alwaysThinkingEnabled":false}`

// webStages may search the web and read pages, and nothing else. The scout and
// the research are the stages whose job is to find what the market's numbers
// do not say: what is growing and by how much, who supplies whom, which part
// of an industry has been paid for. They still get no shell, no files and no
// connectors, so a page that tries to steer them can change an answer and
// nothing more.
//
// The review may search too, to learn what an unfamiliar company does when an
// article does not say and its section depends on it. And since 2026-10-01
// the verdicts and the analysis, at the owner's request: a verdict had rested
// on one article, and it now checks its case in a second, independent
// source; and an analysis looks for the last fortnight's news itself, so
// nothing important is missed. /industry searches for an industry's shape,
// which no feed carries.
var webStages = map[string]bool{Scout: true, Research: true, Review: true, Verdicts: true, Analysis: true, Industry: true}

// webTools are the tools a web stage is given, and pre-approved for, since a
// headless call has nobody to ask.
const webTools = "WebSearch,WebFetch"

// DefaultCallTimeout bounds one headless call. A brief from Opus takes a few
// minutes; a process still running after this has hung rather than thought.
const DefaultCallTimeout = 20 * time.Minute

// Claude answers by running Claude Code headless: one process per call, given
// the stage's system prompt in place of its own, the request on standard input,
// and no tools. It exits when it has answered, so no context survives from one
// call to the next and there is nothing to clear afterwards.
//
// It authenticates however Claude Code on this machine does: a login made with
// /login on a desktop, or CLAUDE_CODE_OAUTH_TOKEN from `claude setup-token` on a
// server. Either way it draws on the subscription, not on API credit.
type Claude struct {
	// Bin is the claude executable. Empty means "claude" on the PATH.
	Bin string

	// Models maps a stage to a model alias or name. A stage missing from it
	// falls back to DefaultModels.
	Models map[string]string

	// Timeout bounds one call. Zero means DefaultCallTimeout.
	Timeout time.Duration

	// OnLimits, when set, is told the plan's standing each time a call
	// reports it, which is what /usage shows.
	OnLimits func(Limits)
}

// strippedEnv are variables a child process must not inherit. Both outrank the
// subscription token in Claude Code's order of credentials, and in headless mode
// an API key is used without asking -- so a key left in a .env file would
// quietly move every call onto API billing, or fail it if the key has no credit.
var strippedEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// Answer runs one call.
func (c Claude) Answer(ctx context.Context, q Question) (Reply, error) {
	modelName := c.model(q.Stage)

	// The system prompt goes in a file rather than an argument: the brief's is
	// several thousand words, and Windows caps a whole command line at 32K
	// characters. It is removed afterwards, since the request file beside it
	// already records it.
	sys, err := os.CreateTemp(q.Dir, "system-*.txt")
	if err != nil {
		return Reply{}, err
	}
	defer os.Remove(sys.Name())
	if _, err := sys.WriteString(q.System); err != nil {
		sys.Close()
		return Reply{}, err
	}
	if err := sys.Close(); err != nil {
		return Reply{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	args := []string{
		"-p",
		"--model", modelName,
		"--system-prompt-file", sys.Name(),
		// Nothing written to the session history: each call stands alone, and
		// a year of them would otherwise pile up under ~/.claude.
		"--no-session-persistence",
		// The stream rather than one JSON object: only the stream carries the
		// plan's limits (rate_limit_event), which /usage reports.
		"--output-format", "stream-json", "--verbose",
	}
	if webStages[q.Stage] {
		// Exactly these two tools exist for the call, and no MCP server is
		// loaded, whatever this machine's own settings say -- except the
		// service's own, where the call carries one (tools.go).
		allowed := webTools
		if t, ok := toolsFrom(ctx); ok {
			config, extra, err := t.mcpConfig(q)
			if err != nil {
				return Reply{}, err
			}
			defer os.Remove(config)
			args = append(args, "--mcp-config", config)
			allowed = allowList(webTools, extra)
		}
		args = append(args, "--tools", webTools, "--allowedTools", allowed, "--strict-mcp-config")
	} else {
		// No tools. Every other stage is reading and writing: the request
		// carries all it needs, and a model that could run commands or fetch
		// pages could be steered into doing so by a headline in the feed.
		args = append(args, "--disallowedTools", "*")
	}
	if quickStages[q.Stage] {
		args = append(args, "--settings", noThinking)
	}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	// Run inside the run's directory, which holds no CLAUDE.md or project
	// settings, so the call sees the request and nothing of wherever the
	// service happens to have been started.
	cmd.Dir = q.Dir
	cmd.Env = childEnv()
	cmd.Stdin = strings.NewReader(q.Prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	runErr := cmd.Run()

	if errors.Is(runErr, exec.ErrNotFound) {
		return Reply{}, fmt.Errorf("%s: Claude Code is not installed here (%s not found on the PATH)", q.Stage, c.bin())
	}
	if ctx.Err() == context.DeadlineExceeded {
		return Reply{}, fmt.Errorf("%s: no answer from %s within %s", q.Stage, modelName, c.timeout())
	}

	out, limits, err := parseStream(stdout.Bytes())
	if limits != nil && c.OnLimits != nil {
		c.OnLimits(*limits)
	}
	if err != nil {
		// Claude Code reports a failure inside the run as JSON on stdout; one
		// that never started -- a bad flag, a crash -- leaves only stderr.
		if runErr != nil {
			return Reply{}, fmt.Errorf("%s: claude failed: %v: %s", q.Stage, runErr, tail(stderr.String()))
		}
		return Reply{}, fmt.Errorf("%s: unreadable reply from claude: %v", q.Stage, err)
	}
	if out.IsError || runErr != nil {
		why := strings.TrimSpace(out.Result)
		if why == "" {
			why = tail(stderr.String())
		}
		// The subtype names the kind of failure and is worth printing, but a
		// run stopped by the plan's limit still calls itself a success, and
		// "claude reported success" reads as nonsense above the real reason.
		if sub := out.Subtype; sub != "" && sub != "success" {
			return Reply{}, fmt.Errorf("%s: claude reported %s: %s", q.Stage, sub, why)
		}
		return Reply{}, fmt.Errorf("%s: claude stopped: %s", q.Stage, why)
	}

	return Reply{
		Text:  out.Result,
		Model: out.resolvedModel(modelName),
		Usage: model.Usage{
			InputTokens:     out.Usage.InputTokens,
			OutputTokens:    out.Usage.OutputTokens,
			CacheReadTokens: out.Usage.CacheReadInputTokens,
		},
	}, nil
}

// result is the part of Claude Code's JSON output this reads.
type result struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
	Usage   struct {
		InputTokens          int64 `json:"input_tokens"`
		OutputTokens         int64 `json:"output_tokens"`
		CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

// parseStream reads Claude Code's stream: one JSON object a line, of which
// the result and the plan's limits are wanted. A single JSON object, as
// --output-format json gives, is read as the result.
func parseStream(stdout []byte) (result, *Limits, error) {
	var (
		out    result
		limits *Limits
		found  bool
	)
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string          `json:"type"`
			Info json.RawMessage `json:"rate_limit_info"`
		}
		if json.Unmarshal(line, &head) != nil {
			continue
		}
		switch head.Type {
		case "rate_limit_event":
			var l Limits
			if json.Unmarshal(head.Info, &l) == nil {
				limits = &l
			}
		case "result", "":
			if json.Unmarshal(line, &out) == nil {
				found = true
			}
		}
	}
	if !found {
		return result{}, limits, errors.New("no result in its output")
	}
	return out, limits, nil
}

// Limits is the plan's standing as Claude Code reports it with a call: whether
// calls are allowed, and how much of each window is used.
type Limits struct {
	// Status is "allowed", "allowed_warning" near a limit, or "rejected".
	Status string `json:"status"`

	// Type names the window that limits now, such as "five_hour".
	Type string `json:"rateLimitType"`

	// Windows is each window's use, keyed "five_hour", "seven_day" and so on.
	Windows map[string]Window `json:"unifiedWindows"`

	// Overage is whether usage past the plan is allowed: "rejected" when the
	// plan has none.
	Overage string `json:"overageStatus"`
}

// Window is one limit window: the share of it used, usually 0 to 1, and when
// it starts again.
type Window struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

// Resets is when the window starts again.
func (w Window) Resets() time.Time { return time.Unix(w.ResetsAt, 0) }

// CheckLimits asks the cheapest model for one word, for the limits that come
// back with the answer: a few hundred tokens of Haiku, where waiting for the
// next real call could mean a day-old figure.
func (c Claude) CheckLimits(ctx context.Context) (Limits, error) {
	dir, err := os.MkdirTemp("", "limits")
	if err != nil {
		return Limits{}, err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin(), "-p",
		"--model", "haiku",
		"--system-prompt", "Answer with one word.",
		"--no-session-persistence",
		"--output-format", "stream-json", "--verbose",
		"--disallowedTools", "*",
		"--settings", noThinking)
	cmd.Dir = dir
	cmd.Env = childEnv()
	cmd.Stdin = strings.NewReader("Say ok.")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	_, limits, _ := parseStream(stdout.Bytes())
	if limits != nil {
		if c.OnLimits != nil {
			c.OnLimits(*limits)
		}
		return *limits, nil
	}
	if runErr != nil {
		return Limits{}, fmt.Errorf("claude failed: %v: %s", runErr, tail(stderr.String()))
	}
	return Limits{}, errors.New("claude reported no limits")
}

// resolvedModel is the full model name the alias became, when the output says
// so unambiguously. "haiku" is what was asked for; "claude-haiku-4-5-20251001"
// is what answered, and the ledger is more useful for saying which.
func (r result) resolvedModel(alias string) string {
	if len(r.ModelUsage) == 1 {
		for name := range r.ModelUsage {
			return name
		}
	}
	return alias
}

// Version asks the installed Claude Code for its version. It makes no model
// call, so it is how a check proves the answerer is there without drawing on
// the plan.
func (c Claude) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin(), "--version").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("claude: Claude Code is not installed here (%s not found on the PATH)", c.bin())
	}
	if err != nil {
		return "", fmt.Errorf("claude --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ModelFor is the model a stage will be answered by.
func (c Claude) ModelFor(stage string) string { return c.model(stage) }

func (c Claude) model(stage string) string {
	if m := strings.TrimSpace(c.Models[stage]); m != "" {
		return m
	}
	if m := DefaultModels[stage]; m != "" {
		return m
	}
	return Opus
}

func (c Claude) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "claude"
}

func (c Claude) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultCallTimeout
}

func childEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		keep := true
		for _, name := range strippedEnv {
			if strings.HasPrefix(kv, name+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// tail keeps the end of a stream, where the reason for a failure usually is.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 500 {
		return "…" + string(r[len(r)-500:])
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

// Session answers by waiting for someone else to write the reply file: a
// Claude Code session, usually, with a subagent per request. docs/RUNBOOK.md has
// the procedure. Nothing bounds the wait but the context, because the person
// answering may be asleep; a prepared run is started the night before.
type Session struct {
	// Poll is how often the reply file is looked for. Zero means two seconds.
	Poll time.Duration
	Log  func(format string, args ...any)
}

// Answer waits for the reply.
func (s Session) Answer(ctx context.Context, q Question) (Reply, error) {
	if s.Log != nil {
		s.Log("waiting for %s", q.ReplyPath())
	}
	for {
		if answer, err := os.ReadFile(q.ReplyPath()); err == nil && len(bytes.TrimSpace(answer)) > 0 {
			return Reply{Text: string(answer)}, nil
		}
		select {
		case <-ctx.Done():
			return Reply{}, fmt.Errorf("no reply written to %s: %w", q.ReplyPath(), ctx.Err())
		case <-time.After(s.poll()):
		}
	}
}

func (s Session) poll() time.Duration {
	if s.Poll > 0 {
		return s.Poll
	}
	return 2 * time.Second
}
