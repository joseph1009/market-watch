package discover

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/triage"
)

// TestLiveDiscover runs the pass over today's real feeds and prints what it
// would put in front of the reader, with the verification result for each
// ticker.
//
//	MARKET_WATCH_LIVE_LLM=1 go test ./internal/discover -run TestLiveDiscover -v -timeout 15m
func TestLiveDiscover(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Skip("set MARKET_WATCH_LIVE_LLM=1 to search today's news (this calls the paid API)")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	prefs, err := config.LoadPrefs("../../data/prefs.yaml")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	sources := prefs.EnabledSources()
	fetcher := &feed.Fetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: os.Getenv("USER_AGENT"),
	}

	articles, _ := fetcher.Fetch(ctx, sources)
	now := time.Now().UTC()
	articles = feed.Dedupe(feed.DropStale(articles, now, feed.MaxArticleAge), sources)
	articles = feed.Match(articles, prefs.Groups)

	rater := &triage.Triager{Completer: triage.NewClaude(key, config.DefaultTriageModel)}
	rated, _, err := rater.Triage(ctx, articles, prefs.Groups)
	if err != nil {
		t.Logf("triage incomplete: %v", err)
	}

	var known []string
	for _, g := range prefs.Groups {
		known = append(known, g.Tickers...)
		known = append(known, g.Names...)
	}

	completer := triage.NewClaude(key, config.DefaultTriageModel)
	completer.MaxTokens = ReplyTokens
	f := &Finder{
		Completer: completer,
		Verifier:  &FIGI{HTTP: &http.Client{Timeout: 30 * time.Second}},
	}

	started := time.Now()
	found, usage, err := f.Find(ctx, rated, known)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	t.Logf("read %d articles in %s: %d in, %d out, estimated $%.4f",
		len(worthReading(rated)), time.Since(started).Round(time.Second),
		usage.InputTokens, usage.OutputTokens, usage.EstimatedUSD)

	t.Logf("%d names cleared the bar:", len(found))
	for _, c := range found {
		symbol := c.Symbol()
		switch {
		case c.Private:
			symbol = "private"
		case symbol == "":
			symbol = "unverified"
		}
		t.Logf("  %-14s %-26s %d outlets, best rating %d", symbol, clip(c.Name, 25), c.Sources, c.Rating)
		t.Logf("                 %s", clip(c.Why, 100))
		if c.Listed != "" {
			t.Logf("                 verified as %s", c.Listed)
		}
	}
}
