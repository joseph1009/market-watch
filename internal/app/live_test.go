package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// These run the real service against the real chat. They are how a brief or an
// analysis is run by hand from a checkout: the model calls go through the relay
// and are answered as RELAY_ANSWER says, claude by default. docs/RUNBOOK.md has the
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

// TestLiveMarket fills the market's history under data/market -- about two
// hours from empty, a minute a day after -- and logs what the week's themes
// would start from: the leaders, the popular industries and the early ones.
// It asks no model anything.
//
//	LIVE_MARKET=1 go test ./internal/app -run TestLiveMarket -v -timeout 4h
func TestLiveMarket(t *testing.T) {
	if os.Getenv("LIVE_MARKET") == "" {
		t.Skip("set LIVE_MARKET=1 to fill the market's history and read it")
	}
	service := liveService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()
	for failures := 0; ; {
		asked, err := service.MarketStore.Sync(ctx, service.Movers, time.Now(), marketChunk)
		if err != nil {
			if failures++; failures == 5 {
				t.Fatalf("sync: %v", err)
			}
			t.Logf("sync: %v; again in a minute", err)
			time.Sleep(time.Minute)
			continue
		}
		t.Logf("fetched %d days; %d missing", asked, service.MarketStore.Missing(time.Now()))
		if asked < marketChunk {
			break
		}
	}
	listings, err := service.listings(ctx)
	if err != nil {
		t.Fatalf("listings: %v", err)
	}
	panel, err := service.loadPanel(listings)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	stocks := market.Measure(panel, listings)
	bench := market.MeasureSeries(panel.Get(market.Benchmark), market.Listing{Symbol: market.Benchmark})
	pool := append(append([]market.Stock{}, stocks...), service.singaporeStocks(ctx)...)
	leaders, left := market.Leaders(pool, market.DefaultRules, leaderCount)
	why := map[string]int{}
	for _, l := range left {
		why[l.Why]++
	}
	t.Logf("%d sessions, %d measured, %d leaders; left out: %v", len(panel.Dates), len(pool), len(leaders), why)
	for i, l := range leaders[:min(40, len(leaders))] {
		t.Logf("%3d %-7s %-40s %-45s 2y %s 12-1 %s 6m %s score %.2f", i+1, l.Symbol, market.PlainName(l.Name), l.Industry,
			pp(l.R24-bench.R24), pp(l.R12x1-bench.R12x1), pp(l.R6-bench.R6), l.Score)
	}
	headlines := service.recentHeadlines()
	inds := market.Industries(stocks, bench, market.DefaultRules, market.Mentions(headlines, listings))
	for _, d := range market.Popular(inds, popularIndustries) {
		t.Logf("popular %.2f: %s", d.Popular, ideas.IndustryLine(d))
	}
	for _, d := range market.Early(inds, earlyIndustries) {
		t.Logf("early %.2f: %s", d.Early, ideas.IndustryLine(d))
	}
	moves := market.Moves(panel, listings, market.DefaultRules, moveTimes, moveBusy)
	for _, m := range moves[:min(10, len(moves))] {
		t.Logf("move %-6s %+.1f%% (%.1f times usual, %.1f times the trading) %s", m.Symbol, 100*m.Percent, m.Times, m.Busy, market.PlainName(m.Name))
	}
}

// TestLiveThemes runs one week's themes and the day's reactions for real,
// with the models, and sends the result to the chat alone: the week is not
// written down, so the scheduled run still does it.
//
//	LIVE_THEMES=1 go test ./internal/app -run TestLiveThemes -v -timeout 2h
func TestLiveThemes(t *testing.T) {
	if os.Getenv("LIVE_THEMES") == "" {
		t.Skip("set LIVE_THEMES=1 to run a week's themes and send them to the chat")
	}
	service := liveService(t)
	if service.Prefs().ChatID == 0 {
		t.Fatal("no chat registered; send /start to the bot first")
	}
	// A log that remembers nothing, so the week runs, and is kept apart.
	log, err := ideas.LoadThemeLog(filepath.Join(t.TempDir(), "themes.json"))
	if err != nil {
		t.Fatal(err)
	}
	service.Themes = log
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if service.Relay != nil {
		var run *relay.Run
		if ctx, run, err = service.Relay.Begin(ctx, "look"); err != nil {
			t.Fatal(err)
		}
		t.Logf("relay run in %s", run.Dir)
	}
	service.sendIdeas(ctx, look{Scheduled: true})
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
	log := slog.New(logging.New(slog.NewTextHandler(os.Stderr, nil), cfg.Secrets()...))
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
