package prices

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
)

// TestLivePrices checks the key against the real service, and reports which of
// the reader's tickers it can actually quote.
//
//	MARKET_WATCH_LIVE=1 go test ./internal/prices -run TestLivePrices -v -timeout 15m
func TestLivePrices(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to check prices against the network")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("FINNHUB_API_KEY")
	if key == "" {
		t.Skip("FINNHUB_API_KEY is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	c := &Client{APIKey: key, HTTP: &http.Client{Timeout: 20 * time.Second}}

	quotes, missed := c.Fetch(ctx, BenchmarkSymbols())
	t.Logf("benchmarks: %d read, %d missing", len(quotes), len(missed))
	for _, q := range quotes {
		t.Logf("  %-32s %9.2f  %6s", LabelFor(q.Symbol), q.Price, q.Move())
	}
	for _, m := range missed {
		t.Errorf("benchmark %s returned no quote", m)
	}

	// The reader's own tickers, to find out what the free tier will not quote.
	prefs, err := config.LoadPrefs("../../data/prefs.yaml")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	var watched []string
	for _, g := range prefs.Groups {
		watched = append(watched, g.Tickers...)
	}
	if len(watched) > maxSymbols {
		watched = watched[:maxSymbols]
	}

	got, absent := c.Fetch(ctx, watched)
	t.Logf("watchlist: %d of %d quoted", len(got), len(watched))
	if len(absent) > 0 {
		t.Logf("  no quote for: %v", absent)
	}
}
