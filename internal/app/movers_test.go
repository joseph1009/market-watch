package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/search"
)

// What is searched for is the move a share made on its own. With the market
// down 3%, a share down 3.5% went with it; one down 8% or up 1% did not.
func TestMoversAreMeasuredAgainstTheMarket(t *testing.T) {
	now := time.Date(2026, 9, 23, 20, 30, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	closed := now.Add(-4 * time.Hour)
	quotes := []model.Quote{
		{Symbol: "SPY", Percent: -3.0, AsOf: closed},
		{Symbol: "XLE", Percent: -7.0, AsOf: closed}, // a benchmark, not a watchlist share
		{Symbol: "AAA", Percent: -3.5, AsOf: closed},
		{Symbol: "BBB", Percent: -8.0, AsOf: closed},
		{Symbol: "CCC", Percent: 1.0, AsOf: closed},
		{Symbol: "DDD", Percent: -6.0, AsOf: closed},
		{Symbol: "EEE", Percent: -10.0, AsOf: since.Add(-time.Hour)}, // the session before
	}

	got := model.Moves(movers(quotes, []string{"AAA", "BBB", "CCC", "DDD", "EEE"}, since))
	if want := "BBB -8.0% · CCC +1.0% · DDD -6.0%"; got != want {
		t.Errorf("movers = %q, want %q: furthest from the market first", got, want)
	}
}

func TestMoversAreCapped(t *testing.T) {
	var quotes []model.Quote
	var watched []string
	for i := 1; i <= 8; i++ {
		sym := fmt.Sprintf("S%d", i)
		quotes = append(quotes, model.Quote{Symbol: sym, Percent: float64(-3 - i)})
		watched = append(watched, sym)
	}
	got := movers(quotes, watched, time.Time{})
	if len(got) != search.MaxMoverQueries || got[0].Symbol != "S8" {
		t.Errorf("movers = %s, want the %d furthest, furthest first", model.Moves(got), search.MaxMoverQueries)
	}
}

// The whole path: every watchlist share is priced, the one that moved on its
// own gets a search of its own, and the brief is given all the prices.
func TestABriefPricesTheWatchlistAndSearchesForTheMover(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242
	closed := a.now().Add(-time.Hour)

	var (
		mu     sync.Mutex
		priced = map[string]bool{}
	)
	quoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sym := r.URL.Query().Get("symbol")
		mu.Lock()
		priced[sym] = true
		mu.Unlock()
		dp := 0.2
		switch sym {
		case "SPY":
			dp = -0.7
		case "MCD":
			dp = -4.8
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"c": 100.0, "pc": 100.0, "dp": dp, "t": closed.Unix()})
	}))
	defer quoteSrv.Close()

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Fed holds rates steady</title><link>https://feed.example/fed</link>
<description>The Fed held.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()

	var queries []string
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		queries = append(queries, req.Query)
		mu.Unlock()
		w.Write([]byte(`{"results":[],"usage":{"credits":1}}`))
	}))
	defer searchSrv.Close()

	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Quotes = &prices.Client{APIKey: "k", HTTP: quoteSrv.Client(), URL: quoteSrv.URL, Pause: time.Millisecond}
	a.Search = &search.Client{APIKey: "tvly-test", URL: searchSrv.URL, Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nThe Fed held [1].\n"}}
	runs, err := history.LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.Runs = runs

	if _, err := a.sendReport(context.Background()); err != nil {
		t.Fatalf("sendReport: %v", err)
	}

	for _, sym := range append(prices.BenchmarkSymbols(), watchedTickers(a.prefs.Groups)...) {
		if !priced[sym] {
			t.Errorf("%s was not priced; every benchmark and watchlist share should be", sym)
		}
	}
	var movers []string
	for _, q := range queries {
		if strings.HasPrefix(q, "Why did") {
			movers = append(movers, q)
		}
	}
	if len(movers) != 1 || movers[0] != "Why did McDonald's (MCD) shares fall?" {
		t.Errorf("mover searches = %q, want one, for McDonald's", movers)
	}
	if got, want := len(a.Generator.Quotes), len(priced); got != want {
		t.Errorf("the brief was given %d prices, want all %d read", got, want)
	}
	if run := a.Runs.All()[0]; run.SearchCredits != len(search.Queries(a.prefs.Groups))+1 {
		t.Errorf("credits = %d, want the regular searches and one for the mover", run.SearchCredits)
	}
}
