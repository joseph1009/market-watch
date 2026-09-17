package prices

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// series builds a history that rises by one a day, so every average has an
// answer that can be worked out on paper.
func series(days int, volume float64) Series {
	start := time.Date(2025, time.August, 13, 0, 0, 0, 0, time.UTC)
	s := Series{Symbol: "TEST", Currency: "USD"}
	for i := 0; i < days; i++ {
		close := 100 + float64(i)
		s.Bars = append(s.Bars, Bar{
			Date:   start.AddDate(0, 0, i),
			Open:   close,
			High:   close,
			Low:    close,
			Close:  close,
			Volume: volume,
		})
	}
	return s
}

func TestSummariseAverages(t *testing.T) {
	s := series(400, 1_000_000)
	last := s.Bars[len(s.Bars)-1].Date
	// Read the morning after, so the last session is a finished one.
	got := Summarise(s, last.AddDate(0, 0, 1))

	if got.Days != 400 {
		t.Errorf("days = %d, want 400", got.Days)
	}
	if got.Last != 499 {
		t.Errorf("last close = %v, want 499", got.Last)
	}
	if got.MA50 != 474.5 {
		t.Errorf("50-day average = %v, want 474.5", got.MA50)
	}
	if got.MA200 != 399.5 {
		t.Errorf("200-day average = %v, want 399.5", got.MA200)
	}
	// Volume is flat, so the volume-weighted average is the plain average of
	// the last thirty closes.
	if got.VWAP30 != 484.5 {
		t.Errorf("30-day average price paid = %v, want 484.5", got.VWAP30)
	}
	if busy := got.Busy(); math.Abs(busy-1) > 1e-9 {
		t.Errorf("latest session = %vx the average, want 1", busy)
	}
	if got.High52 != 499 {
		t.Errorf("52-week high = %v, want 499", got.High52)
	}
	if got.Low52 != 134 {
		t.Errorf("52-week low = %v, want 134 (the close a year before the last)", got.Low52)
	}
}

// An average is a claim about how many days it covers. Fifty sessions do not
// make a two-hundred day average, and the figure has to be absent rather than
// quietly computed from whatever is there.
func TestSummariseWithdrawsAveragesItCannotSupport(t *testing.T) {
	got := Summarise(series(60, 1_000_000), time.Date(2025, time.October, 11, 0, 0, 0, 0, time.UTC))

	if got.MA50 == 0 {
		t.Error("fifty sessions of history should have a 50-day average")
	}
	if got.MA200 != 0 {
		t.Errorf("200-day average = %v from sixty sessions, want none", got.MA200)
	}
	for _, r := range got.Returns {
		if r.Over == "12 months" {
			t.Error("a twelve-month return was reported from sixty sessions")
		}
	}
}

func TestSummariseReturns(t *testing.T) {
	s := series(400, 1_000_000)
	got := Summarise(s, s.Bars[len(s.Bars)-1].Date)

	want := map[string]float64{
		"1 week":    (499 - 492) / 492.0 * 100,
		"1 month":   (499 - 469) / 469.0 * 100,
		"12 months": (499 - 134) / 134.0 * 100,
	}
	found := map[string]bool{}
	for _, r := range got.Returns {
		found[r.Over] = true
		if w, ok := want[r.Over]; ok && math.Abs(r.Percent-w) > 1e-9 {
			t.Errorf("%s = %v, want %v", r.Over, r.Percent, w)
		}
		// A percentage without its starting price does not say what the chart
		// looks like, so the close it was measured from travels with it.
		if r.Over == "1 week" {
			if since := s.Bars[len(s.Bars)-1].Date.AddDate(0, 0, -7); r.From != 492 || !r.Since.Equal(since) {
				t.Errorf("1 week measured from %v on %s, want 492 on %s", r.From, r.Since.Format(time.DateOnly), since.Format(time.DateOnly))
			}
		}
	}
	for _, over := range []string{"1 week", "1 month", "3 months", "6 months", "12 months", "year to date"} {
		if !found[over] {
			t.Errorf("no return over %s", over)
		}
	}
}

// A flat price has no volatility, and a fabricated one would look like risk.
func TestSummariseVolatility(t *testing.T) {
	s := Series{Symbol: "FLAT", Currency: "USD"}
	start := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		s.Bars = append(s.Bars, Bar{
			Date: start.AddDate(0, 0, i), Close: 50, High: 50, Low: 50, Volume: 1000,
		})
	}
	if got := Summarise(s, start.AddDate(0, 0, 119)); got.Volatility != 0 {
		t.Errorf("volatility of a flat price = %v, want 0", got.Volatility)
	}
}

const chartReply = `{"chart":{"result":[{"meta":{"currency":"usd","symbol":"TEST","longName":"Test Corp"},
"timestamp":[1757548800,1757635200,1757721600,1757808000],
"indicators":{"quote":[{"open":[10,11,null,13],"high":[11,12,null,14],"low":[9,10,null,12],
"close":[10.5,11.5,null,13.5],"volume":[1000,1100,null,1300]}]}}],"error":null}}`

func TestFetchDropsSessionsWithNoPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(chartReply))
	}))
	defer server.Close()

	h := &History{HTTP: server.Client(), URL: server.URL + "/"}
	// Four sessions is below the floor for summarising, which is the point of
	// the floor -- but Fetch should still say what it read rather than pretend.
	_, err := h.Fetch(context.Background(), "test")
	if err == nil {
		t.Fatal("four sessions should be refused as too little history")
	}
	if want := "only 3 sessions"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error was %q, want it to mention %q: the null session must not be counted", err, want)
	}
}

func TestFetchReportsAnUnknownSymbol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"chart":{"result":null,"error":{"description":"No data found, symbol may be delisted"}}}`))
	}))
	defer server.Close()

	h := &History{HTTP: server.Client(), URL: server.URL + "/"}
	if _, err := h.Fetch(context.Background(), "NOPE"); err == nil {
		t.Fatal("an unknown symbol should be an error, not an empty history")
	}
}

// A session read while it is still running has a part of a day's volume in it.
// Set against whole-day averages that reads as a quiet market, and it is the
// one figure here a reader would most naturally misread.
func TestSummariseFlagsAnUnfinishedSession(t *testing.T) {
	s := series(400, 1_000_000)
	last := s.Bars[len(s.Bars)-1]
	s.Bars[len(s.Bars)-1].Volume = 120_000 // a couple of hours of trading

	got := Summarise(s, last.Date.Add(11*time.Hour))
	if !got.Partial {
		t.Fatal("a session dated today was not flagged as unfinished")
	}
	if busy := got.Busy(); busy != 0 {
		t.Errorf("busy = %v for an unfinished session, want no comparison at all", busy)
	}

	finished := Summarise(s, last.Date.AddDate(0, 0, 1))
	if finished.Partial {
		t.Error("yesterday's session was flagged as unfinished")
	}
	if finished.Busy() == 0 {
		t.Error("a finished session should be comparable with the averages")
	}
}
