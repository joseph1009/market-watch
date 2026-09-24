package sec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A foreign filer's announcements are its 6-Ks, described by the titles on
// their covers. The share returns and meeting notices that make up most of
// them are left out, and so is anything before the cut-off.
func TestRecentDescribesAForeignFilersSixKs(t *testing.T) {
	covers := map[string]string{
		"placing_6k.htm": "Exhibit 99.1 &ndash; Press Release &ndash; Alibaba Group Announced Completion of HK$80 Billion Placing of New Shares in Hong Kong",
		"return_6k.htm":  "Exhibit 99.1 &ndash; Next Day Disclosure Return with The Stock Exchange of Hong Kong Limited",
		"results_6k.htm": "Exhibit 99.1 &ndash; Press Release &ndash; Alibaba Group Announces June Quarter 2026 Results",
		"agm_6k.htm":     "Exhibit 99.1 &ndash; Notice of Annual General Meeting",
		"old_6k.htm":     "Exhibit 99.1 &ndash; Press Release &ndash; Alibaba Group Announces March Quarter 2026 Results",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tickers":
			w.Write([]byte(`{"0":{"cik_str":1577552,"ticker":"BABA","title":"Alibaba Group Holding Ltd"}}`))
		case r.URL.Path == "/submissions/CIK0001577552.json":
			w.Write([]byte(`{"name":"Alibaba Group Holding Ltd","filings":{"recent":{
				"accessionNumber":["0001-26-5","0001-26-4","0001-26-3","0001-26-2","0001-26-1"],
				"filingDate":["2026-08-26","2026-08-26","2026-08-20","2026-08-06","2026-05-13"],
				"form":["6-K","6-K","6-K","6-K","6-K"],
				"items":["","","","",""],
				"primaryDocument":["placing_6k.htm","return_6k.htm","results_6k.htm","agm_6k.htm","old_6k.htm"]}}}`))
		default:
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if cover, ok := covers[name]; ok {
				w.Write([]byte(`<p>FORM 6-K</p><p>EXHIBITS</p><p>` + cover + `</p><p>SIGNATURES</p>`))
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), UserAgent: "test",
		TickerIndexURL: srv.URL + "/tickers",
		SubmissionsURL: srv.URL + "/submissions/CIK%010d.json",
		ArchiveURL:     srv.URL + "/archive"}

	filings, err := c.Recent(context.Background(), "baba", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range filings {
		got = append(got, f.Filed.Format(time.DateOnly)+" "+f.Describe())
	}
	want := []string{
		"2026-08-26 Press Release - Alibaba Group Announced Completion of HK$80 Billion Placing of New Shares in Hong Kong",
		"2026-08-20 Press Release - Alibaba Group Announces June Quarter 2026 Results",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if filings[0].Ticker != "BABA" {
		t.Errorf("ticker = %q", filings[0].Ticker)
	}
}

// Each filer lays its exhibit list out its own way. These are the covers of
// real results filings, and of the notices filed around them.
func TestResultsExhibitReadsEachCoverLayout(t *testing.T) {
	cases := []struct {
		name, cover string
		want        string // the exhibit number, or "" for none
	}{
		{"Alibaba, one line", "EXHIBITS\nExhibit 99.1 - Press Release - Alibaba Group Announces June Quarter 2026 Results\nSIGNATURES", "99.1"},
		{"JD, title in the next cell", "Exhibit Index\n99.1\nPress Release - JD.com Announces Second Quarter and Interim 2026 Results\nSIGNATURES", "99.1"},
		{"PDD, earnings release", "Exhibit Index\nExhibit 99.1--Press Release (Earnings Release)\nSIGNATURES", "99.1"},
		{"Alibaba, results announced in Hong Kong", "Exhibit 99.1 -Announcement with The Stock Exchange of Hong Kong Limited - Announcement of the March Quarter 2026 Results and Fiscal Year 2026 Annual Results", "99.1"},
		{"a meeting's voting results", "Exhibit 99.1 - Voting Results of Annual General Meeting", ""},
		{"a meeting about results", "Exhibit 99.1 - Announcement - Date of Board Meeting to Approve Quarterly Results", ""},
		{"JD, two notices", "Exhibit Index\n99.1\nPress Release - JD.com to Hold Annual General Meeting on June 29, 2026\n99.2\nNotice of Annual General Meeting of Shareholders", ""},
		{"Sea, no title", "EXHIBIT INDEX\nExhibit 99.1 -- Press Release", ""},
	}
	for _, tc := range cases {
		got, ok := resultsExhibit(exhibits(tc.cover))
		if tc.want == "" && ok {
			t.Errorf("%s: took %+v as results", tc.name, got)
		}
		if tc.want != "" && got.number != tc.want {
			t.Errorf("%s: got %+v, want exhibit %s", tc.name, got, tc.want)
		}
	}
}

// Above the list, the body can break "furnished as Exhibit 99.1 of this
// report" across two lines, and the second half is not an exhibit.
func TestExhibitsAreReadFromTheListOnly(t *testing.T) {
	got := exhibits("The return is furnished as Exhibit\n99.1 of this Current Report on Form 6-K.\nEXHIBITS\nExhibit 99.1 - Monthly Return")
	if len(got) != 1 || got[0].title != "Monthly Return" {
		t.Errorf("got %+v, want only the listed exhibit", got)
	}
}
