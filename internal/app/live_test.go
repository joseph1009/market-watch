package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// TestCannedAnalysis is a pre-written run: an analysis written earlier is
// handed to the real /analyse delivery, which asks nothing and waits for
// nothing. The filings, price, trading history, news, rendering, ticker checks
// and chat are all the real ones, so it shows what an analysis looks like on a
// phone without spending anything on the API.
//
//	CANNED_REPLY=analysis.txt FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestCannedAnalysis -v
//
// The file is the model's reply as the bot would receive it: plain text,
// capitalised section headings, "- " bullets, and the related companies as
// name|ticker|exchange|why lines under COMPANIES TO READ NEXT TO IT.
func TestCannedAnalysis(t *testing.T) {
	path := os.Getenv("CANNED_REPLY")
	if path == "" {
		t.Skip("set CANNED_REPLY to a written analysis to send it to the chat")
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	// The test runs from this package's directory, where the default data
	// directory would be a new and empty one with no chat registered.
	if os.Getenv("DATA_DIR") == "" {
		data, _ := filepath.Abs("../../data")
		t.Setenv("DATA_DIR", data)
	}
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "MU"
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	log := slog.New(logging.New(slog.NewTextHandler(os.Stderr, nil), cfg.TelegramBotToken, cfg.AnthropicAPIKey))
	service, err := New(cfg, log)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	service.Agent = nil
	service.Analyzer = &fundamentals.Analyzer{Completer: written(text)}

	chat := service.Prefs().ChatID
	if chat == 0 {
		t.Fatal("no chat registered; send /start to the bot first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := service.handleAnalyse(ctx, telegram.Message{Chat: telegram.Chat{ID: chat}}, []string{ticker}); err != nil {
		t.Fatalf("analyse: %v", err)
	}
}

// written stands in for the model with a reply that already exists. It names
// no model, so the cost shown with the analysis is nothing, which is true.
type written []byte

func (w written) Complete(context.Context, string, string) (report.Completion, error) {
	return report.Completion{Text: string(w)}, nil
}

// TestRelayBrief is a relay run: the real daily brief, with each of its three
// model calls -- sorting the day's articles, writing the brief, spotting new
// company names -- written to a file and answered from one. Everything else is
// the real thing: the same feeds, prices, watchlists, rendering and chat.
//
//	RELAY_DIR=<dir> go test ./internal/app -run TestRelayBrief -v -timeout 3h
//
// Each call writes <dir>/NN-<stage>-request.txt and then waits for a file
// beside it named NN-<stage>-reply.txt. Write the reply in the form that
// stage's prompt asks for, and the run carries on. Nothing here calls the API.
func TestRelayBrief(t *testing.T) {
	service, relay := relayRun(t, "brief")
	service.Generator.Completer = stage{relay, "brief"}
	if service.Triage != nil {
		service.Triage.Completer = plain{relay, "triage"}
		// One wave of larger batches: each round trip is a person writing a
		// file, so fewer and bigger beats many and small, and the timeout has
		// to allow for however long that takes.
		service.Triage.BatchSize = 150
		service.Triage.Concurrency = 4
		service.Triage.Timeout = 3 * time.Hour
	}
	if service.Finder != nil {
		service.Finder.Completer = plain{relay, "names"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	if err := service.SendReport(ctx); err != nil {
		t.Fatalf("brief: %v", err)
	}
}

// TestRelayAnalysis is a relay run for /analyse: the filings, the price
// history and the news are read as usual, the request is written to a file,
// and the analysis written into the reply is delivered to the chat.
//
//	RELAY_DIR=<dir> FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestRelayAnalysis -v -timeout 2h
//
// This is the single-call analysis. The agent, which looks concepts up while
// it writes, speaks to the API directly and has nothing to relay through.
func TestRelayAnalysis(t *testing.T) {
	service, relay := relayRun(t, "analysis")
	service.Agent = nil
	service.Analyzer = &fundamentals.Analyzer{Completer: stage{relay, "analysis"}}

	chat := service.Prefs().ChatID
	if chat == 0 {
		t.Fatal("no chat registered; send /start to the bot first")
	}
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "MU"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if err := service.handleAnalyse(ctx, telegram.Message{Chat: telegram.Chat{ID: chat}}, []string{ticker}); err != nil {
		t.Fatalf("analyse: %v", err)
	}
}

// relayRun builds the service with its model calls relayed to files.
//
// RELAY_AT=HH:MM holds the run until that time before it starts -- a prepared
// run, which collects what it needs and leaves its requests waiting for
// whenever the answering session is next open.
func relayRun(t *testing.T, kind string) (*App, *relay) {
	t.Helper()

	dir := os.Getenv("RELAY_DIR")
	if dir == "" {
		t.Skip("set RELAY_DIR to answer this run's model calls from files")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("RELAY_DIR: %v", err)
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	// The test runs from this package's directory, where the default data
	// directory would be a new and empty one with no chat registered.
	if os.Getenv("DATA_DIR") == "" {
		data, _ := filepath.Abs("../../data")
		t.Setenv("DATA_DIR", data)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	log := slog.New(logging.New(slog.NewTextHandler(os.Stderr, nil), cfg.TelegramBotToken, cfg.AnthropicAPIKey))
	service, err := New(cfg, log)
	if err != nil {
		t.Fatalf("app: %v", err)
	}

	r := &relay{dir: dir, log: t.Logf}
	r.note("## relay run: %s, %s", kind, time.Now().Format("Mon 2 Jan 2006 15:04"))
	holdUntil(t, r, os.Getenv("RELAY_AT"))
	return service, r
}

// holdUntil waits for the next occurrence of a HH:MM time in this machine's
// timezone, so a run can be started now and collect its news then.
func holdUntil(t *testing.T, r *relay, at string) {
	t.Helper()
	if at == "" {
		return
	}
	when, err := time.ParseInLocation("15:04", at, time.Local)
	if err != nil {
		t.Fatalf("RELAY_AT: %v", err)
	}
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), when.Hour(), when.Minute(), 0, 0, time.Local)
	if !target.After(now) {
		target = target.AddDate(0, 0, 1)
	}
	r.note("holding until %s", target.Format("15:04 on Mon 2 Jan"))
	t.Logf("holding until %s", target.Format(time.RFC1123))
	time.Sleep(time.Until(target))
}

// relay writes each model call to a file and waits for the reply to appear
// beside it.
type relay struct {
	dir  string
	n    atomic.Int32
	log  func(format string, args ...any)
	poll time.Duration

	mu sync.Mutex // guards the ledger, which concurrent stages append to
}

// note appends a line to the run's ledger.
//
// The ledger is what lets a relay run outlive the session answering it. A
// session that is compacted, interrupted or restarted has forgotten what it
// was doing; the ledger says which stages were asked, which were answered and
// how large each was, so the run can be picked up from the files on disk
// rather than from anybody's memory.
func (r *relay) note(format string, args ...any) {
	line := fmt.Sprintf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(r.dir, "ledger.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		r.log("ledger: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		r.log("ledger: %v", err)
	}
}

// stage is the relay as the brief and the analysis want it: a completion.
type stage struct {
	*relay
	name string
}

func (s stage) Complete(ctx context.Context, system, prompt string) (report.Completion, error) {
	answer, err := s.ask(ctx, s.name, system, prompt)
	return report.Completion{Text: answer}, err
}

func (r *relay) ask(ctx context.Context, name, system, prompt string) (string, error) {
	base := filepath.Join(r.dir, fmt.Sprintf("%02d-%s", r.n.Add(1), name))
	body := "===== SYSTEM =====\n" + system + "\n\n===== PROMPT =====\n" + prompt + "\n"
	if err := os.WriteFile(base+"-request.txt", []byte(body), 0o644); err != nil {
		return "", err
	}
	r.note("- [ ] %s asked, %s to read in %s", name, size(len(body)), filepath.Base(base)+"-request.txt")

	reply := base + "-reply.txt"
	r.log("waiting for %s", reply)
	for {
		if answer, err := os.ReadFile(reply); err == nil && len(answer) > 0 {
			r.log("read %s (%d bytes)", reply, len(answer))
			r.note("- [x] %s answered, %s from %s", name, size(len(answer)), filepath.Base(reply))
			return string(answer), nil
		}
		select {
		case <-ctx.Done():
			r.note("- [!] %s abandoned: no reply written to %s", name, filepath.Base(reply))
			return "", fmt.Errorf("no reply written to %s: %w", reply, ctx.Err())
		case <-time.After(r.interval()):
		}
	}
}

func (r *relay) interval() time.Duration {
	if r.poll > 0 {
		return r.poll
	}
	return 2 * time.Second
}

// plain is the same relay for the passes that want plain text and a usage
// figure back. The usage is empty because nothing was spent.
type plain struct {
	*relay
	name string
}

func (p plain) Complete(ctx context.Context, system, prompt string) (string, model.Usage, error) {
	answer, err := p.ask(ctx, p.name, system, prompt)
	return answer, model.Usage{}, err
}

// size reads a byte count the way a person judges whether a request is small
// enough to read in the answering session or wants a subagent of its own.
func size(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d bytes", bytes)
	}
	return fmt.Sprintf("%.0fKB", float64(bytes)/1024)
}
