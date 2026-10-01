package consensus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A day's results: when each reports, what is expected, and a company's
// second share class counted once.
func TestEarningsReadsADaysResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/calendar/earnings" || r.URL.Query().Get("date") != "2026-10-01" {
			w.Write([]byte(`{"data":null}`))
			return
		}
		w.Write([]byte(`{"data":{"rows":[
			{"time":"time-pre-market","symbol":"ACN","name":"Accenture plc","marketCap":"$118,229,410,000","epsForecast":"$3.19","noOfEsts":"7","lastYearEPS":"$3.03"},
			{"time":"time-after-hours","symbol":"NKE","name":"Nike, Inc.","marketCap":"$53,168,595,000","epsForecast":"$0.43","noOfEsts":"12","lastYearEPS":"$0.49"},
			{"time":"time-pre-market","symbol":"MKC","name":"McCormick & Company, Incorporated","marketCap":"$13,012,415,500","epsForecast":"$0.75","noOfEsts":"6","lastYearEPS":"$0.85"},
			{"time":"time-pre-market","symbol":"MKC.V","name":"McCormick & Company, Incorporated","marketCap":"$13,012,415,500","epsForecast":"","noOfEsts":"7","lastYearEPS":"$0.85"},
			{"time":"time-not-supplied","symbol":"XYZ","name":"Unknown Co","marketCap":"N/A","epsForecast":"","noOfEsts":"N/A","lastYearEPS":""}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}

	got, err := c.Earnings(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d results, want 4 (McCormick once): %+v", len(got), got)
	}
	acn := got[0]
	if acn.Symbol != "ACN" || acn.When != "before the open" || acn.Forecast != "$3.19" || acn.LastYear != "$3.03" || acn.Estimates != 7 || acn.MarketCap < 1e11 {
		t.Errorf("ACN = %+v", acn)
	}
	if got[1].When != "after the close" || got[2].Forecast != "$0.75" || got[3].When != "" {
		t.Errorf("rows = %+v", got)
	}

	none, err := c.Earnings(context.Background(), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || len(none) != 0 {
		t.Errorf("a day with nothing due = %v, %v", none, err)
	}
}
