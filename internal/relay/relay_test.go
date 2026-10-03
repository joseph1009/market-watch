package relay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/runcache"
)

// scripted answers every question the same way, and remembers what it was asked.
type scripted struct {
	mu    sync.Mutex
	asked []Question
	text  string
	err   error
}

func (s *scripted) Answer(_ context.Context, q Question) (Reply, error) {
	s.mu.Lock()
	s.asked = append(s.asked, q)
	s.mu.Unlock()
	return Reply{Text: s.text, Model: "claude-test"}, s.err
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Every call leaves both halves on disk and a line in the ledger: that is what
// makes a run readable afterwards, whoever answered it.
func TestAskKeepsTheRequestTheReplyAndTheLedger(t *testing.T) {
	answer := &scripted{text: "1|4|energy"}
	r := &Relay{Root: t.TempDir(), Answer: answer}

	ctx, run, err := r.Begin(context.Background(), "brief")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	got, _, err := r.Plain(Triage).Complete(ctx, "rate these", "1. [CNBC] Oil jumps")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "1|4|energy" {
		t.Errorf("reply = %q", got)
	}

	request := read(t, filepath.Join(run.Dir, "01-triage-request.txt"))
	if !strings.Contains(request, "===== SYSTEM =====\nrate these") || !strings.Contains(request, "===== PROMPT =====\n1. [CNBC] Oil jumps") {
		t.Errorf("request file does not carry both halves:\n%s", request)
	}
	if reply := read(t, filepath.Join(run.Dir, "01-triage-reply.txt")); reply != "1|4|energy" {
		t.Errorf("reply file = %q", reply)
	}
	ledger := read(t, filepath.Join(run.Dir, "ledger.md"))
	for _, want := range []string{"## brief", "- [ ] triage asked", "- [x] triage answered by claude-test"} {
		if !strings.Contains(ledger, want) {
			t.Errorf("ledger is missing %q:\n%s", want, ledger)
		}
	}
}

// A call made while a run cache is open is copied into it, under model/, so
// the latest run's folder has its prompts and replies beside its data.
func TestAskCopiesTheCallIntoTheRunCache(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: &scripted{text: "BUY"}}
	cacheRoot := t.TempDir()
	ctx, cached := (&runcache.Cache{Root: cacheRoot}).Start(context.Background(), runcache.Recommendations, "")
	ctx, _, err := r.Begin(ctx, "look")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Plain(Verdicts).Complete(ctx, "judge", "NVDA facts"); err != nil {
		t.Fatal(err)
	}
	cached.Finish(nil)

	model := filepath.Join(cacheRoot, runcache.Recommendations, "model")
	if !strings.Contains(read(t, filepath.Join(model, "01-verdicts-request.txt")), "NVDA facts") {
		t.Error("the request was not copied into the cache")
	}
	if read(t, filepath.Join(model, "01-verdicts-reply.txt")) != "BUY" {
		t.Error("the reply was not copied into the cache")
	}
}

// Calls made under one run share its directory and are numbered in order, so
// a brief's four sorting batches, its writing and its names read top to bottom.
func TestCallsInOneRunAreNumberedTogether(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: &scripted{text: "ok"}}
	ctx, run, err := r.Begin(context.Background(), "brief")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, stage := range []string{Triage, Triage, Brief, Names} {
		if _, err := r.Stage(stage).Complete(ctx, "s", "p"); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	for _, name := range []string{"01-triage-request.txt", "02-triage-request.txt", "03-brief-request.txt", "04-names-request.txt"} {
		if _, err := os.Stat(filepath.Join(run.Dir, name)); err != nil {
			t.Errorf("%s is missing: %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(r.Root); len(entries) != 1 {
		t.Errorf("one run spread over %d directories", len(entries))
	}
}

// A failed call is recorded as failed, so the ledger never shows a stage as
// merely waiting when it has in fact given up.
func TestAFailedCallIsMarkedInTheLedger(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: &scripted{err: errors.New("rate limited")}}
	ctx, run, _ := r.Begin(context.Background(), "brief")

	if _, err := r.Stage(Brief).Complete(ctx, "s", "p"); err == nil {
		t.Fatal("the failure was swallowed")
	}
	if ledger := read(t, filepath.Join(run.Dir, "ledger.md")); !strings.Contains(ledger, "- [!] brief failed: rate limited") {
		t.Errorf("the ledger does not record the failure:\n%s", ledger)
	}
}

// An empty answer is a failure, not a brief with nothing in it.
func TestAnEmptyReplyIsAnError(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: &scripted{text: "  \n"}}
	if _, err := r.Stage(Brief).Complete(context.Background(), "s", "p"); err == nil {
		t.Error("an empty reply was accepted")
	}
}

// Runs accumulate on the data volume every day. Only the most recent are kept.
func TestOldRunsArePruned(t *testing.T) {
	root := t.TempDir()
	clock := time.Date(2026, 9, 1, 20, 30, 0, 0, time.UTC)
	r := &Relay{Root: root, Answer: &scripted{text: "ok"}, Keep: 3, Now: func() time.Time { return clock }}

	for i := 0; i < 5; i++ {
		if _, _, err := r.Begin(context.Background(), "brief"); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		clock = clock.AddDate(0, 0, 1)
	}

	entries, _ := os.ReadDir(root)
	if len(entries) != 3 {
		t.Fatalf("kept %d runs, want 3", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "20260903") {
		t.Errorf("oldest kept run is %s, want the 3rd of September", entries[0].Name())
	}
}

// A brief and an analysis started in the same second must not share a
// directory, or their numbered files would collide.
func TestRunsStartedTogetherGetDirectoriesOfTheirOwn(t *testing.T) {
	clock := time.Date(2026, 9, 1, 20, 30, 0, 0, time.UTC)
	r := &Relay{Root: t.TempDir(), Answer: &scripted{text: "ok"}, Now: func() time.Time { return clock }}

	_, a, _ := r.Begin(context.Background(), "brief")
	_, b, _ := r.Begin(context.Background(), "brief")
	if a.Dir == b.Dir {
		t.Errorf("two runs share %s", a.Dir)
	}
}

// A person answering writes the reply file; the session answerer picks it up.
func TestASessionAnswerIsReadFromTheReplyFile(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: Session{Poll: 10 * time.Millisecond}}
	ctx, run, _ := r.Begin(context.Background(), "brief")

	go func() {
		reply := filepath.Join(run.Dir, "01-brief-reply.txt")
		for {
			if _, err := os.Stat(filepath.Join(run.Dir, "01-brief-request.txt")); err == nil {
				_ = os.WriteFile(reply, []byte("## OVERVIEW\nWritten by hand."), 0o644)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	got, err := r.Stage(Brief).Complete(ctx, "s", "p")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "## OVERVIEW\nWritten by hand." {
		t.Errorf("reply = %q", got.Text)
	}
}

func TestASessionThatNeverAnswersGivesUpWithTheContext(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: Session{Poll: 10 * time.Millisecond}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := r.Stage(Brief).Complete(ctx, "s", "p"); err == nil {
		t.Error("a run with no answer did not give up")
	}
}

// fakeClaude builds the stand-in for Claude Code once per test binary.
var (
	fakeOnce sync.Once
	fakeBin  string
	fakeErr  error
)

func fakeClaude(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeclaude")
		if err != nil {
			fakeErr = err
			return
		}
		fakeBin = filepath.Join(dir, "claude")
		if runtime.GOOS == "windows" {
			fakeBin += ".exe"
		}
		out, err := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakeclaude").CombinedOutput()
		if err != nil {
			fakeErr = errors.New(string(out))
		}
	})
	if fakeErr != nil {
		t.Fatalf("build fake claude: %v", fakeErr)
	}
	return fakeBin
}

type record struct {
	Args      []string `json:"args"`
	Model     string   `json:"model"`
	System    string   `json:"system"`
	Stdin     string   `json:"stdin"`
	Dir       string   `json:"dir"`
	APIKey    bool     `json:"api_key"`
	AuthToken bool     `json:"auth_token"`
	MCPConfig string   `json:"mcp_config"`
}

func recorded(t *testing.T, path string) record {
	t.Helper()
	var r record
	if err := json.Unmarshal([]byte(read(t, path)), &r); err != nil {
		t.Fatalf("parse record: %v", err)
	}
	return r
}

// The headless call is the service as deployed. What it is given decides
// whether a brief is written well and safely: the stage's own model, our
// system prompt in place of Claude Code's, the request on standard input, no
// tools, and no API key that would move the call off the subscription.
func TestClaudeRunsHeadlessWithTheStagesModelAndNoTools(t *testing.T) {
	bin := fakeClaude(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	t.Setenv("FAKE_CLAUDE_RECORD", rec)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-left-in-a-dotenv")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "also-left-over")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}
	ctx, run, _ := r.Begin(context.Background(), "brief")

	text, usage, err := r.Plain(Triage).Complete(ctx, "You rate news.", "1. [CNBC] Oil jumps")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "answered: 1. [CNBC] Oil jumps" {
		t.Errorf("reply = %q", text)
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 30 {
		t.Errorf("usage = %+v, want the counts Claude Code reported", usage)
	}

	got := recorded(t, rec)
	if got.Model != Opus {
		t.Errorf("model = %q, want Opus 5.5 for sorting", got.Model)
	}
	if got.System != "You rate news." {
		t.Errorf("system prompt = %q, want the stage's own", got.System)
	}
	if got.Stdin != "1. [CNBC] Oil jumps" {
		t.Errorf("stdin = %q, want the request", got.Stdin)
	}
	joined := strings.Join(got.Args, " ")
	for _, want := range []string{"-p", "--disallowedTools *", "--no-session-persistence", "--output-format stream-json --verbose"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args are missing %q: %v", want, got.Args)
		}
	}
	if !strings.Contains(joined, `--settings {"alwaysThinkingEnabled":false}`) {
		t.Errorf("sorting ran with thinking on, which took it from five seconds to sixty-five: %v", got.Args)
	}
	if got.APIKey || got.AuthToken {
		t.Error("an API credential reached Claude Code, which would take the call off the subscription")
	}
	if filepath.Clean(got.Dir) != filepath.Clean(run.Dir) {
		t.Errorf("ran in %s, want the run's own directory so no project settings are picked up", got.Dir)
	}
	if ledger := read(t, filepath.Join(run.Dir, "ledger.md")); !strings.Contains(ledger, "answered by claude-haiku-4-5-20251001") {
		t.Errorf("the ledger does not say which model answered:\n%s", ledger)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(run.Dir, "system-*.txt")); len(leftovers) != 0 {
		t.Errorf("the system prompt file was left behind: %v", leftovers)
	}
}

func TestClaudeUsesTheModelConfiguredForAStage(t *testing.T) {
	bin := fakeClaude(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	t.Setenv("FAKE_CLAUDE_RECORD", rec)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin, Models: map[string]string{Brief: "sonnet"}}}
	if _, err := r.Stage(Brief).Complete(context.Background(), "s", "p"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := recorded(t, rec)
	if got.Model != "sonnet" {
		t.Errorf("model = %q, want the configured sonnet", got.Model)
	}
	// Writing the brief is the judgment thinking is for.
	if strings.Contains(strings.Join(got.Args, " "), "alwaysThinkingEnabled") {
		t.Errorf("the brief was written with thinking turned off: %v", got.Args)
	}
}

// The scout, the research, the review, the verdicts and the analysis may
// search the web and read pages, and nothing else: no shell, no files, no
// connectors. Every other stage, the sorting of themes included, still gets
// no tools at all.
func TestOnlyTheResearchingStagesMaySearchTheWeb(t *testing.T) {
	bin := fakeClaude(t)
	rec := filepath.Join(t.TempDir(), "record.json")
	t.Setenv("FAKE_CLAUDE_RECORD", rec)
	t.Setenv("FAKE_CLAUDE_MODE", "ok")
	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}

	if _, _, err := r.Plain(Research).Complete(context.Background(), "s", "p"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := recorded(t, rec)
	joined := strings.Join(got.Args, " ")
	for _, want := range []string{"--tools WebSearch,WebFetch", "--allowedTools WebSearch,WebFetch", "--strict-mcp-config"} {
		if !strings.Contains(joined, want) {
			t.Errorf("research args are missing %q: %v", want, got.Args)
		}
	}
	if strings.Contains(joined, "--disallowedTools") {
		t.Errorf("research had every tool taken away, web search included: %v", got.Args)
	}
	if got.Model != Opus {
		t.Errorf("research model = %q, want Opus 5.5", got.Model)
	}
	if strings.Contains(joined, "alwaysThinkingEnabled") {
		t.Errorf("research ran with thinking off: %v", got.Args)
	}

	// The review may look up a company it does not know, and answers quickly:
	// placing an article is not a judgment worth thinking at length about.
	if _, _, err := r.Plain(Review).Complete(context.Background(), "s", "p"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got = recorded(t, rec)
	joined = strings.Join(got.Args, " ")
	if !strings.Contains(joined, "--tools WebSearch,WebFetch") || got.Model != Opus {
		t.Errorf("review args = %v, model %q; want web search and Opus 5.5", got.Args, got.Model)
	}
	if !strings.Contains(joined, "alwaysThinkingEnabled") {
		t.Errorf("review ran with thinking on: %v", got.Args)
	}

	if _, _, err := r.Plain(Scout).Complete(context.Background(), "s", "p"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got = recorded(t, rec); !strings.Contains(strings.Join(got.Args, " "), "--tools WebSearch,WebFetch") || got.Model != Opus {
		t.Errorf("scout args = %v, model %q; want web search and Opus 5.5", got.Args, got.Model)
	}

	// The verdicts check a case against a second source, and the analysis
	// looks for the last fortnight's news.
	for _, stage := range []string{Verdicts, Analysis} {
		if _, _, err := r.Plain(stage).Complete(context.Background(), "s", "p"); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got = recorded(t, rec); !strings.Contains(strings.Join(got.Args, " "), "--tools WebSearch,WebFetch") {
			t.Errorf("%s args = %v; want web search", stage, got.Args)
		}
	}

	for _, stage := range []string{Triage, Themes} {
		if _, _, err := r.Plain(stage).Complete(context.Background(), "s", "p"); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		got = recorded(t, rec)
		joined = strings.Join(got.Args, " ")
		if !strings.Contains(joined, "--disallowedTools *") || strings.Contains(joined, "WebSearch") {
			t.Errorf("%s must run with no tools: %v", stage, got.Args)
		}
	}
}

// TestLiveClaude asks the real Claude Code one small question per quick stage,
// through the same answerer the service uses. It is the proof that this
// machine can answer at all -- installed, logged in, and taking the flags --
// for the price of a few hundred tokens.
//
//	LIVE_CLAUDE=1 go test ./internal/relay -run TestLiveClaude -v
func TestLiveClaude(t *testing.T) {
	if os.Getenv("LIVE_CLAUDE") == "" {
		t.Skip("set LIVE_CLAUDE=1 to ask the installed Claude Code a question")
	}
	var seen []Limits
	claude := Claude{OnLimits: func(l Limits) { seen = append(seen, l) }}
	r := &Relay{Root: t.TempDir(), Answer: claude}
	ctx, run, err := r.Begin(context.Background(), "live-check")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	started := time.Now()
	text, usage, err := r.Plain(Triage).Complete(ctx,
		"You rate news. Reply with one line per item in the form number|rating|- and nothing else.",
		"1. [Reuters] Federal Reserve raises interest rates by a quarter point")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	t.Logf("%q in %s, %d in / %d out, run in %s",
		strings.TrimSpace(text), time.Since(started).Round(100*time.Millisecond),
		usage.InputTokens, usage.OutputTokens, run.Dir)
	if !strings.HasPrefix(strings.TrimSpace(text), "1|") {
		t.Errorf("reply is not in the requested form: %q", text)
	}
	if len(seen) != 1 || len(seen[0].Windows) == 0 {
		t.Errorf("the call reported limits %+v, want the plan's windows", seen)
	}
	limits, err := claude.CheckLimits(context.Background())
	if err != nil || len(limits.Windows) == 0 {
		t.Errorf("CheckLimits = %+v, %v", limits, err)
	}
	t.Logf("limits: %+v", limits)
}

// A run the plan's limit stops still calls itself a success and puts the
// reason in the result. What the reader needs is that reason, not the word
// "success" above it.
func TestThePlanLimitIsReportedAsItself(t *testing.T) {
	bin := fakeClaude(t)
	t.Setenv("FAKE_CLAUDE_RECORD", "")
	t.Setenv("FAKE_CLAUDE_MODE", "limit")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}
	_, _, err := r.Plain(Research).Complete(context.Background(), "s", "p")
	if err == nil {
		t.Fatal("a run stopped by the limit was taken for an answer")
	}
	if got := err.Error(); !strings.Contains(got, "session limit") || strings.Contains(got, "reported success") {
		t.Errorf("err = %q, want the limit as the reason and no talk of success", got)
	}
}

// Claude Code reports a failure inside the run -- no login, a limit reached --
// as JSON on stdout. That reason is what the reader needs in the failure
// message, not "exit status 1".
func TestClaudeSaysWhyItFailed(t *testing.T) {
	bin := fakeClaude(t)
	t.Setenv("FAKE_CLAUDE_RECORD", "")
	t.Setenv("FAKE_CLAUDE_MODE", "error")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}
	_, err := r.Stage(Brief).Complete(context.Background(), "s", "p")
	if err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("err = %v, want Claude Code's own reason", err)
	}
}

func TestClaudeReportsAProcessThatNeverStartedProperly(t *testing.T) {
	bin := fakeClaude(t)
	t.Setenv("FAKE_CLAUDE_RECORD", "")
	t.Setenv("FAKE_CLAUDE_MODE", "crash")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin}}
	_, err := r.Stage(Brief).Complete(context.Background(), "s", "p")
	if err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("err = %v, want what it printed to stderr", err)
	}
}

func TestClaudeThatHangsIsStopped(t *testing.T) {
	bin := fakeClaude(t)
	t.Setenv("FAKE_CLAUDE_RECORD", "")
	t.Setenv("FAKE_CLAUDE_MODE", "hang")

	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin, Timeout: 200 * time.Millisecond}}
	started := time.Now()
	_, err := r.Stage(Brief).Complete(context.Background(), "s", "p")
	if err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("err = %v, want a timeout", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Error("the hung process was not killed at the timeout")
	}
}

func TestClaudeThatIsNotInstalledSaysSo(t *testing.T) {
	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: "definitely-not-claude-here"}}
	_, err := r.Stage(Brief).Complete(context.Background(), "s", "p")
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err = %v, want it to say Claude Code is missing", err)
	}
}

// Every call reports the plan's limits, which /usage shows: the stream's
// rate_limit_event, each window's use and when it resets.
func TestEachCallReportsThePlansLimits(t *testing.T) {
	bin := fakeClaude(t)
	t.Setenv("FAKE_CLAUDE_RECORD", "")
	t.Setenv("FAKE_CLAUDE_MODE", "ok")

	var got []Limits
	r := &Relay{Root: t.TempDir(), Answer: Claude{Bin: bin, OnLimits: func(l Limits) { got = append(got, l) }}}
	if _, _, err := r.Plain(Brief).Complete(context.Background(), "s", "p"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("limits reported %d times, want once", len(got))
	}
	l := got[0]
	if l.Status != "allowed" || l.Overage != "rejected" || l.Windows["five_hour"].Utilization != 0.43 ||
		l.Windows["seven_day"].Resets().Unix() != 1791345600 {
		t.Errorf("limits = %+v", l)
	}

	got = nil
	checked, err := Claude{Bin: bin, OnLimits: func(l Limits) { got = append(got, l) }}.CheckLimits(context.Background())
	if err != nil || checked.Windows["seven_day"].Utilization != 0.21 || len(got) != 1 {
		t.Errorf("CheckLimits = %+v, %v; reported %d", checked, err, len(got))
	}
}

func TestVersionAsksNothingOfAModel(t *testing.T) {
	bin := fakeClaude(t)
	got, err := Claude{Bin: bin}.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "9.9.9 (Claude Code)" {
		t.Errorf("version = %q", got)
	}
}

func TestEveryStageIsAnsweredByOpus55(t *testing.T) {
	c := Claude{}
	for _, stage := range []string{Triage, Review, Names, Brief, Themes, Scout, Research, Verdicts, Analysis, Industry, "unknown"} {
		if got := c.ModelFor(stage); got != "claude-opus-5-5" {
			t.Errorf("%s answered by %q, want claude-opus-5-5", stage, got)
		}
	}
}
