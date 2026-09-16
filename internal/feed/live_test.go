package feed

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
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

// TestLiveInspectTheCut shows what the article cap is actually discarding.
//
// Raising the cap is a guess until you have read the articles it would let
// through. These are the ones ranked just below the line, with the source that
// carried them and whether any watchlist claimed them.
//
//	MARKET_WATCH_LIVE=1 go test ./internal/feed -run TestLiveInspect -v
func TestLiveInspectTheCut(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to inspect the cut")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	prefs, err := config.LoadPrefs("../../data/prefs.yaml")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	sources := prefs.EnabledSources()
	fetcher := &Fetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: os.Getenv("USER_AGENT"),
	}

	articles, _ := fetcher.Fetch(ctx, sources)
	now := time.Now().UTC()
	articles = DropStale(articles, now, MaxArticleAge)
	articles = Dedupe(articles, sources)
	articles = Match(articles, prefs.Groups)

	cap := config.DefaultMaxArticles
	ranked := Limit(articles, sources, 0, now) // 0 = rank everything, drop nothing
	if len(ranked) <= cap {
		t.Logf("%d articles, under the cap of %d -- nothing is being dropped", len(ranked), cap)
		return
	}

	dropped := ranked[cap:]
	matched := 0
	bySource := map[string]int{}
	for _, a := range dropped {
		if len(a.GroupIDs) > 0 {
			matched++
		}
		bySource[a.SourceName]++
	}

	t.Logf("%d ranked, %d kept, %d dropped -- %d of the dropped matched a watchlist",
		len(ranked), cap, len(dropped), matched)

	type row struct {
		name string
		n    int
	}
	var rows []row
	for name, n := range bySource {
		rows = append(rows, row{name, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	t.Log("dropped by source:")
	for i, r := range rows {
		if i >= 12 {
			break
		}
		t.Logf("  %-28s %3d", r.name, r.n)
	}

	t.Log("the 15 closest to making the cut:")
	for i, a := range dropped {
		if i >= 15 {
			break
		}
		tag := "unmatched"
		if len(a.GroupIDs) > 0 {
			tag = strings.Join(a.GroupIDs, ",")
		}
		t.Logf("  [%-18s] %-22s %s", tag, truncate(a.SourceName, 22), truncate(a.Title, 70))
	}
}
