package prices

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// A listing outside the US has no keyed quote on any free tier, so its price
// has to come from the same history the averages do -- carrying the currency it
// was struck in, because a Hong Kong level read as dollars is off by a factor
// of about eight.
func TestLatestPricesAListingFromItsHistory(t *testing.T) {
	s := series(40, 1_000_000)
	s.Symbol, s.Currency = "0700.HK", "HKD"

	got, ok := Latest(s)
	if !ok {
		t.Fatal("no price from forty sessions of history")
	}
	if got.Symbol != "0700.HK" {
		t.Errorf("symbol = %q, want 0700.HK", got.Symbol)
	}
	if got.Currency != "HKD" || got.Unit() != "HKD" {
		t.Errorf("currency = %q, unit = %q, want HKD for both", got.Currency, got.Unit())
	}
	// The series rises by one a day, from 100.
	if got.Price != 139 || got.Previous != 138 {
		t.Errorf("price = %v, previous = %v, want 139 from 138", got.Price, got.Previous)
	}
	if got.Change != 1 {
		t.Errorf("change = %v, want 1", got.Change)
	}
	if math.Abs(got.Percent-1.0/138*100) > 1e-9 {
		t.Errorf("percent = %v, want the move from 138 to 139", got.Percent)
	}
	if got.AsOf != s.Bars[len(s.Bars)-1].Date {
		t.Errorf("as at %v, want the last session %v", got.AsOf, s.Bars[len(s.Bars)-1].Date)
	}
}

// A US quote says nothing about its currency because it never has to. Anything
// printing a price still needs an answer, so the empty case is dollars.
func TestAPriceWithoutACurrencyIsInDollars(t *testing.T) {
	got, ok := Latest(series(40, 1_000_000))
	if !ok {
		t.Fatal("no price from forty sessions of history")
	}
	if got.Unit() != "USD" {
		t.Errorf("unit = %q, want USD", got.Unit())
	}
}

// One session gives a level and no move, and a move is the half of a quote the
// brief is actually about. Better no price than a change of zero that reads as
// a flat day.
func TestLatestNeedsASessionToCompareAgainst(t *testing.T) {
	s := series(1, 1_000_000)
	if _, ok := Latest(s); ok {
		t.Error("a single session was priced as though it had a move")
	}
	if _, ok := Latest(Series{}); ok {
		t.Error("an empty history was priced")
	}
}

// A bar is dated by its day, and midnight on the day of a New York session is
// earlier than the brief the evening before. A price is dated when it was
// struck wherever the chart says, or every one of them would read as stale.
func TestLatestIsDatedWhenThePriceWasStruck(t *testing.T) {
	var stamps, closes []string
	day := time.Date(2026, 8, 1, 13, 30, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		stamps = append(stamps, strconv.FormatInt(day.AddDate(0, 0, i).Unix(), 10))
		closes = append(closes, strconv.Itoa(100+i))
	}
	struck := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC)
	body := fmt.Sprintf(`{"chart":{"result":[{"meta":{"currency":"USD","symbol":"NVDA","regularMarketTime":%d},"timestamp":[%s],"indicators":{"quote":[{"close":[%s]}]}}],"error":null}}`,
		struck.Unix(), strings.Join(stamps, ","), strings.Join(closes, ","))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()

	h := &History{HTTP: server.Client(), URL: server.URL + "/"}
	s, err := h.Fetch(context.Background(), "NVDA")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := Latest(s)
	if !ok {
		t.Fatal("no price from thirty sessions")
	}
	if !got.AsOf.Equal(struck) {
		t.Errorf("as at %v, want when it was struck, %v", got.AsOf, struck)
	}
}

// A bar keeps the moment its session opened, which is what says whether a
// verdict given at 07:50 in New York came before that day's open or after it.
func TestFetchKeepsWhenEachSessionOpened(t *testing.T) {
	var stamps, prices []string
	open := time.Date(2026, time.August, 3, 13, 30, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		stamps = append(stamps, fmt.Sprint(open.AddDate(0, 0, i).Unix()))
		prices = append(prices, "10")
	}
	list := strings.Join(prices, ",")
	reply := `{"chart":{"result":[{"meta":{"currency":"USD"},"timestamp":[` + strings.Join(stamps, ",") +
		`],"indicators":{"quote":[{"open":[` + list + `],"high":[` + list + `],"low":[` + list + `],"close":[` + list + `],"volume":[` + list + `]}]}}],"error":null}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(reply))
	}))
	defer server.Close()

	s, err := (&History{HTTP: server.Client(), URL: server.URL + "/"}).Fetch(context.Background(), "SPY")
	if err != nil {
		t.Fatal(err)
	}
	if first := s.Bars[0]; !first.Opened.Equal(open) || !first.Date.Equal(open.Truncate(24*time.Hour)) {
		t.Errorf("first bar opened %v, dated %v", first.Opened, first.Date)
	}
}

// The path is the last year's closes with the averages as they stood each
// day, and no average before there was history enough for it.
func TestSummariseKeepsTheYearsPath(t *testing.T) {
	s := series(400, 1_000_000)
	got := Summarise(s, s.Bars[len(s.Bars)-1].Date.AddDate(0, 0, 1))

	if len(got.Path) != 366 {
		t.Fatalf("path has %d points, want the 366 days of the last year", len(got.Path))
	}
	first, last := got.Path[0], got.Path[len(got.Path)-1]
	if first.Close != 134 || first.MA50 != 0 || first.MA200 != 0 {
		t.Errorf("first point = %+v, want 134 with no averages yet", first)
	}
	if last.Close != 499 || last.MA50 != got.MA50 || last.MA200 != got.MA200 {
		t.Errorf("last point = %+v, want the close and the averages Summarise gives", last)
	}
}
