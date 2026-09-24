package fundamentals

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/sec"
)

// TestLiveFundamentals reads one company's real filings.
//
//	MARKET_WATCH_LIVE=1 FUNDAMENTALS_TICKER=NVDA go test ./internal/fundamentals -run TestLiveFundamentals -v
//
// With PROMPT_OUT=<dir> it saves what /analyse would send the model -- the
// instructions and the opening message -- without sending it, so the input
// can be read without running anything. To have it written, use a relay run:
// see docs/RUNBOOK.md.
func TestLiveFundamentals(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to read real filings")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "NVDA"
	}
	agent := os.Getenv("USER_AGENT")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := &Client{
		Lookup:    &sec.Client{HTTP: &http.Client{Timeout: 30 * time.Second}, UserAgent: agent},
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: agent,
	}

	started := time.Now()
	snap, err := client.Fetch(ctx, ticker, 5)
	if err != nil {
		t.Fatalf("Fetch %s: %v", ticker, err)
	}
	filings := &sec.Client{HTTP: &http.Client{Timeout: 30 * time.Second}, UserAgent: agent}
	for _, problem := range AddBusiness(ctx, filings, &snap, time.Now()) {
		t.Logf("business description: %v", problem)
	}
	if key := os.Getenv("FINNHUB_API_KEY"); key != "" {
		quotes, _ := (&prices.Client{APIKey: key, HTTP: &http.Client{Timeout: 20 * time.Second}}).
			Fetch(ctx, []string{ticker})
		if len(quotes) > 0 {
			snap.Price = &quotes[0]
		}
	}
	addMarket(ctx, t, &snap, ticker)
	t.Logf("read %s in %s", ticker, time.Since(started).Round(time.Millisecond))
	t.Logf("\n%s", snap.Table())

	if dir := os.Getenv("PROMPT_OUT"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("PROMPT_OUT: %v", err)
		}
		files := map[string]string{
			"system.txt": systemPrompt,
			"prompt.txt": (&Analyzer{}).prompt(snap),
		}
		for name, text := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
				t.Fatalf("PROMPT_OUT: %v", err)
			}
		}
		t.Logf("saved the instructions and opening message to %s", dir)
	}

	if len(snap.Years) == 0 {
		t.Error("no fiscal years came back")
	}
	if snap.Balance.AsOf.IsZero() {
		t.Error("no balance sheet date came back")
	}

}

// addMarket attaches the two things that do not come from EDGAR: what the
// share has been doing, and what has been written about the company. Both are
// best-effort in the bot and best-effort here, so a live run still exercises
// the filings when one of them is unavailable.
func addMarket(ctx context.Context, t *testing.T, snap *Snapshot, ticker string) {
	t.Helper()
	now := time.Now().UTC()

	history := &prices.History{HTTP: &http.Client{Timeout: 25 * time.Second}}
	if series, err := history.Fetch(ctx, ticker); err != nil {
		t.Logf("price history: %v", err)
	} else {
		trading := prices.Summarise(series, now)
		snap.Trading = &trading
		t.Logf("price history: %d sessions to %s, last %.2f %s",
			trading.Days, trading.AsOf.Format(time.DateOnly), trading.Last, trading.Currency)
	}

	key := os.Getenv("FINNHUB_API_KEY")
	if key == "" {
		return
	}
	press := &prices.News{APIKey: key, HTTP: &http.Client{Timeout: 25 * time.Second}}
	if err := AddNews(ctx, press, snap, now); err != nil {
		t.Logf("company news: %v", err)
		return
	}
	t.Logf("company news: %d headlines kept", len(snap.News))
	for _, a := range snap.News {
		t.Logf("  %s — %s, %s", a.Title, a.SourceName, a.Published.Format(time.DateOnly))
	}
}
