package triage

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
)

// TestLiveTriage runs triage over today's real feeds and prints what it did, so
// its placements can be read and judged rather than trusted. The risk worth
// watching is stretching: an article pushed into a watchlist it does not
// belong in would put noise into that section.
//
//	MARKET_WATCH_LIVE_LLM=1 go test ./internal/triage -run TestLiveTriage -v -timeout 15m
func TestLiveTriage(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Skip("set MARKET_WATCH_LIVE_LLM=1 to triage today's feeds (this calls the paid API)")
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
	articles = feed.DropStale(articles, now, feed.MaxArticleAge)
	articles = feed.Dedupe(articles, sources)
	matched := feed.Match(articles, prefs.Groups)

	tr := &Triager{Completer: NewClaude(key, config.DefaultTriageModel)}
	started := time.Now()
	triaged, usage, err := tr.Triage(ctx, matched, prefs.Groups)
	if err != nil {
		t.Errorf("triage: %v", err)
	}
	t.Logf("triaged %d articles in %s: %d in, %d out, estimated $%.4f",
		len(triaged), time.Since(started).Round(time.Second),
		usage.InputTokens, usage.OutputTokens, usage.EstimatedUSD)

	var counts [6]int
	for _, a := range triaged {
		counts[a.Rating]++
	}
	t.Logf("ratings: 5=%d 4=%d 3=%d 2=%d 1=%d unrated=%d",
		counts[5], counts[4], counts[3], counts[2], counts[1], counts[0])

	t.Log("")
	t.Log("placed in a watchlist that keywords missed:")
	placed := 0
	for i, a := range triaged {
		added := addedGroups(matched[i], a)
		if len(added) == 0 {
			continue
		}
		placed++
		t.Logf("  %d  %-28s %-20s %s", a.Rating, clip(strings.Join(added, ","), 27),
			clip(a.SourceName, 19), clip(a.Title, 80))
	}
	t.Logf("  (%d articles)", placed)

	t.Log("")
	t.Log("removed as trivial (unmatched, rated 1):")
	removed := 0
	for _, a := range triaged {
		if a.Rating == 1 && len(a.GroupIDs) == 0 {
			removed++
			if removed <= 150 {
				t.Logf("  %-20s %s", clip(a.SourceName, 19), clip(a.Title, 90))
			}
		}
	}
	if removed > 150 {
		t.Logf("  ... and %d more", removed-150)
	}

	// The same cut production makes, to show whether 600 is still binding.
	var kept []model.Article
	for _, a := range triaged {
		if !(a.Rating == 1 && len(a.GroupIDs) == 0) {
			kept = append(kept, a)
		}
	}
	ranked := feed.Limit(kept, sources, 0, now)
	t.Log("")
	if len(ranked) <= config.DefaultMaxArticles {
		t.Logf("%d articles after removing trivia, under the cap of %d", len(ranked), config.DefaultMaxArticles)
		return
	}
	t.Logf("%d articles after removing trivia; the cap of %d cuts these rated 4 or 5:", len(ranked), config.DefaultMaxArticles)
	for _, a := range ranked[config.DefaultMaxArticles:] {
		if a.Rating >= 4 {
			t.Logf("  %d  %-20s %s", a.Rating, clip(a.SourceName, 19), clip(a.Title, 90))
		}
	}
}

func addedGroups(before, after model.Article) []string {
	var added []string
	for _, id := range after.GroupIDs {
		if !before.InGroup(id) {
			added = append(added, id)
		}
	}
	return added
}
