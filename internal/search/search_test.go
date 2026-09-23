package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

var testNow = time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC)

// stub answers searches from a table keyed by query text and records what it
// was sent.
type stub struct {
	mu       sync.Mutex
	requests []request
	auth     []string
	answers  map[string]string // query -> response body
	status   map[string]int    // query -> non-200 status
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	s.mu.Unlock()

	if code := s.status[req.Query]; code != 0 {
		w.WriteHeader(code)
		return
	}
	w.Write([]byte(s.answers[req.Query]))
}

func client(url string) *Client {
	return &Client{APIKey: "tvly-test", URL: url, Now: func() time.Time { return testNow }}
}

const chipResults = `{"results":[
	{"title":"TSMC lifts capex again - Reuters","url":"https://www.reuters.com/tech/tsmc-capex","content":"#### Markets\n\nTSMC raised its spending plan [...] citing AI demand.","published_date":"Wed, 23 Sep 2026 12:32:43 GMT"},
	{"title":"TSM Stock Price | Quote","url":"https://www.marketwatch.com/investing/stock/tsm","content":"Quote page","published_date":null},
	{"title":"Nvidia's next chip - Financial Times","url":"https://www.ft.com/content/abc","content":"Nvidia said...","published_date":"Wed, 23 Sep 2026 04:37:37 GMT"},
	{"title":"DIGITIMES Semiconductors news","url":"https://www.digitimes.com/topic/semiconductors","content":"Latest chip news.","published_date":"Wed, 23 Sep 2026 00:00:00 GMT"},
	{"title":"Post Market Wrap: September 23, 2026 - cnbc.com","url":"https://www.cnbc.com/video/2026/09/23/post-market-wrap-september-23-2026.html","content":"The day's close.","published_date":"Wed, 23 Sep 2026 19:19:00 GMT"}
],"usage":{"credits":1}}`

const marketResults = `{"results":[
	{"title":"TSMC lifts capex again - Reuters","url":"https://reuters.com/tech/tsmc-capex/?utm_source=x","content":"Same story, found twice.","published_date":"Wed, 23 Sep 2026 12:32:43 GMT"},
	{"title":"Stocks close higher | CNBC","url":"https://www.cnbc.com/2026/09/23/stocks.html","content":"The S&P 500 rose.","published_date":"Wed, 23 Sep 2026 20:05:00 GMT"}
],"usage":{"credits":1}}`

func TestCollectTurnsResultsIntoArticles(t *testing.T) {
	s := &stub{answers: map[string]string{"chips": chipResults, "markets": marketResults}}
	srv := httptest.NewServer(s)
	defer srv.Close()

	res := client(srv.URL).Collect(context.Background(),
		[]Query{{Label: "semis", Text: "chips"}, {Label: "markets", Text: "markets"}},
		testNow.Add(-24*time.Hour))

	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if res.Credits != 2 {
		t.Errorf("Credits = %d, want 2, one per search", res.Credits)
	}

	// The quote page has no date and is dropped, as are the section page and
	// the programme, which have dates but are not one story; the Reuters story
	// found by both searches is kept once.
	var titles []string
	for _, a := range res.Articles {
		titles = append(titles, a.Title)
	}
	want := []string{"TSMC lifts capex again", "Nvidia's next chip", "Stocks close higher"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %q, want %q", titles, want)
	}

	first := res.Articles[0]
	if first.SourceID != "web:reuters.com" || first.SourceName != "Reuters" {
		t.Errorf("source = %q / %q, want web:reuters.com / Reuters", first.SourceID, first.SourceName)
	}
	if first.ID != model.ArticleID("https://www.reuters.com/tech/tsmc-capex") {
		t.Errorf("ID is not the canonical address's, so the feeds' copy would not dedupe against it")
	}
	if first.Summary != "Markets TSMC raised its spending plan … citing AI demand." {
		t.Errorf("Summary = %q, want the page's heading marks and passage breaks cleaned", first.Summary)
	}
	if want := time.Date(2026, 9, 23, 12, 32, 43, 0, time.UTC); !first.Published.Equal(want) {
		t.Errorf("Published = %s, want %s", first.Published, want)
	}
	// Published after the clock says it is now: clamped, as the feeds are.
	if last := res.Articles[2]; !last.Published.Equal(testNow) {
		t.Errorf("a future date was kept as %s, want it clamped to now", last.Published)
	}
}

func TestCollectAsksForRestrictedNewsSearch(t *testing.T) {
	s := &stub{answers: map[string]string{"q": `{"results":[]}`}}
	srv := httptest.NewServer(s)
	defer srv.Close()

	client(srv.URL).Collect(context.Background(), []Query{{Label: "x", Text: "q"}}, testNow.Add(-24*time.Hour))

	if len(s.requests) != 1 {
		t.Fatalf("sent %d requests, want 1", len(s.requests))
	}
	req := s.requests[0]
	if s.auth[0] != "Bearer tvly-test" {
		t.Errorf("Authorization = %q, want the key as a bearer token", s.auth[0])
	}
	if req.Topic != "news" || req.SearchDepth != "basic" || req.MaxResults != maxResults || !req.IncludeUsage {
		t.Errorf("request = %+v, want a basic news search for %d results with usage", req, maxResults)
	}
	if len(req.IncludeDomains) != len(Outlets) {
		t.Errorf("restricted to %d domains, want all %d outlets", len(req.IncludeDomains), len(Outlets))
	}
	if req.TimeRange != "day" || req.StartDate != "" {
		t.Errorf("a day after the last brief: time_range=%q start_date=%q, want the past day", req.TimeRange, req.StartDate)
	}
}

// Monday's brief follows Friday's. Searching the past day would skip the
// weekend, so the search starts from the previous brief instead.
func TestCollectReachesBackToThePreviousBrief(t *testing.T) {
	s := &stub{answers: map[string]string{"q": `{"results":[]}`}}
	srv := httptest.NewServer(s)
	defer srv.Close()

	friday := time.Date(2026, 9, 18, 21, 0, 0, 0, time.UTC)
	c := client(srv.URL)
	c.Now = func() time.Time { return time.Date(2026, 9, 21, 21, 0, 0, 0, time.UTC) }
	c.Collect(context.Background(), []Query{{Label: "x", Text: "q"}}, friday)

	if got := s.requests[0]; got.StartDate != "2026-09-18" || got.TimeRange != "" {
		t.Errorf("time_range=%q start_date=%q, want start_date 2026-09-18", got.TimeRange, got.StartDate)
	}
}

func TestCollectKeepsTheOtherSearchesWhenOneFails(t *testing.T) {
	s := &stub{
		answers: map[string]string{"markets": marketResults},
		status:  map[string]int{"chips": 432},
	}
	srv := httptest.NewServer(s)
	defer srv.Close()

	res := client(srv.URL).Collect(context.Background(),
		[]Query{{Label: "semis", Text: "chips"}, {Label: "markets", Text: "markets"}},
		testNow.Add(-24*time.Hour))

	if len(res.Articles) != 2 {
		t.Errorf("got %d articles, want the 2 from the search that worked", len(res.Articles))
	}
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one", res.Errors)
	}
	if !errors.Is(res.Errors[0], ErrOutOfCredits) {
		t.Errorf("432 reported as %v, want ErrOutOfCredits", res.Errors[0])
	}
	if !strings.Contains(res.Errors[0].Error(), "semis") {
		t.Errorf("error %q does not name the search that failed", res.Errors[0])
	}
}

func TestCollectWithoutAKeyDoesNothing(t *testing.T) {
	var c *Client
	if res := c.Collect(context.Background(), General, testNow); len(res.Articles) != 0 || len(res.Errors) != 0 {
		t.Errorf("nil client returned %+v", res)
	}
	if res := (&Client{}).Collect(context.Background(), General, testNow); len(res.Articles) != 0 || len(res.Errors) != 0 {
		t.Errorf("keyless client returned %+v", res)
	}
}

func TestUsageReadsTheAccountAllowance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tvly-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"key":{"usage":0,"limit":null},"account":{"current_plan":"Researcher","plan_usage":42,"plan_limit":1000}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "tvly-test", UsageURL: srv.URL}
	got, err := c.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != (Usage{Plan: "Researcher", Used: 42, Limit: 1000}) {
		t.Errorf("Usage = %+v", got)
	}

	c.APIKey = "tvly-wrong"
	if _, err := c.Usage(context.Background()); err == nil || !strings.Contains(err.Error(), "TAVILY_API_KEY") {
		t.Errorf("a refused key gave %v, want an error naming TAVILY_API_KEY", err)
	}
}

func TestTrimOutletRemovesOnlyTheOutletsName(t *testing.T) {
	reuters := outletFor("https://www.reuters.com/x")
	ft := outletFor("https://www.ft.com/x")
	oil := outletFor("https://oilprice.com/x")
	bloomberg := outletFor("https://www.bloomberg.com/x")
	nyt := outletFor("https://www.nytimes.com/x")

	cases := []struct {
		title string
		o     Outlet
		want  string
	}{
		{"Oil falls on Gulf supply - Reuters", reuters, "Oil falls on Gulf supply"},
		{"Oil falls on Gulf supply - reuters.com", reuters, "Oil falls on Gulf supply"},
		{"China takes stock of Broadcom gear - Financial Times", ft, "China takes stock of Broadcom gear"},
		{"Hormuz shipments hit high - Bloomberg.com", bloomberg, "Hormuz shipments hit high"},
		{"Trump's Diesel Dilemma - The New York Times", nyt, "Trump's Diesel Dilemma"},
		// The outlet goes; the section name before it is the page's own.
		{"Oil set for losing streak - Crude Oil Prices Today | OilPrice.com", oil, "Oil set for losing streak - Crude Oil Prices Today"},
		// "ft" is inside "shift": whole words only.
		{"Supply chains - a structural shift", ft, "Supply chains - a structural shift"},
		{"No suffix at all", reuters, "No suffix at all"},
	}
	for _, c := range cases {
		if got := trimOutlet(c.title, c.o); got != c.want {
			t.Errorf("trimOutlet(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestOutletForMatchesSubdomainsAndNamesStrangers(t *testing.T) {
	if got := outletFor("https://markets.ft.com/data"); got.Name != "Financial Times" {
		t.Errorf("markets.ft.com = %q, want the Financial Times", got.Name)
	}
	if got := outletFor("https://asia.nikkei.com/Business"); got.Name != "Nikkei Asia" {
		t.Errorf("asia.nikkei.com = %q, want Nikkei Asia", got.Name)
	}
	// A host on no list ranks with no weight rather than borrowing one.
	got := outletFor("https://www.example.org/story")
	if got.Name != "example.org" || got.Weight != 0 || got.SourceID() != "web:example.org" {
		t.Errorf("stranger = %+v, want named by host with no weight", got)
	}
	// "notft.com" is not the FT.
	if got := outletFor("https://notft.com/x"); got.Name == "Financial Times" {
		t.Error("notft.com matched the Financial Times")
	}
}

func TestDomainLabel(t *testing.T) {
	for domain, want := range map[string]string{
		"reuters.com":          "reuters",
		"asia.nikkei.com":      "nikkei",
		"businesstimes.com.sg": "businesstimes",
		"japantimes.co.jp":     "japantimes",
		"ft.com":               "ft",
		"theblock.co":          "theblock",
	} {
		if got := domainLabel(domain); got != want {
			t.Errorf("domainLabel(%q) = %q, want %q", domain, got, want)
		}
	}
}

func TestOutletsAreDistinctAndWeighted(t *testing.T) {
	seen := map[string]bool{}
	for _, o := range Outlets {
		if seen[o.Domain] {
			t.Errorf("%s listed twice", o.Domain)
		}
		seen[o.Domain] = true
		if o.Weight < 1 || o.Weight > 10 || o.Name == "" {
			t.Errorf("%s: weight %d, name %q", o.Domain, o.Weight, o.Name)
		}
	}
	// Tavily's documented limit on include_domains.
	if len(Outlets) > 300 {
		t.Errorf("%d outlets, more than the 300 a search accepts", len(Outlets))
	}
}
