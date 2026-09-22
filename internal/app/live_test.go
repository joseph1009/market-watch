package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// These run the real service against the real chat. They are how a brief or an
// analysis is run by hand from a checkout: the model calls go through the relay
// and are answered as RELAY_ANSWER says, claude by default. RUNBOOK.md has the
// procedure, including answering a session run with subagents.

// TestLiveBrief sends one real brief.
//
//	LIVE_BRIEF=1 go test ./internal/app -run TestLiveBrief -v -timeout 1h
//	LIVE_BRIEF=1 RELAY_ANSWER=session RELAY_AT=08:30 go test ./internal/app -run TestLiveBrief -v -timeout 12h
//
// The second is a prepared run: started the night before, it holds until 08:30,
// collects the news, and leaves each request waiting for a session to answer.
// `market-watch --once` sends a brief the same way from the binary; this form
// exists for the hold.
func TestLiveBrief(t *testing.T) {
	if os.Getenv("LIVE_BRIEF") == "" {
		t.Skip("set LIVE_BRIEF=1 to send a real brief")
	}
	service := liveService(t)
	holdUntil(t, os.Getenv("RELAY_AT"))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	if err := service.SendReport(ctx); err != nil {
		t.Fatalf("brief: %v", err)
	}
}

// TestLiveAnalysis sends one real /analyse to the chat.
//
//	LIVE_ANALYSIS=MU go test ./internal/app -run TestLiveAnalysis -v -timeout 1h
func TestLiveAnalysis(t *testing.T) {
	ticker := os.Getenv("LIVE_ANALYSIS")
	if ticker == "" {
		t.Skip("set LIVE_ANALYSIS to a ticker to send a real analysis")
	}
	service := liveService(t)
	chat := service.Prefs().ChatID
	if chat == 0 {
		t.Fatal("no chat registered; send /start to the bot first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	if err := service.handleAnalyse(ctx, telegram.Message{Chat: telegram.Chat{ID: chat}}, []string{ticker}); err != nil {
		t.Fatalf("analyse: %v", err)
	}
}

// TestCannedAnalysis is a pre-written run: an analysis written earlier is
// handed to the real /analyse delivery, which asks nothing and waits for
// nothing. The filings, price, trading history, news, rendering, ticker checks
// and chat are all the real ones, so it shows what an analysis looks like on a
// phone without asking a model anything.
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
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "MU"
	}

	service := liveService(t)
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

// written stands in for the model with a reply that already exists.
type written []byte

func (w written) Complete(context.Context, string, string) (report.Completion, error) {
	return report.Completion{Text: string(w)}, nil
}

// liveService builds the service as the binary would, from .env and the data
// directory at the root of the checkout.
func liveService(t *testing.T) *App {
	t.Helper()
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
	log := slog.New(logging.New(slog.NewTextHandler(os.Stderr, nil), cfg.TelegramBotToken, cfg.ClaudeToken))
	service, err := New(cfg, log)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Logf("answered by %s, runs written under %s", cfg.RelayAnswer, cfg.RelayDir)
	return service
}

// holdUntil waits for the next occurrence of a HH:MM time in this machine's
// timezone, so a run can be started now and collect its news then.
func holdUntil(t *testing.T, at string) {
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
	t.Logf("holding until %s", target.Format(time.RFC1123))
	time.Sleep(time.Until(target))
}
