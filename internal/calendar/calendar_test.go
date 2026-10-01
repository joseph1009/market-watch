package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const week = `[
{"title":"Core PCE Price Index m\/m","country":"USD","date":"2026-10-01T08:30:00-04:00","impact":"High","forecast":"0.3%","previous":"0.2%"},
{"title":"ISM Manufacturing PMI","country":"USD","date":"2026-10-01T10:00:00-04:00","impact":"Medium","forecast":"49.5","previous":"48.7"},
{"title":"FOMC Member Cook Speaks","country":"USD","date":"2026-10-01T13:25:00-04:00","impact":"Low","forecast":"","previous":""},
{"title":"German Prelim CPI m\/m","country":"EUR","date":"2026-09-30T08:00:00-04:00","impact":"High","forecast":"0.2%","previous":"0.1%"},
{"title":"Retail Sales m\/m","country":"AUD","date":"2026-10-01T21:30:00-04:00","impact":"High","forecast":"0.4%","previous":"0.6%"},
{"title":"BOJ Gov Ueda Speaks","country":"JPY","date":"2026-10-02T02:00:00-04:00","impact":"High","forecast":"","previous":""},
{"title":"Bank Holiday","country":"CNY","date":"2026-10-01T00:00:00-04:00","impact":"Holiday","forecast":"","previous":""},
{"title":"S&amp;P Global Services PMI","country":"USD","date":"2026-10-03T09:45:00-04:00","impact":"Medium","forecast":"","previous":"53.9"}
]`

// The week is read whole; Key keeps what a reader of the US market needs
// from now on: American releases rated high or medium, and the other large
// economies' high ones, soonest first.
func TestTheWeeksKeyReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(week)) }))
	t.Cleanup(srv.Close)

	all, err := (&ForexFactory{HTTP: srv.Client(), URL: srv.URL}).Week(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 8 {
		t.Fatalf("read %d releases, want 8", len(all))
	}
	now := time.Date(2026, 10, 1, 7, 30, 0, 0, time.FixedZone("EDT", -4*3600))
	key := Key(all, now, now.AddDate(0, 0, 3))
	var titles []string
	for _, r := range key {
		titles = append(titles, r.Country+" "+r.Title)
	}
	want := []string{"USD Core PCE Price Index m/m", "USD ISM Manufacturing PMI", "JPY BOJ Gov Ueda Speaks", "USD S&P Global Services PMI"}
	if len(titles) != len(want) {
		t.Fatalf("key = %v, want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("key[%d] = %q, want %q", i, titles[i], want[i])
		}
	}
	if key[0].Forecast != "0.3%" || key[0].Previous != "0.2%" || key[0].At.Hour() != 8 {
		t.Errorf("PCE = %+v", key[0])
	}
}

func TestARefusalIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	if _, err := (&ForexFactory{HTTP: srv.Client(), URL: srv.URL}).Week(context.Background()); err == nil {
		t.Error("a refused request was not reported")
	}
}
