package history

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempRuns(t *testing.T) *Runs {
	t.Helper()
	r, err := LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	return r
}

func TestSummaryAnswersWhetherTheCapIsCuttingAnything(t *testing.T) {
	r := tempRuns(t)
	day := time.Date(2026, 9, 14, 20, 30, 0, 0, time.UTC)

	_ = r.Add(Run{At: day, Kept: 400, Matched: 200, CutImportant: 0})
	if got := r.Summary(time.UTC); !strings.Contains(got, "Nothing important lost to the cap") {
		t.Errorf("a clean run did not say so:\n%s", got)
	}

	_ = r.Add(Run{At: day.AddDate(0, 0, 1), Kept: 600, Matched: 300, CutImportant: 4})
	got := r.Summary(time.UTC)
	if !strings.Contains(got, "cut 4 article(s) rated 4 or 5") {
		t.Errorf("the cap warning is missing:\n%s", got)
	}
	if !strings.Contains(got, "MAX_ARTICLES") {
		t.Errorf("the warning does not say what to change:\n%s", got)
	}
}

// A source that fails once had an outage; one that fails most days has moved or
// died, and is the one worth naming.
func TestSummaryNamesOnlyPersistentSourceFailures(t *testing.T) {
	r := tempRuns(t)
	day := time.Date(2026, 9, 14, 20, 30, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		run := Run{At: day.AddDate(0, 0, i), Kept: 400, Failed: []string{"dead-feed"}}
		if i == 0 {
			run.Failed = append(run.Failed, "blipped-once")
		}
		_ = r.Add(run)
	}

	got := r.Summary(time.UTC)
	if !strings.Contains(got, "dead-feed") {
		t.Errorf("a feed failing every run was not named:\n%s", got)
	}
	if strings.Contains(got, "blipped-once") {
		t.Errorf("a single outage was reported as a persistent failure:\n%s", got)
	}
}

func TestRunsAreCappedAndNewestFirst(t *testing.T) {
	r := tempRuns(t)
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < RunsKept+5; i++ {
		_ = r.Add(Run{At: day.AddDate(0, 0, i), Kept: i})
	}

	all := r.All()
	if len(all) != RunsKept {
		t.Errorf("kept %d runs, want %d", len(all), RunsKept)
	}
	if all[0].At.Before(all[1].At) {
		t.Error("runs are not newest first")
	}
}

func TestSummaryWithNoHistorySaysSo(t *testing.T) {
	if got := tempRuns(t).Summary(time.UTC); !strings.Contains(got, "No briefs recorded yet") {
		t.Errorf("got %q", got)
	}
}

// Widening a watchlist from a list of tickers to a sector can only be judged by
// reading what it caught, which means /stats has to show a few of them.
func TestSummaryShowsWhatJudgmentPlaced(t *testing.T) {
	r := tempRuns(t)
	day := time.Date(2026, 9, 20, 20, 30, 0, 0, time.UTC)

	_ = r.Add(Run{At: day, Kept: 300, Matched: 150, Placed: 40})
	_ = r.Add(Run{At: day.AddDate(0, 0, 1), Kept: 300, Matched: 150, Placed: 60,
		PlacedExamples: []string{"Saudi Arabia cuts Europe off from October crude → Energy"}})

	got := r.Summary(time.UTC)
	if !strings.Contains(got, "50 placed by judgment") {
		t.Errorf("the average is missing:\n%s", got)
	}
	if !strings.Contains(got, "Saudi Arabia cuts Europe off") {
		t.Errorf("the examples are missing:\n%s", got)
	}
}

// Headlines come from news feeds, and a run of them is written into a message
// Telegram parses as HTML. One ampersand would otherwise cost the whole reply.
func TestSummaryEscapesHeadlines(t *testing.T) {
	r := tempRuns(t)
	_ = r.Add(Run{
		At:             time.Date(2026, 9, 21, 20, 30, 0, 0, time.UTC),
		Placed:         1,
		PlacedExamples: []string{"Johnson & Johnson wins <appeal> → Healthcare & Pharma"},
	})

	got := r.Summary(time.UTC)
	if strings.Contains(got, "<appeal>") || strings.Contains(got, "Johnson & Johnson") {
		t.Errorf("a headline reached the message unescaped:\n%s", got)
	}
	if !strings.Contains(got, "Johnson &amp; Johnson wins &lt;appeal&gt;") {
		t.Errorf("the headline is not readable once escaped:\n%s", got)
	}
}

// The search block is the evidence for turning the media feeds off, so it has
// to say what search missed and by whom, and stay out of the way until search
// has run.
func TestSummaryComparesSearchWithTheFeeds(t *testing.T) {
	r := tempRuns(t)
	day := time.Date(2026, 9, 14, 20, 30, 0, 0, time.UTC)

	_ = r.Add(Run{At: day, Kept: 400})
	if got := r.Summary(time.UTC); strings.Contains(got, "News search") {
		t.Errorf("a record with no searches reported on search:\n%s", got)
	}

	_ = r.Add(Run{At: day.AddDate(0, 0, 1), Kept: 400, Searched: 180, SearchCredits: 15,
		SearchOnly: 30, Cited: 60, CitedSearchOnly: 9,
		MissedBySearch: map[string]int{"bls-cpi": 2, "cnbc-markets": 3}})
	_ = r.Add(Run{At: day.AddDate(0, 0, 2), Kept: 400, Searched: 220, SearchCredits: 15,
		SearchOnly: 40, Cited: 40, CitedSearchOnly: 5,
		MissedBySearch: map[string]int{"cnbc-markets": 1, "fed-speeches": 1}})

	got := r.Summary(time.UTC)
	for _, want := range []string{
		"News search, over 2 brief(s)",
		"200 articles found per brief, for 15 credits",
		"35 kept stories per brief that no feed carried",
		"14 of 100 cited stories came from search alone",
		// Most-missed first, ties by name.
		"cnbc-markets 4, bls-cpi 2, fed-speeches 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}
