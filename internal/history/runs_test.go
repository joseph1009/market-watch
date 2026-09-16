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

	_ = r.Add(Run{At: day, Kept: 400, Matched: 200, CutImportant: 0, USD: 0.60})
	if got := r.Summary(time.UTC); !strings.Contains(got, "Nothing important lost to the cap") {
		t.Errorf("a clean run did not say so:\n%s", got)
	}

	_ = r.Add(Run{At: day.AddDate(0, 0, 1), Kept: 600, Matched: 300, CutImportant: 4, USD: 0.70})
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
		run := Run{At: day.AddDate(0, 0, i), Kept: 400, USD: 0.6, Failed: []string{"dead-feed"}}
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
