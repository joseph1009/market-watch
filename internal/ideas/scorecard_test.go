package ideas

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func TestARecordNeedsAChartToMeasureFrom(t *testing.T) {
	at := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	idea := model.Idea{Name: "Rambus", Ticker: "RMBS", Exchange: "US", Verdict: model.Buy,
		Trading: &model.Trading{Last: 100, Currency: "USD"}}

	r, ok := NewRecord(idea, "RMBS", at)
	if !ok || r.Price != 100 || r.Symbol != "RMBS" || r.Entry != 0 {
		t.Errorf("record = %+v, %v", r, ok)
	}
	if _, ok := NewRecord(idea, "", at); ok {
		t.Error("a verdict with no chart was recorded; it could never be scored")
	}
}

// day is a New York session: opened at 13:30 UTC on the day.
func day(d int, open, close float64) Session {
	return Session{Opened: time.Date(2026, 9, d, 13, 30, 0, 0, time.UTC), Open: open, Close: close}
}

func TestTheScorecardMeasuresEachVerdictAgainstTheIndex(t *testing.T) {
	path := t.TempDir() + "/scorecard.json"
	s, err := LoadScorecard(path)
	if err != nil {
		t.Fatal(err)
	}
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := then.Add(30 * 24 * time.Hour)
	if err := s.Add(
		Record{At: then, Name: "Winner", Symbol: "WIN", Chart: "WIN", Verdict: model.Buy, Price: 90},
		Record{At: then, Name: "Loser", Symbol: "LOS", Chart: "LOS", Verdict: model.Buy, Price: 90},
		Record{At: then, Name: "Faller", Symbol: "FAL", Chart: "FAL", Verdict: model.Sell, Price: 90},
		Record{At: now.Add(-24 * time.Hour), Name: "Fresh", Symbol: "NEW", Chart: "NEW", Verdict: model.Buy, Price: 10},
	); err != nil {
		t.Fatal(err)
	}

	// Reloaded, as /scorecard will find it on another day.
	s, err = LoadScorecard(path)
	if err != nil || len(s.All()) != 4 {
		t.Fatalf("reloaded %d records, err %v", len(s.All()), err)
	}

	due := s.Due(now, 0)
	if strings.Join(due, ",") != "FAL,LOS,WIN" {
		t.Errorf("due = %v, want the three a week old, newest first, and not the fresh one", due)
	}

	// Each opened at 100 the day of the verdict, whatever it closed at the
	// night before, and the index at 500. The index is up 10%. The winner
	// rose 30%, the loser 5%, the faller fell 10%.
	paths := map[string]Path{
		"WIN": {day(1, 100, 101), day(30, 128, 130)},
		"LOS": {day(1, 100, 99), day(30, 104, 105)},
		"FAL": {day(1, 100, 98), day(30, 91, 90)},
	}
	bench := Path{day(1, 500, 502), day(30, 548, 550)}
	text := s.Summary(now, paths, bench, nil, time.UTC)
	for _, want := range []string{
		"4 verdicts since 1 Sep",
		"3 are a week old or more",
		"1 is newer",
		// +20 and -5 points against the index.
		"<b>BUY</b>, 2 scored: 1 on course to beat the S&amp;P 500 by 5 points (50%), on average 7.5 points ahead of it",
		"<b>SELL</b>, 1 scored: 1 on course to trail it by 5 points (100%), on average 20.0 points behind it",
		"Winner <code>WIN</code>, BUY on 1 Sep: +30.0%, against +10.0% for the index",
		"Faller <code>FAL</code>, SELL on 1 Sep: -10.0%",
		"the next session's open",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary is missing %q:\n%s", want, text)
		}
	}
	// Every call came the same way, so there is no split to show.
	if strings.Contains(text, "By how the company was found") {
		t.Errorf("a breakdown into one group was shown:\n%s", text)
	}
}

// The closer look is written before the open from the night's news. The share
// that news sends up 10% at the open was never to be had at the close before
// it, so the verdict is measured from the open.
func TestAVerdictBeforeTheOpenIsMeasuredFromTheOpen(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	// 07:50 in New York on 1 September, before the 09:30 open.
	given := time.Date(2026, 9, 1, 11, 50, 0, 0, time.UTC)
	if err := s.Add(Record{At: given, Name: "Gapper", Symbol: "GAP", Chart: "GAP", Verdict: model.Buy, Price: 100}); err != nil {
		t.Fatal(err)
	}
	// The session of 31 August opened before the verdict, and is not it.
	aug31 := func(open, close float64) Session {
		s := day(1, open, close)
		s.Opened = s.Opened.AddDate(0, 0, -1)
		return s
	}
	paths := map[string]Path{"GAP": {aug31(99, 100), day(1, 110, 112), day(29, 118, 121)}}
	bench := Path{aug31(500, 500), day(1, 500, 501), day(29, 505, 505)}
	now := given.Add(30 * 24 * time.Hour)

	text := s.Summary(now, paths, bench, nil, time.UTC)
	// From the open at 110, +10%; from the close at 100 it would have read +21%.
	if !strings.Contains(text, "Gapper <code>GAP</code>, BUY on 1 Sep: +10.0%, against +1.0% for the index") {
		t.Errorf("not measured from the open:\n%s", text)
	}

	// Settling writes the entry down, so it outlives the two years of
	// history the chart source serves.
	if err := s.Settle(paths, bench, nil); err != nil {
		t.Fatal(err)
	}
	r := s.All()[0]
	if r.Entry != 110 || r.EntryBenchmark != 500 || !r.EntryAt.Equal(day(1, 0, 0).Opened) {
		t.Errorf("settled = %+v", r)
	}
	s, _ = LoadScorecard(s.Path)
	if s.All()[0].Entry != 110 {
		t.Errorf("the entry was not saved: %+v", s.All()[0])
	}
	text = s.Summary(now, map[string]Path{"GAP": {day(29, 118, 121)}}, Path{day(29, 505, 505)}, nil, time.UTC)
	if !strings.Contains(text, "+10.0%") {
		t.Errorf("a settled verdict lost its entry when the history no longer reached it:\n%s", text)
	}
}

// A verdict whose next session has not happened has nothing to be measured
// from yet.
func TestAVerdictWithNoSessionSinceIsNotScored(t *testing.T) {
	s, _ := LoadScorecard(t.TempDir() + "/scorecard.json")
	given := time.Date(2026, 9, 1, 11, 50, 0, 0, time.UTC)
	_ = s.Add(Record{At: given, Name: "Halted", Symbol: "HLT", Chart: "HLT", Verdict: model.Buy})
	before := day(1, 10, 10)
	before.Opened = before.Opened.AddDate(0, 0, -1)
	text := s.Summary(given.Add(10*24*time.Hour), map[string]Path{"HLT": {before}},
		Path{before, day(8, 500, 505)}, nil, time.UTC)
	if !strings.Contains(text, "1 could not be priced") {
		t.Errorf("summary = %s", text)
	}
}

// The index is a dollar fund, so a share priced abroad is scored in dollars:
// up 10% in yen while the yen fell 10% is down 1% to a dollar investor.
func TestASharePricedAbroadIsScoredInDollars(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	// 20:00 in Tokyo, after its close: the next session is the 2nd.
	then := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	now := then.Add(30 * 24 * time.Hour)
	if err := s.Add(
		Record{At: then, Name: "Advantest", Symbol: "6857.T", Chart: "6857.T", Verdict: model.Buy, Price: 990, Currency: "JPY", FX: 0.0071},
		Record{At: then, Name: "SK Hynix", Symbol: "000660.KS", Chart: "000660.KS", Verdict: model.Buy, Price: 99, Currency: "KRW"},
		// A currency whose rate cannot be read goes unscored.
		Record{At: then, Name: "Tencent", Symbol: "0700.HK", Chart: "0700.HK", Verdict: model.Buy, Price: 500, Currency: "HKD"},
	); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Currencies(now), ","); got != "JPY,KRW,HKD" {
		t.Errorf("currencies = %s", got)
	}

	asia := func(d int, open, close float64) Session {
		return Session{Opened: time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC), Open: open, Close: close}
	}
	date := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	rates := Rates{
		"JPY": {{date(1), 0.0071}, {date(2), 0.0070}, {date(30), 0.0063}},
		// The won on the 2nd, the day of the entry: not the 1st's, when the
		// verdict was given, nor the 3rd's.
		"KRW": {{date(1), 0.00070}, {date(2), 0.00080}, {date(3), 0.00090}, {date(30), 0.00088}},
	}
	paths := map[string]Path{
		"6857.T":    {asia(1, 980, 990), asia(2, 1000, 1010), asia(30, 1090, 1100)},
		"000660.KS": {asia(1, 98, 99), asia(2, 100, 101), asia(30, 100, 100)},
		"0700.HK":   {asia(1, 490, 500), asia(2, 500, 505), asia(30, 590, 600)},
	}
	bench := Path{day(1, 500, 500), day(30, 500, 500)}
	text := s.Summary(now, paths, bench, rates, time.UTC)
	for _, want := range []string{
		"2 are a week old or more",
		"1 could not be priced in dollars today",
		// -1% for Advantest and +10% for SK Hynix, against a flat index.
		"<b>BUY</b>, 2 scored: 1 on course to beat the S&amp;P 500 by 5 points (50%), on average 4.5 points ahead of it",
		"SK Hynix <code>000660.KS</code>, BUY on 1 Sep: +10.0% in US dollars",
		"Advantest <code>6857.T</code>, BUY on 1 Sep: -1.0% in US dollars",
		"Shares listed abroad are counted in US dollars",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary is missing %q:\n%s", want, text)
		}
	}
}

// Whether high confidence is worth more than low, and whether the themes do
// better than the news, is what the breakdowns are for.
func TestTheScorecardSplitsCallsByConfidenceAndSource(t *testing.T) {
	s, _ := LoadScorecard(t.TempDir() + "/scorecard.json")
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	_ = s.Add(
		Record{At: then, Name: "A", Symbol: "A", Chart: "A", Verdict: model.Buy, Confidence: "high", Source: SourceTheme, Theme: "Power"},
		Record{At: then, Name: "B", Symbol: "B", Chart: "B", Verdict: model.Sell, Confidence: "high", Source: SourceReaction},
		Record{At: then, Name: "C", Symbol: "C", Chart: "C", Verdict: model.Buy, Confidence: "low"},
		Record{At: then, Name: "D", Symbol: "D", Chart: "D", Verdict: model.Hold, Confidence: "low"},
	)
	paths := map[string]Path{
		"A": {day(1, 100, 100), day(30, 120, 120)}, // +20, 10 ahead
		"B": {day(1, 100, 100), day(30, 90, 90)},   // -10, 20 behind, as called
		"C": {day(1, 100, 100), day(30, 100, 100)}, // flat, 10 behind
		"D": {day(1, 100, 100), day(30, 100, 100)},
	}
	bench := Path{day(1, 500, 500), day(30, 550, 550)}
	text := s.Summary(then.Add(30*24*time.Hour), paths, bench, nil, time.UTC)
	for _, want := range []string{
		"<b>By confidence</b>, BUYs and SELLs together",
		"• high: 2 of 2 on course (100%), on average +15.0 points the way called",
		"• low: 0 of 1 on course (0%), on average -10.0 points the way called",
		"<b>By how the company was found</b>",
		"• weekly themes, from the numbers: 1 of 1 on course",
		"• reactions to the news: 1 of 1 on course",
		"• the old closer look, from the news: 0 of 1 on course",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "medium") {
		t.Errorf("a confidence nobody gave was listed:\n%s", text)
	}
}

func TestAnEmptyScorecardSaysWhenItStarts(t *testing.T) {
	s, _ := LoadScorecard(t.TempDir() + "/none.json")
	if text := s.Summary(time.Now(), nil, nil, nil, time.UTC); !strings.Contains(text, "No verdicts yet") {
		t.Errorf("summary = %q", text)
	}
}
