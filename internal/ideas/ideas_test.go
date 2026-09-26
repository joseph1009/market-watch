package ideas

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/model"
)

var cited = []model.Article{
	{ID: "a1", Title: "Micron raises HBM outlook", SourceName: "CNBC", URL: "https://example.com/1"},
	{ID: "a2", Title: "Memory prices climb", SourceName: "Reuters", URL: "https://example.com/2"},
}

// fakeCompleter answers with a fixed reply and remembers what it was asked.
type fakeCompleter struct {
	reply          string
	err            error
	system, prompt string
}

func (f *fakeCompleter) Complete(_ context.Context, system, prompt string) (string, model.Usage, error) {
	f.system, f.prompt = system, prompt
	return f.reply, model.Usage{InputTokens: 10, OutputTokens: 5}, f.err
}

// fakeVerifier registers the names it is given, by ticker.
type fakeVerifier map[string]string

func (v fakeVerifier) Verify(_ context.Context, queries []discover.Query) ([]string, error) {
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = v[q.Ticker+"."+q.Exchange]
	}
	return out, nil
}

func TestResearchKeepsOnlyCompaniesWhoseTickerChecksOut(t *testing.T) {
	c := &fakeCompleter{reply: strings.Join([]string{
		"Some preamble the model should not have written.",
		"Rambus|RMBS|US|connected|1|Its interface chips go into every HBM stack.",
		"SK Hynix|000660|KS|connected|1,2|The largest HBM maker.",
		"Made Up Memory|MUM|US|connected|1|Invented, and the exchange says so.",
		"Wrong Ticker Co|AAPL|US|news|2|The ticker belongs to someone else.",
		"OpenAI|private|-|news|1|Private, so nothing to judge.",
		"Rambus|RMBS|US|connected|2|Named twice.",
		"Samsung Electronics|005930|ZZ|connected|1|An exchange outside the list.",
	}, "\n")}
	r := &Researcher{Completer: c, Verifier: fakeVerifier{
		"RMBS.US":   "RAMBUS INC",
		"000660.KS": "SK HYNIX INC",
		"AAPL.US":   "APPLE INC",
	}}

	got, _, err := r.Propose(context.Background(), Input{Brief: "OVERVIEW\nMicron [1].", Cited: cited})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(got) != 2 || got[0].Ticker != "RMBS" || got[1].Symbol() != "000660.KS" {
		t.Fatalf("got %+v, want Rambus and SK Hynix only", got)
	}
	if got[0].Listed != "RAMBUS INC" || !got[0].Connected {
		t.Errorf("Rambus = %+v, want its registered name and marked connected", got[0])
	}
	if len(got[1].Articles) != 2 || got[1].Articles[1].ID != "a2" {
		t.Errorf("SK Hynix articles = %+v, want both cited stories", got[1].Articles)
	}
	if !strings.Contains(c.system, "up to 20 listed companies") {
		t.Errorf("the system prompt did not carry the limit: %q", c.system[:200])
	}
	if !strings.Contains(c.prompt, "[1] Micron raises HBM outlook (CNBC)") {
		t.Errorf("the prompt did not number the brief's articles:\n%s", c.prompt)
	}
}

func TestResearchIsCappedAtItsMaximum(t *testing.T) {
	var lines []string
	verifier := fakeVerifier{}
	for _, tk := range []string{"AAA", "BBB", "CCC", "DDD"} {
		lines = append(lines, tk+" Corp|"+tk+"|US|news|1|Something happened.")
		verifier[tk+".US"] = tk + " CORP"
	}
	r := &Researcher{Completer: &fakeCompleter{reply: strings.Join(lines, "\n")}, Verifier: verifier, Max: 3}

	got, _, err := r.Propose(context.Background(), Input{Cited: cited})
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d ideas, err %v; want 3", len(got), err)
	}
}

// Without a check there is no telling a real ticker from a plausible one, so
// a failed check returns nothing rather than unchecked tickers.
func TestResearchReturnsNothingWhenTheCheckFails(t *testing.T) {
	r := &Researcher{
		Completer: &fakeCompleter{reply: "Rambus|RMBS|US|connected|1|Interface chips."},
		Verifier:  failingVerifier{},
	}
	got, _, err := r.Propose(context.Background(), Input{Cited: cited})
	if err == nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want an error and nothing", got, err)
	}
}

type failingVerifier struct{}

func (failingVerifier) Verify(context.Context, []discover.Query) ([]string, error) {
	return nil, errors.New("openfigi: 429 Too Many Requests")
}

func TestVerdictsAreMatchedToTheirCompany(t *testing.T) {
	ideas := []model.Idea{
		{Name: "Rambus", Ticker: "RMBS", Exchange: "US", Listed: "RAMBUS INC", Connected: true, Link: "Interface chips.", Articles: cited[:1]},
		{Name: "SK Hynix", Ticker: "000660", Exchange: "KS", Listed: "SK HYNIX INC", Link: "Largest HBM maker."},
		{Name: "Skipped Co", Ticker: "SKP", Exchange: "US", Listed: "SKIPPED CO", Link: "The reply leaves it out."},
		{Name: "Odd Co", Ticker: "ODD", Exchange: "US", Listed: "ODD CO", Link: "Given a verdict that is not one."},
	}
	reply := `=== RMBS
VERDICT: BUY
CONFIDENCE: Medium
CASE: HBM demand runs through its chips [1].
It has no net debt.
NUMBERS: revenue +18% a year; 38x earnings
RISK: One customer is a fifth of sales.

=== 000660.KS
VERDICT: **HOLD**
CONFIDENCE: low
CASE: Priced for the upturn already.
NUMBERS: 8% below its 50-day average
RISK: Memory prices turn fast.

=== ODD
VERDICT: STRONG BUY
CONFIDENCE: high`

	c := &fakeCompleter{reply: reply}
	got, _, err := (&Judge{Completer: c}).Judge(context.Background(), ideas, []string{"FACTS FOR RAMBUS", "FACTS FOR HYNIX", "", ""}, cited, nil)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d verdicts, want 2 (the skipped and the invalid dropped): %+v", len(got), got)
	}
	r, h := got[0], got[1]
	if r.Verdict != model.Buy || r.Confidence != "medium" || r.Case != "HBM demand runs through its chips [1]. It has no net debt." {
		t.Errorf("Rambus = %+v", r)
	}
	if r.Numbers != "revenue +18% a year; 38x earnings" || r.Risk != "One customer is a fifth of sales." {
		t.Errorf("Rambus numbers/risk = %q / %q", r.Numbers, r.Risk)
	}
	if h.Verdict != model.Hold || h.Confidence != "low" {
		t.Errorf("SK Hynix = %+v", h)
	}

	for _, want := range []string{"=== RMBS", "=== 000660.KS", "FACTS FOR RAMBUS", "Not in today's news, but connected to it: Interface chips. [1]", "[1] Micron raises HBM outlook (CNBC)"} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the verdict prompt is missing %q:\n%s", want, c.prompt)
		}
	}
}

func TestARecordNeedsAPriceToMeasureFrom(t *testing.T) {
	at := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	idea := model.Idea{Name: "Rambus", Ticker: "RMBS", Exchange: "US", Verdict: model.Buy,
		Trading: &model.Trading{Last: 100, Currency: "USD"}}

	r, ok := NewRecord(idea, "RMBS", 500, at)
	if !ok || r.Price != 100 || r.Benchmark != 500 || r.Symbol != "RMBS" {
		t.Errorf("record = %+v, %v", r, ok)
	}
	idea.Trading = nil
	if _, ok := NewRecord(idea, "RMBS", 500, at); ok {
		t.Error("a verdict with no price was recorded; it could never be scored")
	}
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
		Record{At: then, Name: "Winner", Symbol: "WIN", Chart: "WIN", Verdict: model.Buy, Price: 100, Benchmark: 500},
		Record{At: then, Name: "Loser", Symbol: "LOS", Chart: "LOS", Verdict: model.Buy, Price: 100, Benchmark: 500},
		Record{At: then, Name: "Faller", Symbol: "FAL", Chart: "FAL", Verdict: model.Sell, Price: 100, Benchmark: 500},
		Record{At: now.Add(-24 * time.Hour), Name: "Fresh", Symbol: "NEW", Chart: "NEW", Verdict: model.Buy, Price: 10, Benchmark: 550},
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

	// The index is up 10%. The winner rose 30%, the loser 5%, the faller fell 10%.
	text := s.Summary(now, map[string]float64{"WIN": 130, "LOS": 105, "FAL": 90}, 550, nil, time.UTC)
	for _, want := range []string{
		"4 verdicts since 1 Sep",
		"3 are a week old or more",
		"1 is newer",
		// +20 and -5 points against the index.
		"<b>BUY</b>, 2 scored: 1 on course to beat the S&amp;P 500 by 5 points (50%), on average 7.5 points ahead of it",
		"<b>SELL</b>, 1 scored: 1 on course to trail it by 5 points (100%), on average 20.0 points behind it",
		"Winner <code>WIN</code>, BUY on 1 Sep: +30.0%, against +10.0% for the index",
		"Faller <code>FAL</code>, SELL on 1 Sep: -10.0%",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary is missing %q:\n%s", want, text)
		}
	}
}

// A BUY promises five points over a year, so it is held to that pace: a
// month in, a fifth of a point ahead is not yet on course; past the year, four
// points is short and six is enough.
func TestAVerdictIsHeldToThePaceOfItsMargin(t *testing.T) {
	then := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		age   time.Duration
		ahead float64
		right bool
	}{
		{"a month, 0.2 points ahead", 30 * 24 * time.Hour, 0.002, false},
		{"a month, 0.5 points ahead", 30 * 24 * time.Hour, 0.005, true},
		{"past the year, 4 points ahead", 400 * 24 * time.Hour, 0.04, false},
		{"past the year, 6 points ahead", 400 * 24 * time.Hour, 0.06, true},
	} {
		buy := scored{Record: Record{At: then, Verdict: model.Buy}, gain: 0.10 + tc.ahead, index: 0.10, age: tc.age}
		if got, _ := buy.right(); got != tc.right {
			t.Errorf("BUY, %s: right = %v, want %v", tc.name, got, tc.right)
		}
		sell := scored{Record: Record{At: then, Verdict: model.Sell}, gain: 0.10 - tc.ahead, index: 0.10, age: tc.age}
		if got, _ := sell.right(); got != tc.right {
			t.Errorf("SELL, %s: right = %v, want %v", tc.name, got, tc.right)
		}
	}
}

// The index is a dollar fund, so a share priced abroad is scored in dollars:
// up 10% in yen while the yen fell 10% is down 1% to a dollar investor.
func TestASharePricedAbroadIsScoredInDollars(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	now := then.Add(30 * 24 * time.Hour)
	if err := s.Add(
		// The rate kept when the verdict was given.
		Record{At: then, Name: "Advantest", Symbol: "6857.T", Chart: "6857.T", Verdict: model.Buy, Price: 1000, Currency: "JPY", FX: 0.0070, Benchmark: 500},
		// Given before rates were kept: looked up from the history, to the day.
		Record{At: then, Name: "SK Hynix", Symbol: "000660.KS", Chart: "000660.KS", Verdict: model.Buy, Price: 100, Currency: "KRW", Benchmark: 500},
		// A currency whose rate cannot be read goes unscored.
		Record{At: then, Name: "Tencent", Symbol: "0700.HK", Chart: "0700.HK", Verdict: model.Buy, Price: 500, Currency: "HKD", Benchmark: 500},
	); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Currencies(now), ","); got != "JPY,KRW,HKD" {
		t.Errorf("currencies = %s", got)
	}

	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	rates := Rates{
		"JPY": {{day(1), 0.0070}, {day(30), 0.0063}},
		// The won: 0.00080 on the 1st, the day the verdict was given, not the
		// 0.00070 before it or the 0.00090 after.
		"KRW": {{time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), 0.00070}, {day(1), 0.00080}, {day(2), 0.00090}, {day(30), 0.00088}},
	}
	text := s.Summary(now, map[string]float64{"6857.T": 1100, "000660.KS": 100, "0700.HK": 600}, 500, rates, time.UTC)
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

func TestAnEmptyScorecardSaysWhenItStarts(t *testing.T) {
	s, _ := LoadScorecard(t.TempDir() + "/none.json")
	if text := s.Summary(time.Now(), nil, 0, nil, time.UTC); !strings.Contains(text, "No verdicts yet") {
		t.Errorf("summary = %q", text)
	}
}

// The fact sheet now carries prices from two places: the US quote feed, in
// dollars, and the chart source, in whatever the share trades in. Set side by
// side in one request without their units, a Hong Kong price and a US one
// invite a comparison that is nonsense.
func TestTheFactSheetNamesTheCurrencyOfEachPrice(t *testing.T) {
	ideas := []model.Idea{
		{Name: "Rambus", Ticker: "RMBS", Exchange: "US", Listed: "RAMBUS INC", Link: "Interface chips.",
			Quote: &model.Quote{Symbol: "RMBS", Price: 94.20, Percent: 1.2}},
		{Name: "Tencent", Ticker: "0700", Exchange: "HK", Listed: "TENCENT HOLDINGS LTD", Link: "Games and cloud.",
			Quote: &model.Quote{Symbol: "0700.HK", Price: 512.40, Percent: -0.8, Currency: "HKD"}},
	}

	c := &fakeCompleter{reply: "=== RMBS\nVERDICT: HOLD\nCONFIDENCE: low\nCASE: Nothing to act on."}
	if _, _, err := (&Judge{Completer: c}).Judge(context.Background(), ideas, []string{"", ""}, cited, nil); err != nil {
		t.Fatalf("Judge: %v", err)
	}

	for _, want := range []string{"Last session: +1.2%, at USD 94.20.", "Last session: -0.8%, at HKD 512.40."} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the verdict prompt is missing %q:\n%s", want, c.prompt)
		}
	}
}

// Before the open, a US share's price is the last close, and results out after
// it have not been traded on. The verdict and the screen are told which
// articles those are, so a share that has not moved is not read as having
// shrugged the news off.
func TestNewsAfterThePriceIsMarkedAsNotYetTraded(t *testing.T) {
	closed := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC) // 16:00 in New York
	articles := []model.Article{
		{ID: "before", Title: "Rambus shares rise", SourceName: "CNBC", Published: closed.Add(-2 * time.Hour)},
		{ID: "after", Title: "Rambus beats and raises", SourceName: "Reuters", Published: closed.Add(10 * time.Minute)},
		{ID: "undated", Title: "Rambus profile", SourceName: "Blog"},
	}
	idea := model.Idea{Name: "Rambus", Ticker: "RMBS", Exchange: "US", Listed: "RAMBUS INC", Link: "Results.",
		Articles: articles, Quote: &model.Quote{Symbol: "RMBS", Price: 94.20, Percent: 0.3, AsOf: closed}}

	c := &fakeCompleter{reply: "=== RMBS\nVERDICT: HOLD\nCONFIDENCE: low\nCASE: Wait."}
	if _, _, err := (&Judge{Completer: c}).Judge(context.Background(), []model.Idea{idea}, []string{""}, articles, nil); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if want := "Articles [2] came out after that price: the market has not traded on them yet."; !strings.Contains(c.prompt, want) {
		t.Errorf("the verdict prompt is missing %q:\n%s", want, c.prompt)
	}

	rows := []Row{{Ticker: "RMBS", Name: "Rambus", Sector: "Semiconductors", Line: "last session +0.3%", Articles: articles, PricedAt: closed}}
	if got, want := screenPrompt("Brief.", articles, rows), "today's articles [1][2][3], not yet traded on: [2]"; !strings.Contains(got, want) {
		t.Errorf("the screen prompt is missing %q:\n%s", want, got)
	}

	// A price with no time marks nothing.
	idea.Quote.AsOf = time.Time{}
	c = &fakeCompleter{reply: "=== RMBS\nVERDICT: HOLD\nCONFIDENCE: low\nCASE: Wait."}
	(&Judge{Completer: c}).Judge(context.Background(), []model.Idea{idea}, []string{""}, articles, nil)
	if strings.Contains(c.prompt, "not traded") {
		t.Errorf("a price with no time marked articles:\n%s", c.prompt)
	}
}

// An event is kept with a full date inside the window, and a line without one
// still reads as it always did.
func TestResearchReadsTheEventAhead(t *testing.T) {
	today := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	c := &fakeCompleter{reply: strings.Join([]string{
		"Rambus|RMBS|US|connected|1|Its chips go into every HBM stack.|Q3 results|2026-10-27|high|Bullish.",
		"SK Hynix|000660|KS|connected|1|The largest HBM maker.|Investor day|2027-03-01|medium|neutral",
		"Samsung|005930|KS|connected|2|A rival.|Results|October 2026|high|bearish",
		"Kioxia|285A|JP|connected|2|Memory.|none|||",
		"Lam Research|LRCX|US|connected|1|Sells the machines.",
		"Applied Materials|AMAT|US|connected|1|Also machines.|Earnings|2026-09-26|LOUD|up",
	}, "\n")}
	r := &Researcher{Completer: c, Verifier: fakeVerifier{
		"RMBS.US": "RAMBUS INC", "000660.KS": "SK HYNIX INC", "005930.KS": "SAMSUNG ELECTRONICS",
		"285A.JP": "KIOXIA HOLDINGS", "LRCX.US": "LAM RESEARCH CORP", "AMAT.US": "APPLIED MATERIALS INC",
	}}
	got, _, err := r.Propose(context.Background(), Input{Cited: cited, Today: today})
	if err != nil || len(got) != 6 {
		t.Fatalf("got %d, err %v", len(got), err)
	}
	if !strings.Contains(c.prompt, "Today is Saturday 26 September 2026.") {
		t.Errorf("the prompt does not say what day it is:\n%s", c.prompt)
	}

	e := got[0].Event
	if e == nil || e.Name != "Q3 results" || e.Date.Format(time.DateOnly) != "2026-10-27" || e.Impact != "HIGH" || e.Bias != "BULLISH" {
		t.Errorf("Rambus event = %+v", e)
	}
	if got[0].Link != "Its chips go into every HBM stack." {
		t.Errorf("the reason ran into the event: %q", got[0].Link)
	}
	for i, why := range []string{"", "past the window", "a month is not a date", "none", "no event given"} {
		if i > 0 && got[i].Event != nil {
			t.Errorf("%s: %s kept %+v", why, got[i].Name, got[i].Event)
		}
	}
	// Today counts; words outside the lists are left blank rather than kept.
	if e := got[5].Event; e == nil || e.Impact != "" || e.Bias != "" {
		t.Errorf("Applied Materials event = %+v", e)
	}
}
