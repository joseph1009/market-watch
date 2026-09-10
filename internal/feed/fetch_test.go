package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func fixedNow() time.Time { return time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC) }

func testFetcher() *Fetcher {
	return &Fetcher{Now: fixedNow}
}

func TestFetchNormalizesItemsIntoArticles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != DefaultUserAgent {
			t.Errorf("User-Agent = %q, want %q (SEC rejects requests without one)", ua, DefaultUserAgent)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssSample))
	}))
	defer srv.Close()

	src := model.Source{ID: "cnbc", Name: "CNBC Markets", URL: srv.URL, Weight: 8, Enabled: true}
	articles, errs := testFetcher().Fetch(context.Background(), []model.Source{src})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(articles))
	}

	a := articles[0]
	if a.SourceID != "cnbc" || a.SourceName != "CNBC Markets" {
		t.Errorf("source identity not stamped: %+v", a)
	}
	if a.ID != model.ArticleID(a.URL) {
		t.Errorf("ID = %q, want the canonical-URL hash %q", a.ID, model.ArticleID(a.URL))
	}
	if !a.Fetched.Equal(fixedNow()) {
		t.Errorf("Fetched = %s, want %s", a.Fetched, fixedNow())
	}
}

// One bad feed is routine. It must cost its own stories and nothing else.
func TestFetchIsolatesAFailingSource(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssSample))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusInternalServerError)
	}))
	defer bad.Close()

	sources := []model.Source{
		{ID: "bad", Name: "Broken Feed", URL: bad.URL, Weight: 5},
		{ID: "good", Name: "CNBC", URL: good.URL, Weight: 8},
	}

	articles, errs := testFetcher().Fetch(context.Background(), sources)
	if len(articles) != 2 {
		t.Errorf("got %d articles, want the 2 from the healthy feed", len(articles))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if errs[0].SourceID != "bad" {
		t.Errorf("error attributed to %q, want \"bad\"", errs[0].SourceID)
	}
	if !strings.Contains(errs[0].Error(), "500") {
		t.Errorf("error %q does not name the status", errs[0].Error())
	}
}

// Article order must not depend on which server answers first, or the same news
// day would produce a different report on every run.
func TestFetchReturnsArticlesInSourceOrder(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write([]byte(`<rss><channel><item><title>Slow</title><link>https://example.com/slow</link></item></channel></rss>`))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>Fast</title><link>https://example.com/fast</link></item></channel></rss>`))
	}))
	defer fast.Close()

	sources := []model.Source{
		{ID: "slow", Name: "Slow", URL: slow.URL},
		{ID: "fast", Name: "Fast", URL: fast.URL},
	}

	articles, errs := testFetcher().Fetch(context.Background(), sources)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(articles) != 2 || articles[0].Title != "Slow" || articles[1].Title != "Fast" {
		t.Errorf("got %v, want the slow source's article first", titles(articles))
	}
}

func TestFetchResolvesRelativeLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><title>Relative</title><link>/markets/story</link></item></channel></rss>`))
	}))
	defer srv.Close()

	articles, _ := testFetcher().Fetch(context.Background(), []model.Source{{ID: "s", Name: "S", URL: srv.URL}})
	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(articles))
	}
	if want := srv.URL + "/markets/story"; articles[0].URL != want {
		t.Errorf("URL = %q, want %q", articles[0].URL, want)
	}
}

func TestFetchSubstitutesTheClockForMissingAndFutureDates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel>
		  <item><title>Undated</title><link>https://example.com/a</link></item>
		  <item><title>From the future</title><link>https://example.com/b</link>
		    <pubDate>Fri, 09 Sep 2044 12:00:00 +0000</pubDate></item>
		</channel></rss>`))
	}))
	defer srv.Close()

	articles, _ := testFetcher().Fetch(context.Background(), []model.Source{{ID: "s", Name: "S", URL: srv.URL}})
	if len(articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(articles))
	}
	for _, a := range articles {
		if !a.Published.Equal(fixedNow()) {
			t.Errorf("%q Published = %s, want the fetch time %s", a.Title, a.Published, fixedNow())
		}
	}
}

func TestFetchSkipsItemsWithNothingUsable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel>
		  <item><title>No link</title></item>
		  <item><link>https://example.com/no-title</link></item>
		  <item><title>Good</title><link>https://example.com/good</link></item>
		</channel></rss>`))
	}))
	defer srv.Close()

	articles, _ := testFetcher().Fetch(context.Background(), []model.Source{{ID: "s", Name: "S", URL: srv.URL}})
	if len(articles) != 1 || articles[0].Title != "Good" {
		t.Errorf("got %v, want only the complete item", titles(articles))
	}
}

func TestCollectRunsTheWholePipeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssSample))
	}))
	defer srv.Close()

	// Both sources serve the identical feed, so dedupe should halve the yield.
	sources := []model.Source{
		{ID: "cnbc", Name: "CNBC", URL: srv.URL, Weight: 8},
		{ID: "aggregator", Name: "Aggregator", URL: srv.URL, Weight: 4},
	}

	res := Collect(context.Background(), testFetcher(), Options{Sources: sources, Groups: testGroups(), Max: 10})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if res.Fetched != 4 {
		t.Errorf("Fetched = %d, want 4 before dedupe", res.Fetched)
	}
	if len(res.Articles) != 2 {
		t.Fatalf("got %d articles after dedupe, want 2", len(res.Articles))
	}
	for _, a := range res.Articles {
		if a.SourceID != "cnbc" {
			t.Errorf("%q kept from %q, want the heavier source cnbc", a.Title, a.SourceID)
		}
	}

	// Matching ran: the chip story names NVDA. The Fed story deliberately does
	// not match -- it says "rates", not "rate cut" -- and is kept anyway for the
	// general overview.
	if got := res.Articles[0]; !equalStrings(got.GroupIDs, []string{"semis-ai"}) {
		t.Errorf("%q GroupIDs = %v, want [semis-ai]", got.Title, got.GroupIDs)
	}
}

func TestCollectHonorsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	res := Collect(ctx, testFetcher(), Options{Sources: []model.Source{{ID: "s", Name: "S", URL: srv.URL}}, Max: 10})
	if !res.AllFailed() {
		t.Errorf("got %d articles, want the cancelled fetch to report failure", len(res.Articles))
	}
}

func titles(articles []model.Article) []string {
	out := make([]string, len(articles))
	for i, a := range articles {
		out[i] = a.Title
	}
	return out
}
