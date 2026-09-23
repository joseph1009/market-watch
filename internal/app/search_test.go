package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/search"
)

func TestRecordSearchSortsCitationsByWhoFoundThem(t *testing.T) {
	onlySearched := model.Article{ID: "1", SourceID: "web:reuters.com"}
	both := model.Article{ID: "2", SourceID: "cnbc-markets", Also: []string{"web:cnbc.com"}}
	onlyFed := model.Article{ID: "3", SourceID: "fed-speeches"}
	onlyCNBC := model.Article{ID: "4", SourceID: "cnbc-top", Also: []string{"yahoo-finance"}}

	found := search.Result{Articles: make([]model.Article, 180), Credits: 15}
	var run history.Run
	recordSearch(&run, found,
		[]model.Article{onlySearched, both, onlyFed, onlyCNBC},
		[]model.Article{onlySearched, both, onlyFed, onlyCNBC})

	if run.Searched != 180 || run.SearchCredits != 15 {
		t.Errorf("Searched=%d credits=%d, want 180 and 15", run.Searched, run.SearchCredits)
	}
	if run.SearchOnly != 1 || run.CitedSearchOnly != 1 || run.Cited != 4 {
		t.Errorf("SearchOnly=%d CitedSearchOnly=%d Cited=%d, want 1, 1, 4", run.SearchOnly, run.CitedSearchOnly, run.Cited)
	}
	// A story both routes carried is neither search's alone nor missed by it.
	want := map[string]int{"fed-speeches": 1, "cnbc-top": 1}
	if len(run.MissedBySearch) != len(want) || run.MissedBySearch["fed-speeches"] != 1 || run.MissedBySearch["cnbc-top"] != 1 {
		t.Errorf("MissedBySearch = %v, want %v", run.MissedBySearch, want)
	}
	if len(run.Failed) != 0 {
		t.Errorf("Failed = %v after a clean search", run.Failed)
	}
}

// A search that found nothing -- out of credits, or down -- is a failure to
// record, not evidence that it misses every story.
func TestRecordSearchMakesNoComparisonWhenSearchFoundNothing(t *testing.T) {
	var run history.Run
	recordSearch(&run, search.Result{Errors: []error{errors.New("x"), errors.New("y")}}, nil,
		[]model.Article{{ID: "1", SourceID: "cnbc-top"}})

	if len(run.Failed) != 1 || run.Failed[0] != searchFailure {
		t.Errorf("Failed = %v, want one entry for the searches", run.Failed)
	}
	if run.Cited != 0 || len(run.MissedBySearch) != 0 {
		t.Errorf("Cited=%d MissedBySearch=%v, want no comparison", run.Cited, run.MissedBySearch)
	}
}

func TestSearchStartsFromThePreviousBriefWithinLimits(t *testing.T) {
	now := time.Date(2026, 9, 21, 21, 0, 0, 0, time.UTC) // a Monday
	runs, err := history.LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Runs: runs, Now: func() time.Time { return now }}

	// No brief yet: the past day.
	if got := a.searchSince(); !got.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("with no history, since = %s, want a day back", got)
	}

	// Friday's brief: Monday's search reaches back to it.
	friday := time.Date(2026, 9, 18, 21, 5, 0, 0, time.UTC)
	_ = runs.Add(history.Run{At: friday})
	if got := a.searchSince(); !got.Equal(friday) {
		t.Errorf("after Friday's brief, since = %s, want %s", got, friday)
	}

	// A rerun an hour after a brief still searches the day, not the hour.
	_ = runs.Add(history.Run{At: now.Add(-time.Hour)})
	if got := a.searchSince(); !got.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("an hour after a brief, since = %s, want a day back", got)
	}
}

func TestSearchNeverReachesPastTheArticleWindow(t *testing.T) {
	now := time.Date(2026, 9, 21, 21, 0, 0, 0, time.UTC)
	runs, err := history.LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = runs.Add(history.Run{At: now.AddDate(0, 0, -20)})
	a := &App{Runs: runs, Now: func() time.Time { return now }}

	if got := a.searchSince(); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Errorf("after a three-week gap, since = %s, want a week back", got)
	}
}

// The whole path: a search result travels with the feeds into the brief, can
// be cited, and the run record says which citation came from where.
func TestABriefCarriesSearchResultsAndRecordsThem(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Fed holds rates steady in September</title><link>https://feed.example/fed</link>
<description>The Fed held.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()

	// Searches run four at a time, so the count is shared between goroutines.
	var searches atomic.Int32
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches.Add(1)
		w.Write([]byte(`{"results":[{"title":"Nvidia unveils a new chip - Reuters",
			"url":"https://www.reuters.com/tech/nvidia-chip","content":"Nvidia said...",
			"published_date":"Thu, 10 Sep 2026 09:00:00 GMT"}],"usage":{"credits":1}}`))
	}))
	defer searchSrv.Close()

	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Search = &search.Client{APIKey: "tvly-test", URL: searchSrv.URL, Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nThe Fed held [1] and Nvidia launched a chip [2].\n"}}
	runs, err := history.LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.Runs = runs

	done, err := a.sendReport(context.Background())
	if err != nil {
		t.Fatalf("sendReport: %v", err)
	}

	var names []string
	for _, c := range done.rep.Cited {
		names = append(names, c.SourceName)
	}
	if got := strings.Join(names, ","); !strings.Contains(got, "Reuters") || !strings.Contains(got, "Stub") {
		t.Fatalf("the brief was offered %s, want the feed's story and the search's", got)
	}

	got := a.Runs.All()
	if len(got) != 1 {
		t.Fatalf("%d runs recorded, want 1", len(got))
	}
	run := got[0]
	if want := len(search.Queries(a.prefs.Groups)); int(searches.Load()) != want || run.SearchCredits != want {
		t.Errorf("searches=%d credits=%d, want %d each", searches.Load(), run.SearchCredits, want)
	}
	// Every search returned the same article: counted once.
	if run.Searched != 1 || run.SearchOnly != 1 {
		t.Errorf("Searched=%d SearchOnly=%d, want 1 and 1", run.Searched, run.SearchOnly)
	}
	if run.Cited != 2 || run.CitedSearchOnly != 1 || run.MissedBySearch["stub-feed"] != 1 {
		t.Errorf("Cited=%d CitedSearchOnly=%d MissedBySearch=%v, want 2, 1 and the feed's story missed",
			run.Cited, run.CitedSearchOnly, run.MissedBySearch)
	}
}

// briefStub answers the brief's one call with a fixed reply.
type briefStub struct{ reply string }

func (s briefStub) Complete(context.Context, string, string) (report.Completion, error) {
	return report.Completion{Text: s.reply}, nil
}
