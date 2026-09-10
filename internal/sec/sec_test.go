package sec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const tickerIndexBody = `{"0":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc."},
"1":{"cik_str":1045810,"ticker":"NVDA","title":"NVIDIA CORP"}}`

// A submissions response carrying one material filing, one routine one, and one
// too old to count.
const submissionsBody = `{
 "cik":"320193","name":"Apple Inc.",
 "filings":{"recent":{
  "accessionNumber":["0000320193-26-000101","0000320193-26-000102","0000320193-26-000050"],
  "filingDate":["2026-09-09","2026-09-09","2026-01-04"],
  "form":["8-K","8-K","8-K"],
  "items":["2.02,9.01","7.01","5.02"],
  "primaryDocument":["a8k.htm","b8k.htm","c8k.htm"]}}}`

func newStub(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent; SEC answers that with 403")
		}
		switch {
		case strings.Contains(r.URL.Path, "company_tickers"):
			_, _ = w.Write([]byte(tickerIndexBody))
		default:
			_, _ = w.Write([]byte(submissionsBody))
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{
		HTTP:           srv.Client(),
		UserAgent:      "test contact@example.com",
		TickerIndexURL: srv.URL + "/files/company_tickers.json",
		SubmissionsURL: srv.URL + "/submissions/CIK%010d.json",
	}, srv
}

// The whole point of using the submissions API over the RSS feed: the headline
// says what happened rather than repeating the form type.
func TestFilingHeadlineStatesWhatHappened(t *testing.T) {
	f := Filing{
		Ticker: "AAPL", Company: "Apple Inc.",
		Items: []string{"2.02"},
		Filed: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
		URL:   "https://www.sec.gov/Archives/edgar/data/320193/x/a8k.htm",
	}
	got := f.article()

	if !strings.Contains(got.Title, "Apple Inc.") || !strings.Contains(got.Title, "reported results") {
		t.Errorf("Title = %q, want the company and what it did", got.Title)
	}
	if strings.Contains(got.Title, "Current report") {
		t.Errorf("Title fell back to the form type: %q", got.Title)
	}
	if got.SourceID != SourceID {
		t.Errorf("SourceID = %q", got.SourceID)
	}
	if len(got.Tickers) != 1 || got.Tickers[0] != "AAPL" {
		t.Errorf("Tickers = %v, want the filer", got.Tickers)
	}
}

// Regulation FD and Other Events are most of 8-K volume and say nothing. They
// are what produced "Analog Devices also filed a Reg FD 8-K the same day".
func TestRoutineItemsAreIgnored(t *testing.T) {
	for _, raw := range []string{"7.01", "8.01", "9.01", "7.01,9.01", ""} {
		if got := materialCodes(raw); len(got) != 0 {
			t.Errorf("materialCodes(%q) = %v, want none", raw, got)
		}
	}
	if got := materialCodes("2.02,9.01"); len(got) != 1 || got[0] != "2.02" {
		t.Errorf("materialCodes kept the wrong codes: %v", got)
	}
}

func TestCollectReturnsOnlyMaterialRecentFilings(t *testing.T) {
	c, _ := newStub(t)
	c.tickers = map[string]company{"AAPL": {CIK: 320193, Name: "Apple Inc."}}
	c.once.Do(func() {}) // the index is pre-seeded; do not fetch it

	since := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	articles, errs := c.Collect(context.Background(), []string{"AAPL"}, since)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// Of three filings: one material and recent, one Reg FD, one from January.
	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1: %+v", len(articles), articles)
	}
	if !strings.Contains(articles[0].Title, "reported results") {
		t.Errorf("Title = %q", articles[0].Title)
	}
}

// A watchlist naming something SEC does not list -- a foreign issuer, or a
// typo -- must be skipped quietly rather than reported as a failure.
func TestUnknownTickersAreSkippedNotFailed(t *testing.T) {
	c, _ := newStub(t)
	c.tickers = map[string]company{"AAPL": {CIK: 320193, Name: "Apple Inc."}}
	c.once.Do(func() {})

	articles, errs := c.Collect(context.Background(), []string{"NOTREAL"}, time.Time{})
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none for an unlisted symbol", errs)
	}
	if len(articles) != 0 {
		t.Errorf("articles = %v, want none", articles)
	}
}

func TestFilingURLPointsAtTheDocument(t *testing.T) {
	got := filingURL(320193, "0000320193-26-000101", "a8k.htm")
	want := "https://www.sec.gov/Archives/edgar/data/320193/000032019326000101/a8k.htm"
	if got != want {
		t.Errorf("filingURL = %q, want %q", got, want)
	}
	// Without a primary document the index page is still reachable.
	if got := filingURL(320193, "0000320193-26-000101", ""); !strings.HasSuffix(got, "/") {
		t.Errorf("filingURL without a document = %q", got)
	}
}

// The submissions arrays are parallel by contract; a ragged one would pair a
// form with another filing's date.
func TestRaggedSubmissionsAreRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"X","filings":{"recent":{
		 "form":["8-K","8-K"],"filingDate":["2026-09-09"],
		 "accessionNumber":["a"],"items":["2.02"],"primaryDocument":["x.htm"]}}}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), UserAgent: "test"}
	if _, err := c.recent(context.Background(), company{CIK: 1, Name: "X"}, time.Time{}); err == nil {
		t.Error("ragged arrays were accepted")
	}
}

// SEC returns the CIK as a quoted string. Typing it as a number rejected every
// response, and all 93 lookups failed against the live API before this was
// caught -- the kind of shape mismatch a stub with a hand-written body hides.
func TestSubmissionsDecodeAcceptsAQuotedCIK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"cik":"0001167419","name":"Riot Platforms","filings":{"recent":{
		 "accessionNumber":["a"],"filingDate":["2026-09-09"],"form":["8-K"],
		 "items":["2.02"],"primaryDocument":["x.htm"]}}}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), UserAgent: "test", SubmissionsURL: srv.URL + "/%010d"}
	got, err := c.recent(context.Background(), company{CIK: 1167419, Name: "Riot"}, time.Time{})
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d filings, want 1", len(got))
	}
	if got[0].Company != "Riot Platforms" {
		t.Errorf("Company = %q", got[0].Company)
	}
}
