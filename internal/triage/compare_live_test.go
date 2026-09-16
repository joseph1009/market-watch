package triage

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
)

// TestLiveCompareTriageModels rates the same articles with two models and
// prints only the decisions they disagree on: whether an article is placed in
// a watchlist, and whether it is removed as trivial. Those are the only two
// things a rating changes while the cap is not binding, so they are the only
// disagreements worth a reader's time.
//
//	MARKET_WATCH_LIVE_LLM=1 go test ./internal/triage -run TestLiveCompare -v -timeout 15m
//
// TRIAGE_COMPARE_A and TRIAGE_COMPARE_B override the two models.
func TestLiveCompareTriageModels(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Skip("set MARKET_WATCH_LIVE_LLM=1 to compare triage models (this calls the paid API)")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	modelA := envOr("TRIAGE_COMPARE_A", "claude-haiku-4-5")
	modelB := envOr("TRIAGE_COMPARE_B", "claude-sonnet-5")

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
	articles = feed.DropStale(articles, time.Now().UTC(), feed.MaxArticleAge)
	articles = feed.Dedupe(articles, sources)
	matched := feed.Match(articles, prefs.Groups)

	type run struct {
		model    string
		articles []model.Article
		usage    model.Usage
		took     time.Duration
		err      error
	}
	runs := []*run{{model: modelA}, {model: modelB}}
	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func(r *run) {
			defer wg.Done()
			started := time.Now()
			tr := &Triager{Completer: NewClaude(key, r.model)}
			r.articles, r.usage, r.err = tr.Triage(ctx, matched, prefs.Groups)
			r.took = time.Since(started)
		}(r)
	}
	wg.Wait()

	for _, r := range runs {
		if r.err != nil {
			t.Errorf("%s: %v", r.model, r.err)
		}
		var counts [6]int
		placed, removed := 0, 0
		for i, a := range r.articles {
			counts[a.Rating]++
			if len(addedGroups(matched[i], a)) > 0 {
				placed++
			}
			if isTrivial(a) {
				removed++
			}
		}
		t.Logf("%-18s %4s  $%.4f  ratings 5=%d 4=%d 3=%d 2=%d 1=%d unrated=%d  placed=%d removed=%d",
			r.model, r.took.Round(time.Second), r.usage.EstimatedUSD,
			counts[5], counts[4], counts[3], counts[2], counts[1], counts[0], placed, removed)
	}

	a, b := runs[0].articles, runs[1].articles
	exact, near := 0, 0
	for i := range matched {
		switch d := a[i].Rating - b[i].Rating; {
		case d == 0:
			exact++
			near++
		case d == 1 || d == -1:
			near++
		}
	}
	t.Logf("ratings identical on %d of %d (%.0f%%), within one on %d (%.0f%%)",
		exact, len(matched), pct(exact, len(matched)), near, pct(near, len(matched)))

	t.Log("")
	t.Logf("placement disagreements  (A = %s, B = %s)", modelA, modelB)
	n := 0
	for i := range matched {
		pa, pb := addedGroups(matched[i], a[i]), addedGroups(matched[i], b[i])
		if strings.Join(pa, ",") == strings.Join(pb, ",") {
			continue
		}
		n++
		t.Logf("  A %d %-26s B %d %-26s %-18s %s",
			a[i].Rating, clip(orNone(pa), 25), b[i].Rating, clip(orNone(pb), 25),
			clip(a[i].SourceName, 17), clip(a[i].Title, 85))
	}
	t.Logf("  (%d)", n)

	t.Log("")
	t.Log("removal disagreements (removed = unmatched and rated 1)")
	n = 0
	for i := range matched {
		ra, rb := isTrivial(a[i]), isTrivial(b[i])
		if ra == rb {
			continue
		}
		n++
		who := "A removes, B keeps"
		if rb {
			who = "B removes, A keeps"
		}
		t.Logf("  %-19s A %d  B %d  %-18s %s", who, a[i].Rating, b[i].Rating,
			clip(a[i].SourceName, 17), clip(a[i].Title, 85))
	}
	t.Logf("  (%d)", n)
}

func isTrivial(a model.Article) bool { return a.Rating == 1 && len(a.GroupIDs) == 0 }

func orNone(groups []string) string {
	if len(groups) == 0 {
		return "-"
	}
	return strings.Join(groups, ",")
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
