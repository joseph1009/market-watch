package market

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/prices"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// fakeSource serves the sessions given, an empty day for any other, and
// counts what was asked.
type fakeSource struct {
	days   map[string]map[string]prices.SessionBar
	splits []prices.Split
	asked  []string
	drops  int // requests to fail before answering
}

func (f *fakeSource) Session(_ context.Context, date time.Time) (map[string]prices.SessionBar, error) {
	f.asked = append(f.asked, date.Format(dayLayout))
	if f.drops > 0 {
		f.drops--
		return nil, errors.New("connection reset")
	}
	return f.days[date.Format(dayLayout)], nil
}

func (f *fakeSource) Splits(_ context.Context, since, until time.Time) ([]prices.Split, error) {
	return f.splits, nil
}

// A sync asks for the newest weekdays first, skips the weekend and what the
// store holds, marks an old empty weekday as a holiday but a recent one only
// as not served yet, and keeps only common shares that traded enough.
func TestSyncFillsNewestFirstAndRemembersHolidays(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	src := &fakeSource{days: map[string]map[string]prices.SessionBar{
		"2026-09-25": {
			"AAA":   {Open: 10, Close: 11, Volume: 1e6},
			"WARRW": {Close: 5, Volume: 1e7},
			"PENNY": {Close: 0.5, Volume: 1e8},
			"THIN":  {Close: 50, Volume: 100},
		},
		"2026-09-24": {"AAA": {Open: 9, Close: 10, Volume: 1e6}},
	}}
	// Monday 28 September, 07:50 in New York: the 28th has not closed.
	now := time.Date(2026, 9, 28, 11, 50, 0, 0, time.UTC)

	if _, err := store.Sync(context.Background(), src, now, 4); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(src.asked, ","); got != "2026-09-25,2026-09-24,2026-09-23,2026-09-22" {
		t.Errorf("asked for %s", got)
	}
	days, _ := store.Days()
	if len(days) != 2 {
		t.Fatalf("held %v", days)
	}
	f, err := store.read(day(2026, 9, 25))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Bars) != 1 || f.Bars["AAA"] != [3]float64{10, 11, 1e6} {
		t.Errorf("kept %v", f.Bars)
	}
	// The 22nd and 23rd are within four days of the 28th? No: six and five
	// days back, so they were holidays, and are not asked for again.
	if !store.Known(day(2026, 9, 22)) || !store.Known(day(2026, 9, 23)) {
		t.Error("an old empty weekday was not marked")
	}

	src.asked = nil
	if _, err := store.Sync(context.Background(), src, now, 1); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(src.asked, ","); got != "2026-09-21" {
		t.Errorf("the second sync asked for %s, want the next gap back", got)
	}
}

// A dropped connection is asked again; three in a row end the sync, to be
// taken up next time from where it stopped.
func TestADroppedDayIsAskedForAgain(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	src := &fakeSource{drops: 2, days: map[string]map[string]prices.SessionBar{"2026-09-25": {"AAA": {Close: 10, Volume: 1e6}}}}
	now := time.Date(2026, 9, 28, 11, 50, 0, 0, time.UTC)
	if _, err := store.Sync(context.Background(), src, now, 1); err != nil || !store.Known(day(2026, 9, 25)) {
		t.Errorf("err %v after two drops; asked %v", err, src.asked)
	}
	src = &fakeSource{drops: 3}
	if _, err := (&Store{Dir: t.TempDir()}).Sync(context.Background(), src, now, 1); err == nil {
		t.Error("three drops in a row were not reported")
	}
}

// A day within four days that comes back empty may not be served yet, and is
// asked for again next time.
func TestARecentEmptyDayIsAskedForAgain(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	src := &fakeSource{}
	now := time.Date(2026, 9, 29, 11, 50, 0, 0, time.UTC)
	_, _ = store.Sync(context.Background(), src, now, 1)
	if store.Known(day(2026, 9, 28)) {
		t.Error("yesterday was marked a holiday before it could have been served")
	}
}

// A split the source had not applied when it served a day is applied when
// the day is read; one it had applied is not applied twice.
func TestSplitsAreAppliedOnceOnLoad(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	before, after := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	// Served before the split: unadjusted.
	_ = store.Save(day(2026, 9, 9), before, map[string]Bar{"NVDA": {Open: 1000, Close: 1000, Volume: 100}, "AAA": {Close: 5, Volume: 1}})
	// Served after it: the source had already adjusted it.
	_ = store.Save(day(2026, 9, 8), after, map[string]Bar{"NVDA": {Open: 99, Close: 99, Volume: 1000}})
	_ = store.Save(day(2026, 9, 10), before, map[string]Bar{"NVDA": {Open: 101, Close: 101, Volume: 1000}})
	_ = store.saveSplits(splitRecord{Checked: day(2026, 9, 25), Splits: []Split{{Symbol: "NVDA", Date: day(2026, 9, 10), From: 1, To: 10}}})

	p, err := store.Load(time.Time{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := p.Get("NVDA")
	if got := []float64{n.Close[0], n.Close[1], n.Close[2]}; got[0] != 99 || got[1] != 100 || got[2] != 101 {
		t.Errorf("closes = %v, want 99, 100 (1,000 split ten for one), 101", got)
	}
	if n.Volume[1] != 1000 {
		t.Errorf("volume = %v, want the shares multiplied as the price was divided", n.Volume[1])
	}
	if a := p.Get("AAA"); a == nil || a.Close[0] != 0 || a.Close[1] != 5 || a.Close[2] != 0 {
		t.Errorf("AAA = %+v, want a zero on the days it did not trade", a)
	}
}

// The first sync reads no splits: every session it fetched arrived adjusted.
// Later ones read the splits since the last.
func TestSplitsAreReadFromTheSecondSyncOn(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	src := &fakeSource{splits: []prices.Split{{Symbol: "NVDA", Date: day(2026, 9, 29), From: 1, To: 2}}}
	_, _ = store.Sync(context.Background(), src, time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC), 1)
	r, _ := store.splits()
	if !r.Checked.Equal(day(2026, 9, 28)) || len(r.Splits) != 0 {
		t.Errorf("after the first sync: %+v", r)
	}
	_, _ = store.Sync(context.Background(), src, time.Date(2026, 9, 29, 23, 0, 0, 0, time.UTC), 1)
	r, _ = store.splits()
	if !r.Checked.Equal(day(2026, 9, 29)) || len(r.Splits) != 1 {
		t.Errorf("after the second: %+v", r)
	}
}

// series builds a panel of sessions from closes, one list a symbol, all the
// same length, with a million shares a day.
func panelOf(closes map[string][]float64) *Panel {
	p := &Panel{series: map[string]*Series{}}
	n := 0
	for sym, cs := range closes {
		s := &Series{Symbol: sym}
		for _, c := range cs {
			s.Open = append(s.Open, c)
			s.Close = append(s.Close, c)
			s.Volume = append(s.Volume, 1e6)
		}
		p.series[sym] = s
		n = len(cs)
	}
	start := day(2024, 9, 2)
	for i := 0; i < n; i++ {
		p.Dates = append(p.Dates, start.AddDate(0, 0, i))
	}
	return p
}

// steady rises by rate a session, with a wobble that gives it a swing of
// about a fifth a year, as an ordinary share has.
func steady(n int, from, rate float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = from * math.Pow(1+rate, float64(i)) * (1 + 0.02*math.Sin(float64(i)))
	}
	return out
}

func TestTheMeasuresReadTheSeries(t *testing.T) {
	cs := make([]float64, 300)
	for i := range cs {
		cs[i] = 100 + float64(i)
	}
	s := panelOf(map[string][]float64{"AAA": cs}).Get("AAA")
	if got := s.Return(Year); math.Abs(got-(399.0/147-1)) > 1e-9 {
		t.Errorf("year = %v", got)
	}
	if got := s.ReturnBetween(Year, Month); math.Abs(got-(378.0/147-1)) > 1e-9 {
		t.Errorf("year less a month = %v", got)
	}
	if got := s.Average(200, 0); got != 299.5 {
		t.Errorf("200-day average = %v", got)
	}
	if !math.IsNaN(s.Return(TwoYears)) {
		t.Error("a return longer than the history was given")
	}
}

// The leaders are the steady risers still above their long average; a share
// whose year came in one day, one pinned to an offer, and one below its
// average are left out, each saying why.
func TestLeadersRankSteadyRisersAndLeaveOutJumps(t *testing.T) {
	n := TwoYears
	jump := steady(n, 50, 0.0001)
	for i := n - 100; i < n; i++ {
		jump[i] *= 1.6
	}
	pinned := steady(n, 50, 0.0005)
	for i := n - 60; i < n; i++ {
		pinned[i] = pinned[n-61] * 1.3
	}
	faller := steady(n, 100, -0.001)
	p := panelOf(map[string][]float64{
		"FAST": steady(n, 20, 0.003),
		"SLOW": steady(n, 20, 0.001),
		"JUMP": jump,
		"PIN":  pinned,
		"DOWN": faller,
		"SPY":  steady(n, 500, 0.0005),
	})
	var listings []Listing
	for _, sym := range []string{"FAST", "SLOW", "JUMP", "PIN", "DOWN"} {
		listings = append(listings, Listing{Symbol: sym, Name: sym + " Inc.", Industry: "Widgets", MarketCap: 5e9})
	}
	rules := DefaultRules
	rules.MinDollarVolume = 1e6

	leaders, left := Leaders(Measure(p, listings), rules, 10)
	var got []string
	for _, l := range leaders {
		got = append(got, l.Symbol)
	}
	if strings.Join(got, ",") != "FAST,SLOW" {
		t.Errorf("leaders = %v", got)
	}
	why := map[string]string{}
	for _, l := range left {
		why[l.Symbol] = l.Why
	}
	for sym, want := range map[string]string{"JUMP": "one day", "PIN": "takeover", "DOWN": "200-day"} {
		if !strings.Contains(why[sym], want) {
			t.Errorf("%s left out as %q, want %q", sym, why[sym], want)
		}
	}
}

// An industry risen across its members is popular; one whose year lags but
// whose last three months are turning up with more of its shares above
// their fifty-day average is early. One whose year lags and is still
// sliding is neither.
func TestIndustriesAreScoredPopularAndEarly(t *testing.T) {
	n := TwoYears
	closes := map[string][]float64{"SPY": steady(n, 500, 0.0004)}
	var listings []Listing
	add := func(industry string, count int, series func(k int) []float64) {
		for k := 0; k < count; k++ {
			sym := industry[:3] + string(rune('A'+k))
			closes[sym] = series(k)
			listings = append(listings, Listing{Symbol: sym, Name: sym, Industry: industry, MarketCap: 5e9})
		}
	}
	add("HOTTEST", 5, func(k int) []float64 { return steady(n, 20, 0.003+0.0001*float64(k)) })
	add("FLAT", 5, func(k int) []float64 { return steady(n, 20, 0.0001*float64(k)) })
	add("TURNING", 5, func(k int) []float64 {
		// Two years of falling, then three weeks of climbing: below their
		// fifty-day average a month ago, above it now.
		cs := steady(n, 40, -0.001)
		for i := n - 15; i < n; i++ {
			cs[i] = cs[n-16] * math.Pow(1.01+0.0002*float64(k), float64(i-(n-16)))
		}
		return cs
	})
	add("SLIDING", 5, func(k int) []float64 { return steady(n, 40, -0.002-0.0001*float64(k)) })
	add("TINY", 3, func(k int) []float64 { return steady(n, 20, 0.004) })

	rules := DefaultRules
	rules.MinDollarVolume = 1e6
	p := panelOf(closes)
	spy := MeasureSeries(p.Get("SPY"), Listing{Symbol: "SPY"})
	inds := Industries(Measure(p, listings), spy, rules, map[string]int{"TURA": 2})

	if len(inds) != 4 {
		t.Fatalf("industries = %d, want four: TINY has too few members", len(inds))
	}
	if pop := Popular(inds, 1); len(pop) != 1 || pop[0].Name != "HOTTEST" {
		t.Errorf("popular = %+v", pop)
	}
	early := Early(inds, 3)
	if len(early) != 1 || early[0].Name != "TURNING" || early[0].Mentions != 2 {
		t.Errorf("early = %+v", early)
	}
	if early[0].Breadth50 <= early[0].Breadth50Before {
		t.Errorf("breadth %v from %v", early[0].Breadth50, early[0].Breadth50Before)
	}
}

// A move counts where it is several times the share's usual, on heavy
// trading; a big move on a volatile share, or on light trading, does not.
func TestMovesAreMeasuredAgainstTheSharesUsual(t *testing.T) {
	n := 80
	calm := steady(n, 100, 0)
	calm[n-1] = calm[n-2] * 1.08
	wild := make([]float64, n)
	for i := range wild {
		wild[i] = 100 * (1 + 0.06*math.Sin(float64(i)*1.7))
	}
	wild[n-1] = wild[n-2] * 1.08
	quiet := steady(n, 100, 0)
	quiet[n-1] = quiet[n-2] * 0.9
	p := panelOf(map[string][]float64{"CALM": calm, "WILD": wild, "QUIET": quiet})
	for _, sym := range []string{"CALM", "WILD"} {
		p.Get(sym).Volume[n-1] = 5e6
	}
	var listings []Listing
	for _, sym := range []string{"CALM", "WILD", "QUIET"} {
		listings = append(listings, Listing{Symbol: sym, Name: sym, MarketCap: 5e9})
	}
	rules := DefaultRules
	rules.MinDollarVolume = 1e6

	moves := Moves(p, listings, rules, 3, 2)
	if len(moves) != 1 || moves[0].Symbol != "CALM" || math.Abs(moves[0].Percent-0.08) > 1e-9 || moves[0].Busy < 4 {
		t.Errorf("moves = %+v", moves)
	}
}

func TestPlainNamesAndMentions(t *testing.T) {
	for listed, want := range map[string]string{
		"Agilent Technologies Inc. Common Stock":                    "Agilent Technologies",
		"Alphabet Inc. Class A Common Stock":                        "Alphabet",
		"Taiwan Semiconductor Manufacturing Company Ltd.":           "Taiwan Semiconductor Manufacturing",
		"First Solar, Inc. Common Stock":                            "First Solar",
		"Sea Limited American Depositary Shares, each representing": "Sea",
	} {
		if got := PlainName(listed); got != want {
			t.Errorf("PlainName(%q) = %q, want %q", listed, got, want)
		}
	}
	titles := []string{
		"Agilent beats on instruments demand",
		"First quarter rush for solar panels",
		"First Solar raises guidance",
		"Pharmacy chains merge",
		"Trump signs order on drug prices",
		"Stock futures slip as China data disappoints",
		"People are buying fewer cars",
		"China Yuchai wins engine order",
		"Morgan Stanley upgrades chipmakers",
	}
	got := Mentions(titles, []Listing{
		{Symbol: "A", Name: "Agilent Technologies Inc. Common Stock"},
		{Symbol: "FSLR", Name: "First Solar, Inc. Common Stock"},
		{Symbol: "ARM", Name: "Arm Holdings plc American Depositary Shares"},
		{Symbol: "DJT", Name: "Trump Media & Technology Group Corp. Common Stock"},
		{Symbol: "SYBT", Name: "Stock Yards Bancorp, Inc. Common Stock"},
		{Symbol: "PPLI", Name: "People Inc. Common Stock"},
		{Symbol: "CYD", Name: "China Yuchai International Limited Common Stock"},
		{Symbol: "CPHI", Name: "China Pharma Holdings, Inc. Common Stock"},
		{Symbol: "MS", Name: "Morgan Stanley Common Stock"},
		{Symbol: "SWK", Name: "Stanley Black & Decker, Inc. Common Stock"},
	})
	want := map[string]int{"A": 1, "FSLR": 1, "ARM": 0, "DJT": 0, "SYBT": 0, "PPLI": 0, "CPHI": 0, "MS": 1, "SWK": 0}
	for sym, n := range want {
		if got[sym] != n {
			t.Errorf("%s named in %d headlines, want %d (all: %v)", sym, got[sym], n, got)
		}
	}
	if got["CYD"] != 0 {
		t.Errorf("CYD counted by its first word, which begins another company's name: %v", got)
	}
}
