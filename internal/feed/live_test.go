package feed

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// Feeds rot: they move, change format, or start refusing our User-Agent, and
// nothing in the unit tests would notice. This checks the real defaults against
// the real network, so it is opt-in rather than part of every run.
//
//	MARKET_WATCH_LIVE=1 go test ./internal/feed -run TestLive -v
func TestLiveDefaultSourcesStillParse(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to check the default feeds against the network")
	}
	// SEC needs the contact address in USER_AGENT and 403s without it. Loading
	// .env here means the check does not depend on how the shell was set up.
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sources := config.DefaultPrefs().EnabledSources()
	// SEC EDGAR needs a contact address, so the check runs with the same
	// USER_AGENT the deployment uses rather than the contact-free default.
	f := &Fetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: os.Getenv("USER_AGENT"),
	}

	articles, errs := f.Fetch(ctx, sources)
	for _, e := range errs {
		t.Errorf("source %s (%s) failed: %v", e.SourceID, e.SourceName, e.Err)
	}

	// Per-source yield matters more than the total: one prolific feed could
	// otherwise hide a source that now returns an empty document.
	counts := make(map[string]int, len(sources))
	for _, a := range articles {
		counts[a.SourceID]++
	}
	for _, s := range sources {
		if counts[s.ID] == 0 {
			t.Errorf("source %s (%s) parsed but yielded no articles", s.ID, s.Name)
			continue
		}
		t.Logf("%-16s %3d articles", s.ID, counts[s.ID])
	}

	reportClustering(t, articles, sources)
}

// reportClustering shows what headline matching actually did to today's feeds.
// A threshold picked from invented examples is a guess; this is the check that
// it collapses genuine duplicates without merging distinct stories.
func reportClustering(t *testing.T, articles []model.Article, sources []model.Source) {
	t.Helper()

	for _, threshold := range []float64{0.5, 0.55, 0.6, 0.65, 0.7} {
		weights := make(map[string]int, len(sources))
		for _, s := range sources {
			weights[s.ID] = s.Weight
		}
		got := dedupeByTitle(dedupeByURL(articles, weights), weights, threshold)

		merged := 0
		for _, a := range got {
			if a.Corroborations > 0 {
				merged++
			}
		}
		t.Logf("threshold %.2f: %d articles -> %d stories (%d had corroboration)",
			threshold, len(articles), len(got), merged)
	}

	// The clusters themselves, so a bad merge is visible rather than inferred.
	weights := make(map[string]int, len(sources))
	for _, s := range sources {
		weights[s.ID] = s.Weight
	}
	clusters := clusterByTitle(dedupeByURL(articles, weights), weights, DefaultSimilarity)

	shown := 0
	for _, c := range clusters {
		if len(c.members) < 2 || shown >= 15 {
			continue
		}
		shown++
		t.Logf("  cluster of %d across %d outlet(s):", len(c.members), len(c.sources))
		for _, m := range c.members {
			t.Logf("      %s", truncate(m, 88))
		}
	}
	if shown == 0 {
		t.Log("  no headlines merged at all")
	}
}
