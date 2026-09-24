package sec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The latest 8-K under Item 2.02 is the results filing, and its press release
// is the document the index page lists as EX-99.1, whatever the file is
// called; an older results filing and a later 8-K of another kind are passed
// over.
func TestTheLatestResultsReleaseIsFoundByItsExhibitType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tickers":
			w.Write([]byte(`{"0":{"cik_str":1045810,"ticker":"NVDA","title":"NVIDIA CORP"}}`))
		case "/submissions/CIK0001045810.json":
			w.Write([]byte(`{"name":"NVIDIA CORP","filings":{"recent":{
				"accessionNumber":["0001045810-26-000080","0001045810-26-000073","0001045810-26-000020"],
				"filingDate":["2026-09-01","2026-08-26","2026-05-20"],
				"form":["8-K","8-K","8-K"],
				"items":["5.02","2.02,9.01","2.02,9.01"],
				"primaryDocument":["a.htm","nvda-20260826.htm","b.htm"]}}}`))
		case "/archive/1045810/000104581026000073/0001045810-26-000073-index.html":
			w.Write([]byte(`<table>
<tr><td>1</td><td>8-K</td><td><a href="/ix?doc=/Archives/nvda-20260826.htm">nvda-20260826.htm</a></td><td>8-K</td></tr>
<tr><td>3</td><td>EX-99.2</td><td><a href="/Archives/edgar/data/1045810/000104581026000073/q2fy27cfocommentary.htm">q2fy27cfocommentary.htm</a></td><td>EX-99.2</td></tr>
<tr><td>2</td><td>EX-99.1</td><td><a href="/Archives/edgar/data/1045810/000104581026000073/q2fy27pr.htm">q2fy27pr.htm</a></td><td>EX-99.1</td></tr>
</table>`))
		case "/archive/1045810/000104581026000073/q2fy27pr.htm":
			w.Write([]byte(`<html><body><p>NVIDIA Announces Financial Results for Second Quarter Fiscal 2027</p><p>Revenue of $78.4 billion, up 12% from Q1.</p><p>` +
				strings.Repeat("Outlook for the third quarter. ", 100) + `</p></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), UserAgent: "test",
		TickerIndexURL: srv.URL + "/tickers",
		SubmissionsURL: srv.URL + "/submissions/CIK%010d.json",
		ArchiveURL:     srv.URL + "/archive"}

	filing, text, err := c.EarningsRelease(context.Background(), "nvda", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 300)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filing.URL, "/q2fy27pr.htm") || filing.Filed.Day() != 26 {
		t.Errorf("filing = %+v", filing)
	}
	if !strings.HasPrefix(text, "NVIDIA Announces Financial Results") || !strings.Contains(text, "$78.4 billion") {
		t.Errorf("text = %q", text)
	}
	if !strings.HasSuffix(text, "the rest is not shown.]") || len([]rune(text)) > 360 {
		t.Errorf("a long release was not cut: %d runes", len([]rune(text)))
	}

	// With nothing since the cut-off, the older results are not offered as
	// the latest.
	if _, _, err := c.EarningsRelease(context.Background(), "NVDA", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 300); err == nil {
		t.Error("a results filing older than the cut-off was used")
	}
}

// A foreign filer reports on 6-K, with no item codes. The cover page names each
// exhibit, and the results are told apart by title from the share returns and
// a meeting's voting results filed after them -- here as exhibit 99.2, so the
// exhibit named on the cover is the one read.
func TestResultsAreFoundInAForeignFilersSixK(t *testing.T) {
	cover := func(exhibits ...string) string {
		return `<html><body><p>FORM 6-K</p><p>Report of Foreign Private Issuer</p><p>EXHIBITS</p>` +
			strings.Join(exhibits, "") + `<p>SIGNATURES</p></body></html>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tickers":
			w.Write([]byte(`{"0":{"cik_str":1577552,"ticker":"BABA","title":"Alibaba Group Holding Ltd"}}`))
		case "/submissions/CIK0001577552.json":
			w.Write([]byte(`{"name":"Alibaba Group Holding Ltd","filings":{"recent":{
				"accessionNumber":["0001104659-26-109625","0001104659-26-105208","0001104659-26-099220"],
				"filingDate":["2026-09-22","2026-09-04","2026-08-20"],
				"form":["6-K","6-K","6-K"],
				"items":["","",""],
				"primaryDocument":["agm_6k.htm","return_6k.htm","results_6k.htm"]}}}`))
		case "/archive/1577552/000110465926109625/agm_6k.htm":
			w.Write([]byte(cover(`<p>Exhibit 99.1 &ndash; Voting Results of Annual General Meeting</p>`)))
		case "/archive/1577552/000110465926105208/return_6k.htm":
			w.Write([]byte(cover(`<p>Exhibit 99.1 &ndash; Monthly Return with The Stock Exchange of Hong Kong Limited</p>`)))
		case "/archive/1577552/000110465926099220/results_6k.htm":
			w.Write([]byte(cover(
				`<p>Exhibit 99.1 &ndash; Announcement &ndash; Date of Board Meeting</p>`,
				`<p>Exhibit 99.2 &ndash; Press Release &ndash; Alibaba Group Announces June&nbsp;Quarter 2026 Results</p>`)))
		case "/archive/1577552/000110465926099220/0001104659-26-099220-index.html":
			w.Write([]byte(`<table>
<tr><td>1</td><td>FORM 6-K</td><td><a href="/Archives/edgar/data/1577552/000110465926099220/results_6k.htm">results_6k.htm</a></td><td>6-K</td></tr>
<tr><td>2</td><td>EXHIBIT 99.1</td><td><a href="/Archives/edgar/data/1577552/000110465926099220/ex99-1.htm">ex99-1.htm</a></td><td>EX-99.1</td></tr>
<tr><td>3</td><td>EXHIBIT 99.2</td><td><a href="/Archives/edgar/data/1577552/000110465926099220/ex99-2.htm">ex99-2.htm</a></td><td>EX-99.2</td></tr>
</table>`))
		case "/archive/1577552/000110465926099220/ex99-2.htm":
			w.Write([]byte(`<p>Alibaba Group Announces June Quarter 2026 Results</p><p>Revenue was RMB243,000 million.</p>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), UserAgent: "test",
		TickerIndexURL: srv.URL + "/tickers",
		SubmissionsURL: srv.URL + "/submissions/CIK%010d.json",
		ArchiveURL:     srv.URL + "/archive"}

	filing, text, err := c.EarningsRelease(context.Background(), "BABA", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filing.URL, "/ex99-2.htm") || filing.Filed.Day() != 20 {
		t.Errorf("filing = %+v, want exhibit 99.2 of the 20 August 6-K", filing)
	}
	if !strings.Contains(text, "RMB243,000 million") {
		t.Errorf("text = %q", text)
	}
}
