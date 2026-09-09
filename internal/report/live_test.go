package report

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// The whole pipeline against the real network and the real model: collect,
// summarize, render. Nothing else proves the prompt actually produces a brief
// worth reading.
//
// This one spends money, so it has its own switch rather than sharing
// MARKET_WATCH_LIVE with the free checks:
//
//	MARKET_WATCH_LIVE_LLM=1 go test ./internal/report -run TestLiveEndToEnd -v
func TestLiveEndToEndBrief(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Skip("set MARKET_WATCH_LIVE_LLM=1 to generate a real brief (this calls the paid API)")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	prefs := config.DefaultPrefs()
	fetcher := &feed.Fetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: os.Getenv("USER_AGENT"),
	}

	collected := feed.Collect(ctx, fetcher, prefs.EnabledSources(), prefs.Groups, 250)
	for _, e := range collected.Errors {
		t.Logf("source %s failed: %v", e.SourceID, e.Err)
	}
	if collected.AllFailed() {
		t.Fatal("every source failed; nothing to summarize")
	}
	t.Logf("collected %d articles (%d before dedupe)", len(collected.Articles), collected.Fetched)

	display, err := time.LoadLocation(config.DefaultDisplayTZ)
	if err != nil {
		t.Fatalf("load display timezone: %v", err)
	}

	generator := &Generator{
		Completer:       NewClaude(key, config.DefaultModel),
		DisplayLocation: display,
	}

	started := time.Now()
	rep, err := generator.Generate(ctx, collected.Articles, prefs.Groups)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	t.Logf("generated in %s", time.Since(started).Round(time.Second))
	t.Logf("usage: %d in, %d out, cache read %d, estimated $%.4f",
		rep.Usage.InputTokens, rep.Usage.OutputTokens, rep.Usage.CacheReadTokens, rep.Usage.EstimatedUSD)
	if rep.Usage.Total() == 0 {
		t.Error("no usage was recorded; the footer would silently omit it")
	}

	t.Logf("\n===== OVERVIEW =====\n%s", rep.Overview)
	for _, s := range rep.Sections {
		t.Logf("\n===== %s (%d articles) =====\n%s", s.GroupName, len(s.Articles), s.Body)
	}

	// The brief has to survive rendering too, or it never reaches the reader.
	messages := telegram.Render(rep, display)
	lengths := make([]int, len(messages))
	for i, m := range messages {
		lengths[i] = len([]rune(m))
	}
	t.Logf("rendered into %d telegram message(s), lengths %v", len(messages), lengths)

	// Delivery is the last link in the chain, and the only one whose failures
	// are invisible from here. It runs only with a chat configured, so the
	// check stays useful for anyone without one.
	chatID, err := strconv.ParseInt(os.Getenv("TELEGRAM_CHAT_ID"), 10, 64)
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	switch {
	case token == "" || err != nil:
		t.Log("TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID unset; skipping delivery")
	default:
		bot := telegram.New(token, &http.Client{Timeout: 30 * time.Second})
		if err := bot.SendReport(ctx, chatID, messages); err != nil {
			t.Fatalf("SendReport: %v", err)
		}
		t.Logf("delivered %d message(s) to chat %d", len(messages), chatID)
	}

	if rep.Overview == "" {
		t.Error("the brief has no overview")
	}
	for _, s := range rep.Sections {
		if strings.TrimSpace(s.Body) == "" {
			t.Errorf("section %s came back empty", s.GroupID)
		}
	}
}
