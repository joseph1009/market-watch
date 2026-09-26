package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// stub answers each endpoint with the Micron reply saved in testdata, as
// Nasdaq sent it on 24 September 2026, trimmed of the rows nothing reads.
func stub(t *testing.T, broken string) *Client {
	t.Helper()
	files := map[string]string{
		"/analyst/MU/earnings-forecast":      "forecast",
		"/analyst/MU/targetprice":            "target",
		"/company/MU/earnings-surprise":      "surprise",
		"/analyst/MU/earnings-date":          "earnings-date",
		"/company/MU/insider-trades":         "insider",
		"/quote/MU/short-interest":           "short",
		"/company/MU/institutional-holdings": "holdings",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != browser {
			http.Error(w, "no browser", http.StatusForbidden)
			return
		}
		name, ok := files[r.URL.Path]
		if !ok {
			w.Write([]byte(`{"data":null,"status":{"rCode":400}}`))
			return
		}
		if name == broken {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		body, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL}
}

func TestEveryPartIsRead(t *testing.T) {
	r, err := stub(t, "").Fetch(context.Background(), "mu")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Years) < 2 || r.Years[0].Period != "Aug 2026" || r.Years[0].EPS != 72.99 || r.Years[0].Up != 2 {
		t.Errorf("years = %+v", r.Years)
	}
	if len(r.Quarters) == 0 || r.Quarters[0].Analysts != 10 {
		t.Errorf("quarters = %+v", r.Quarters)
	}
	if r.Target != 1563.04 || r.Buy != 29 || r.Hold != 1 {
		t.Errorf("target %v, ratings %d/%d/%d", r.Target, r.Buy, r.Hold, r.Sell)
	}
	// The expected figure arrives as a string and the reported one as a
	// number, in the same row.
	if len(r.Surprises) != 4 || r.Surprises[0].EPS != 24.89 || r.Surprises[0].Expected != 20.98 || r.Surprises[0].Reported.Day() != 24 {
		t.Errorf("surprises = %+v", r.Surprises)
	}
	// "(178,789)" is a net sale.
	if r.InsiderBuys3 != 1 || r.InsiderSells12 != 73 || r.InsiderNet3 != -178789 {
		t.Errorf("insiders: buys %d, sells %d, net %v", r.InsiderBuys3, r.InsiderSells12, r.InsiderNet3)
	}
	if r.ShortShares != 29705339 || r.ShortPrior != 30016025 || r.ShortAsOf.Month() != 8 {
		t.Errorf("short interest %v (prior %v) on %v", r.ShortShares, r.ShortPrior, r.ShortAsOf)
	}
	if r.Institutional != 87.97 || r.FundsAdded != 2363 || r.FundsSoldOut != 121 {
		t.Errorf("funds: %v%%, added %d, sold out %d", r.Institutional, r.FundsAdded, r.FundsSoldOut)
	}
	if r.NextResults.Format("2006-01-02") != "2026-09-30" || r.ResultsWhen != "after market close" || r.ResultsEstimated {
		t.Errorf("next results %v, %q, estimated %v", r.NextResults, r.ResultsWhen, r.ResultsEstimated)
	}
	if len(r.Missing) != 0 {
		t.Errorf("missing %v", r.Missing)
	}
}

// The fact sheet turns the forecasts into multiples of today's price and
// labels them as estimates.
func TestTheFactsSetTheForecastsAgainstThePrice(t *testing.T) {
	r, _ := stub(t, "").Fetch(context.Background(), "MU")
	facts := r.Facts(1045.82)
	for _, want := range []string{
		"not filed figures",
		"period to Aug 2027 158.67",
		"6.6 times",
		"49% above today's US$1,045.82",
		"+18.6%",
		"net 178,789 shares sold",
		"29.7m shares on 31 Aug 2026",
		"2,363 added",
		"Next results: Wednesday 30 Sep 2026, after market close, as the company has announced it.",
	} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %q:\n%s", want, facts)
		}
	}
	if line := r.Line(1045.82); line != "fwd P/E 14.3x FYAug26, 6.6x FYAug27; estimates 4 wks +2/-0; target +49%" {
		t.Errorf("line = %q", line)
	}
}

// A part that fails is named and the rest stand; a symbol with nothing at all
// is not covered.
func TestAFailedPartIsNamedAndAnUnknownSymbolIsNotCovered(t *testing.T) {
	c := stub(t, "short")
	r, err := c.Fetch(context.Background(), "MU")
	if err != nil || r.ShortShares != 0 || len(r.Missing) != 1 || r.Missing[0] != "short interest" {
		t.Errorf("err %v, short %v, missing %v", err, r.ShortShares, r.Missing)
	}
	if !strings.Contains(r.Facts(0), "Not available: short interest") {
		t.Error("the facts do not say what is missing")
	}

	if _, err := c.Fetch(context.Background(), "SPY"); !errors.Is(err, ErrNotCovered) {
		t.Errorf("a symbol with no data: err = %v", err)
	}
	got := c.FetchAll(context.Background(), []string{"MU", "SPY"}, 2)
	if mu, ok := got["MU"]; !ok || len(got) != 1 || mu.Target != 1563.04 || len(mu.Surprises) != 0 {
		t.Errorf("FetchAll = %+v, want MU alone, with its forecasts and target and nothing more", got)
	}
}

// A date nobody has announced is Zacks' guess, and the facts say so.
func TestAnEstimatedResultsDateIsCalledOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"reportText":"Apple Inc. Common Stock is estimated to report earnings on  10/29/2026. The upcoming earnings date is derived from an algorithm based on a company's historical reporting dates."}}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}

	var r Report
	if err := c.resultsDate(context.Background(), "AAPL", &r); err != nil {
		t.Fatal(err)
	}
	if r.NextResults.Format("2006-01-02") != "2026-10-29" || r.ResultsWhen != "" || !r.ResultsEstimated {
		t.Errorf("next results %v, %q, estimated %v", r.NextResults, r.ResultsWhen, r.ResultsEstimated)
	}
	r.Target = 1 // covered
	if facts := r.Facts(0); !strings.Contains(facts, "Thursday 29 Oct 2026. Not yet announced") {
		t.Errorf("facts:\n%s", facts)
	}
}

func TestFiguresAreReadHoweverTheSiteWritesThem(t *testing.T) {
	for in, want := range map[string]float64{
		`"$1,045.82"`: 1045.82, `"87.97%"`: 87.97, `"(1,307,590)"`: -1307590, `1.158306`: 1.158306, `"24"`: 24,
	} {
		var n num
		if err := json.Unmarshal([]byte(in), &n); err != nil || !n.ok || n.v != want {
			t.Errorf("%s -> %v (ok %v, err %v), want %v", in, n.v, n.ok, err, want)
		}
	}
	for _, in := range []string{`"N/A"`, `null`, `"--"`} {
		var n num
		if err := json.Unmarshal([]byte(in), &n); err != nil || n.ok {
			t.Errorf("%s read as known (%v)", in, n.v)
		}
	}
}
