package feed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// fakeTriager rates every article through a function and places unmatched ones
// wherever place says.
type fakeTriager struct {
	rate  func(model.Article) int
	place string
	usage model.Usage
	err   error
}

func (f fakeTriager) Triage(_ context.Context, articles []model.Article, _ []model.Group) ([]model.Article, model.Usage, error) {
	out := make([]model.Article, len(articles))
	for i, a := range articles {
		a.Rating = f.rate(a)
		if f.place != "" && len(a.GroupIDs) == 0 {
			a.GroupIDs = []string{f.place}
		}
		out[i] = a
	}
	return out, f.usage, f.err
}

// collectSample runs Collect over rssSample: a chip story that matches semis-ai
// by ticker, and a Fed story that matches nothing.
func collectSample(t *testing.T, max int, tr Triager) Result {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssSample))
	}))
	t.Cleanup(srv.Close)

	sources := []model.Source{{ID: "cnbc", Name: "CNBC", URL: srv.URL, Weight: 8}}
	res := Collect(context.Background(), testFetcher(), Options{
		Sources: sources, Groups: testGroups(), Max: max, Triage: tr,
	})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	return res
}

func always(n int) func(model.Article) int { return func(model.Article) int { return n } }

func TestCollectRemovesUnmatchedArticlesRatedOne(t *testing.T) {
	res := collectSample(t, 10, fakeTriager{rate: always(1)})

	if len(res.Articles) != 1 || !res.Articles[0].InGroup("semis-ai") {
		t.Fatalf("got %v, want only the keyword-matched chip story", titles(res.Articles))
	}
	if res.Trivial != 1 {
		t.Errorf("Trivial = %d, want 1", res.Trivial)
	}
	if res.Dropped != 0 {
		t.Errorf("Dropped = %d, want 0: trivia is not the cap's doing", res.Dropped)
	}
}

func TestCollectCountsWhatTriagePlaced(t *testing.T) {
	res := collectSample(t, 10, fakeTriager{rate: always(4), place: "macro-rates"})

	if len(res.Articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(res.Articles))
	}
	if res.NewlyMatched != 1 || res.Placements != 1 {
		t.Errorf("NewlyMatched = %d, Placements = %d; want 1 and 1", res.NewlyMatched, res.Placements)
	}
	if res.Matched != 2 {
		t.Errorf("Matched = %d, want both articles once triage placed the Fed story", res.Matched)
	}
	if res.Rated != 2 {
		t.Errorf("Rated = %d, want 2", res.Rated)
	}
}

// The monitoring number: a cap that keeps cutting 4s and 5s is set too low.
func TestCollectCountsImportantArticlesTheCapCut(t *testing.T) {
	res := collectSample(t, 1, fakeTriager{rate: always(5)})

	if res.Dropped != 1 || res.CutImportant != 1 {
		t.Errorf("Dropped = %d, CutImportant = %d; want 1 and 1", res.Dropped, res.CutImportant)
	}
}

func TestCollectCarriesOnWhenTriageFails(t *testing.T) {
	failed := fakeTriager{rate: always(0), err: errors.New("overloaded")}
	res := collectSample(t, 10, failed)

	if res.TriageErr == nil {
		t.Error("the triage failure was not reported")
	}
	if len(res.Articles) != 2 || res.Rated != 0 || res.Trivial != 0 {
		t.Errorf("got %d articles, %d rated, %d trivial; want both kept unrated",
			len(res.Articles), res.Rated, res.Trivial)
	}
}

func TestCollectWithoutTriageReportsNoTriage(t *testing.T) {
	res := collectSample(t, 10, nil)
	if res.Rated != 0 || res.TriageErr != nil || res.TriageUsage.Total() != 0 {
		t.Errorf("triage fields set without a triager: %+v", res)
	}
}

// The reason triage exists: a vital story from a lighter outlet must beat
// routine paperwork from a primary source.
func TestRatingOutranksSourceWeight(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "notice", SourceID: "sec-8k", Title: "Notice of sanctions actions", Published: now, Rating: 1},
		{ID: "shock", SourceID: "aggregator", Title: "Drone strikes hit Saudi pipeline", Published: now, Rating: 5},
	}

	got := Limit(articles, testSources(), 1, now)
	if got[0].ID != "shock" {
		t.Errorf("kept %q, want the rated-5 story over the rated-1 notice", got[0].ID)
	}
}

// Unrated articles land between well-rated and poorly-rated ones, so a failed
// batch neither buries its articles nor lifts them above what triage vouched for.
func TestUnratedArticlesRankBetweenHighAndLowRatings(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "low", SourceID: "cnbc", Published: now, Rating: 2},
		{ID: "unrated", SourceID: "cnbc", Published: now},
		{ID: "high", SourceID: "cnbc", Published: now, Rating: 4},
	}

	got := Limit(articles, testSources(), 0, now)
	if order := []string{got[0].ID, got[1].ID, got[2].ID}; !equalStrings(order, []string{"high", "unrated", "low"}) {
		t.Errorf("order = %v, want [high unrated low]", order)
	}
}
