package fundamentals

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

type fakeLookup struct{}

func (fakeLookup) LookupCIK(context.Context, string) (int, string, error) {
	return 1045810, "TEST CORP", nil
}

// conceptServer serves one canned response per tag, and 404s for anything else,
// exactly as EDGAR does for a concept the filer has never tagged.
func conceptServer(t *testing.T, byTag map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for tag, body := range byTag {
			if strings.HasSuffix(r.URL.Path, "/"+tag+".json") {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	return &Client{
		Lookup:  fakeLookup{},
		HTTP:    srv.Client(),
		BaseURL: srv.URL + "/api/xbrl/companyconcept/CIK%010d/%s/%s.json",
		Unpaced: true,
	}
}

func annualJSON(rows ...string) string {
	return `{"units":{"USD":[` + strings.Join(rows, ",") + `]}}`
}

func row(start, end string, val float64, form, filed string) string {
	return fmt.Sprintf(`{"start":%q,"end":%q,"val":%f,"form":%q,"fp":"FY","fy":2026,"filed":%q}`,
		start, end, val, form, filed)
}

func TestConceptReturnsNewestFirstAndFlagsWhatIsNotReported(t *testing.T) {
	c := conceptServer(t, map[string]string{
		"Revenues": annualJSON(
			row("2024-01-01", "2024-12-31", 100, "10-K", "2025-02-01"),
			row("2025-01-01", "2025-12-31", 150, "10-K", "2026-02-01"),
		),
	})

	obs, err := c.Concept(context.Background(), 1045810, "us-gaap", "Revenues")
	if err != nil {
		t.Fatalf("Concept: %v", err)
	}
	if obs[0].End.Year() != 2025 || obs[0].Value != 150 {
		t.Errorf("first observation = %v, want the 2025 period", obs[0])
	}
	if obs[0].Unit != "USD" || !obs[0].Duration() {
		t.Errorf("unit or period wrong: %+v", obs[0])
	}

	if _, err := c.Concept(context.Background(), 1045810, "us-gaap", "GrossProfit"); !errors.Is(err, ErrNotReported) {
		t.Errorf("err = %v, want ErrNotReported for a tag the filer never used", err)
	}
}

// An amendment restates a year the original filing already reported. The later
// filing has to win, or the analysis quotes a figure the company withdrew.
func TestAnnualPrefersTheMostRecentlyFiledCopy(t *testing.T) {
	c := conceptServer(t, map[string]string{
		"Revenues": annualJSON(
			row("2025-01-01", "2025-12-31", 150, "10-K", "2026-02-01"),
			row("2025-01-01", "2025-12-31", 140, "10-K/A", "2026-06-01"),
		),
	})

	obs, _ := c.Concept(context.Background(), 1045810, "us-gaap", "Revenues")
	years := Annual(obs)
	if len(years) != 1 {
		t.Fatalf("got %d years, want the restatement collapsed into one", len(years))
	}
	if years[0].Value != 140 {
		t.Errorf("value = %.0f, want the restated 140", years[0].Value)
	}
}

func TestAnnualRejectsQuarterlyAndInterimPeriods(t *testing.T) {
	c := conceptServer(t, map[string]string{
		"Revenues": annualJSON(
			row("2026-01-01", "2026-03-31", 40, "10-Q", "2026-05-01"),  // a quarter
			row("2025-01-01", "2025-06-30", 70, "10-K", "2026-02-01"),  // a half year
			row("2025-01-01", "2025-12-31", 150, "10-K", "2026-02-01"), // the year
		),
	})

	obs, _ := c.Concept(context.Background(), 1045810, "us-gaap", "Revenues")
	years := Annual(obs)
	if len(years) != 1 || years[0].Value != 150 {
		t.Errorf("got %v, want only the full year", years)
	}
	if got := Quarterly(obs); len(got) != 1 || got[0].Value != 40 {
		t.Errorf("Quarterly = %v, want only the three-month period", got)
	}
}

func TestFetchFallsBackToTheTagTheFilerActuallyUses(t *testing.T) {
	// No RevenueFromContractWithCustomer..., which is the first tag tried.
	c := conceptServer(t, map[string]string{
		"Revenues": annualJSON(row("2025-01-01", "2025-12-31", 150, "10-K", "2026-02-01")),
		"Assets":   `{"units":{"USD":[{"end":"2025-12-31","val":900,"form":"10-K","fy":2025,"fp":"FY","filed":"2026-02-01"}]}}`,
	})

	snap, err := c.Fetch(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Years) != 1 || !snap.Years[0].Figure("revenue").Known {
		t.Fatalf("revenue was not found through the fallback tag: %+v", snap.Years)
	}
	if snap.Balance.Figure("assets").Amount != 900 {
		t.Errorf("assets = %v, want 900", snap.Balance.Figure("assets"))
	}
	if !containsString(snap.Missing, "grossProfit") {
		t.Errorf("Missing = %v, want gross profit named as unreported", snap.Missing)
	}
}

// The distinction the whole package rests on: absent is not zero.
func TestMissingFiguresNeverBecomeNumbers(t *testing.T) {
	y := Year{Figures: map[string]Value{"revenue": known(100)}}
	if margin := Ratio(y.Figure("grossProfit"), y.Figure("revenue")); margin.Known {
		t.Errorf("a margin was computed from a missing gross profit: %v", margin)
	}
	if fcf := y.FreeCashFlow(); fcf.Known {
		t.Errorf("free cash flow was computed without cash flow: %v", fcf)
	}
	if div := Ratio(known(10), known(0)); div.Known {
		t.Error("division by zero produced a known value")
	}
}

func TestTableSaysNotReportedRatherThanZero(t *testing.T) {
	snap := Snapshot{
		Ticker: "TEST", Company: "Test Corp", CIK: 1,
		Years: []Year{{
			Label: "FY to Dec 2025", End: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			Figures: map[string]Value{"revenue": known(1_500_000_000)},
		}},
		Missing: []string{"grossProfit"},
	}

	table := snap.Table()
	if !strings.Contains(table, "1.50bn") {
		t.Errorf("revenue not shown with its scale:\n%s", table)
	}
	if !strings.Contains(table, "not reported") {
		t.Errorf("a missing line did not say so:\n%s", table)
	}
	if strings.Contains(table, "%!") {
		t.Errorf("a format verb failed:\n%s", table)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A filer that changed tags mid-history must still read as one continuous
// series. NVIDIA's revenue moved from RevenueFromContractWithCustomer... to
// Revenues in 2022, and taking the first tag that returned anything left four
// years reading "not reported".
func TestFetchMergesYearsAcrossATagChange(t *testing.T) {
	c := conceptServer(t, map[string]string{
		"RevenueFromContractWithCustomerExcludingAssessedTax": annualJSON(
			row("2021-02-01", "2022-01-30", 26914, "10-K", "2022-03-01"),
		),
		"Revenues": annualJSON(
			row("2024-01-29", "2025-01-26", 130497, "10-K", "2025-02-26"),
			row("2021-02-01", "2022-01-30", 26914, "10-K", "2022-03-01"),
		),
	})

	snap, err := c.Fetch(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Years) != 2 {
		t.Fatalf("got %d years, want both the old and the new tag: %+v", len(snap.Years), snap.Years)
	}
	if got := snap.Years[0].Figure("revenue"); !got.Known || got.Amount != 130497 {
		t.Errorf("newest revenue = %+v, want 130497 from the current tag", got)
	}
	if got := snap.Years[1].Figure("revenue"); !got.Known || got.Amount != 26914 {
		t.Errorf("oldest revenue = %+v, want 26914 from the retired tag", got)
	}
}

func TestAddAndLessStayUnknownWhenALineIsMissing(t *testing.T) {
	if got := Add(known(5), Value{}); got.Known {
		t.Errorf("Add with a missing side = %+v, want unknown", got)
	}
	if got := Add(known(5), known(3)); !got.Known || got.Amount != 8 {
		t.Errorf("Add(5,3) = %+v, want 8", got)
	}
}

// A foreign filer states each figure twice in one filing: in its own currency
// and as a US-dollar convenience translation. Picking between them arbitrarily
// cost TSMC four of its five years.
func TestDualCurrencyFilingKeepsTheReportingCurrency(t *testing.T) {
	period := func(unit string, vals ...string) string {
		return `"` + unit + `":[` + strings.Join(vals, ",") + `]`
	}
	twd := []string{
		row("2024-01-01", "2024-12-31", 2894308, "20-F", "2025-04-17"),
		row("2023-01-01", "2023-12-31", 2161736, "20-F", "2024-04-17"),
	}
	usd := []string{
		row("2024-01-01", "2024-12-31", 88268, "20-F", "2025-04-17"),
		row("2023-01-01", "2023-12-31", 70000, "20-F", "2024-04-17"),
	}
	c := conceptServer(t, map[string]string{
		"Revenue": `{"units":{` + period("TWD", twd...) + `,` + period("USD", usd...) + `}}`,
	})

	snap, err := c.Fetch(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Currency != "TWD" {
		t.Errorf("Currency = %q, want the currency most figures are in", snap.Currency)
	}
	if len(snap.Years) != 2 {
		t.Fatalf("got %d years, want both: %+v", len(snap.Years), snap.Years)
	}
	for _, y := range snap.Years {
		got := y.Figure("revenue")
		if !got.Known {
			t.Errorf("%s lost its revenue to the dollar translation", y.Label)
		}
		if got.Amount == 88268 || got.Amount == 70000 {
			t.Errorf("%s took the dollar translation: %.0f", y.Label, got.Amount)
		}
	}
}

// Earnings per share and earnings per American share are both filed, in
// different currencies. Taking the wrong one put 1.36 dollars in a column of
// Taiwan dollars, beside figures forty times larger.
func TestPerShareFiguresFollowTheReportingCurrency(t *testing.T) {
	c := conceptServer(t, map[string]string{
		"Revenue": `{"units":{"TWD":[` + row("2024-01-01", "2024-12-31", 2894308, "20-F", "2025-04-17") + `]}}`,
		"DilutedEarningsLossPerShare": `{"units":{"TWD/shares":[` +
			row("2024-01-01", "2024-12-31", 44.67, "20-F", "2025-04-17") + `],"USD/shares":[` +
			row("2024-01-01", "2024-12-31", 1.36, "20-F", "2025-04-17") + `]}}`,
	})

	snap, err := c.Fetch(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := snap.Years[0].Figure("epsDiluted"); !got.Known || got.Amount != 44.67 {
		t.Errorf("diluted EPS = %+v, want 44.67 in the reporting currency", got)
	}
}

// Every amount carries its own scale, so a figure read out of the table into
// the analysis cannot lose it on the way.
func TestAmountsCarryTheirScale(t *testing.T) {
	tests := map[float64]string{
		215_938_000_000:   "215.94bn",
		6_042_000_000:     "6.04bn",
		31_575_000:        "31.6m",
		-9_923_000_000:    "-9.92bn",
		2_894_307_700_000: "2.89tn",
		4_200:             "4k",
	}
	for in, want := range tests {
		if got := amount(known(in)); got != want {
			t.Errorf("amount(%.0f) = %q, want %q", in, got, want)
		}
	}
	if got := amount(Value{}); got != "not reported" {
		t.Errorf("a missing figure rendered as %q", got)
	}
}

// A half year against the same half of last year is a comparison; a half year
// against a full one is an artefact.
func TestGrowthComparesLikePeriods(t *testing.T) {
	half := func(label string, revenue float64) *Year {
		return &Year{Label: label, Figures: map[string]Value{"revenue": known(revenue)}}
	}
	snap := Snapshot{
		YTD:      half("Half year to 26 Jul 2026", 177_836),
		PriorYTD: half("Half year to 27 Jul 2025", 90_805),
		Years: []Year{
			{Label: "FY to Jan 2026", Figures: map[string]Value{"revenue": known(215_938)}},
			{Label: "FY to Jan 2025", Figures: map[string]Value{"revenue": known(130_497)}},
		},
	}

	columns := snap.columns()
	if len(columns) != 4 {
		t.Fatalf("got %d columns, want the two interim periods and two years", len(columns))
	}

	// The half year: 177,836 against 90,805 is 95.8%, not a comparison with a
	// full year.
	if got := snap.revenueGrowth(columns, 0); !got.Known || fmt.Sprintf("%.1f", got.Amount*100) != "95.8" {
		t.Errorf("year-to-date growth = %+v, want 95.8%% against the same half last year", got)
	}
	// The prior-year half has nothing of its own kind to compare against.
	if got := snap.revenueGrowth(columns, 1); got.Known {
		t.Errorf("the prior interim was given a growth rate: %+v", got)
	}
	// The latest full year against the one before it.
	if got := snap.revenueGrowth(columns, 2); !got.Known || fmt.Sprintf("%.1f", got.Amount*100) != "65.5" {
		t.Errorf("annual growth = %+v, want 65.5%%", got)
	}
}

func TestValuationUsesThePriceWhenTheCurrenciesMatch(t *testing.T) {
	snap := Snapshot{
		Ticker: "TEST", Company: "Test Corp", Currency: "USD",
		Price: &model.Quote{Price: 100, Percent: 1.2},
		Years: []Year{{
			Label:   "FY to Dec 2025",
			Figures: map[string]Value{"epsDiluted": known(5), "operatingCashFlow": known(3e9), "capitalExpenditure": known(1e9)},
		}},
		Balance: Balance{Figures: map[string]Value{
			"equity": known(20e9), "sharesOutstanding": known(1e9),
		}},
	}

	table := snap.Table()
	for _, want := range []string{
		"100.00 USD, +1.2% on its last session",
		"20.00x", // price 100 over earnings of 5
		"5.00x",  // price 100 over book value of 20 a share
		"Market value of the equity",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("valuation block is missing %q:\n%s", want, table)
		}
	}
}

// A dollar price over earnings filed in Taiwan dollars produces a confident
// number that means nothing, and a US listing may stand for several ordinary
// shares besides.
func TestValuationRefusesToMixCurrencies(t *testing.T) {
	snap := Snapshot{
		Ticker: "TSM", Currency: "TWD",
		Price: &model.Quote{Price: 413.75, Percent: -1.0},
		Years: []Year{{Label: "FY to Dec 2024", Figures: map[string]Value{"epsDiluted": known(44.67)}}},
		Balance: Balance{Figures: map[string]Value{
			"equity": known(4.2e12), "sharesOutstanding": known(25.9e9),
		}},
	}

	table := snap.Table()
	if !strings.Contains(table, "413.75 USD") {
		t.Errorf("the price is missing:\n%s", table)
	}
	if strings.Contains(table, "Price to earnings") || strings.Contains(table, "Price to book") {
		t.Errorf("multiples were computed across two currencies:\n%s", table)
	}
	if !strings.Contains(table, "No multiples are computed") {
		t.Errorf("the table does not say why there are no multiples:\n%s", table)
	}
}

func TestNoPriceMeansNoValuationBlock(t *testing.T) {
	snap := Snapshot{Ticker: "TEST", Currency: "USD",
		Years: []Year{{Label: "FY to Dec 2025", Figures: map[string]Value{"revenue": known(1e9)}}}}

	if table := snap.Table(); strings.Contains(table, "Market price") {
		t.Errorf("a valuation block appeared without a price:\n%s", table)
	}
}

// The case for and against each weigh the business, the figures and where
// they lead equally, as three groups under their own sub-headings, so neither
// a ratio-only case nor a story-only one gets through. The sub-headings are
// what the reader scans for.
func TestSystemPromptBalancesTheBusinessAndTheNumbers(t *testing.T) {
	for _, want := range []string{
		`"### In the business"`,
		`"### In the numbers"`,
		`"### What follows"`,
		"No group outranks another",
		"do not bring in market shares, customers or events from memory",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the prompt no longer carries %q", want)
		}
	}
}

// The analysis looks forward and questions its figures, with CAN SLIM's
// questions in mind and its traps named (asked for on 2026-10-02).
func TestSystemPromptLooksAheadAndQuestionsTheNumbers(t *testing.T) {
	for _, want := range []string{
		"It is not to tell the reader whether to buy",
		"WHERE IT IS HEADING",
		"DO THE NUMBERS HOLD UP",
		"looks right, looks wrong or needs a closer look",
		"Think one step past the obvious",
		"CAN SLIM",
		"Do not write the checklist out",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the prompt no longer carries %q", want)
		}
	}
}

func TestSystemPromptAllowsMultiplesButNotVerdicts(t *testing.T) {
	for _, want := range []string{
		"Where a market price and multiples are given",
		"Do not call a multiple cheap or expensive",
		"valuation cannot be addressed",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the prompt no longer carries %q", want)
		}
	}
}

// A company mid-cycle makes the last full year a poor divisor. Micron's annual
// earnings were US$7.59 a share while it had earned US$41.40 in the nine months
// since: 122 times against about 21, both arithmetically correct and only one
// describing the company.
func TestMultiplesUseTheLastTwelveMonths(t *testing.T) {
	end := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		Currency: "USD",
		Price:    &model.Quote{Price: 927.60},
		Years: []Year{{
			Label:   "FY to Aug 2025",
			Figures: map[string]Value{"epsDiluted": known(7.59)},
		}},
		YTD: &Year{
			Label: "Nine months to 28 May 2026", End: end,
			Figures: map[string]Value{"epsDiluted": known(41.40)},
		},
		PriorYTD: &Year{
			Label:   "Nine months to 29 May 2025",
			Figures: map[string]Value{"epsDiluted": known(4.75)},
		},
		Balance: Balance{Figures: map[string]Value{"sharesOutstanding": known(1.13e9)}},
	}

	table := snap.Table()
	if !strings.Contains(table, "the twelve months to 28 May 2026") {
		t.Errorf("multiples are not on a trailing basis:\n%s", table)
	}
	// 927.60 / (7.59 - 4.75 + 41.40) = 20.97
	if !strings.Contains(table, "20.97x") {
		t.Errorf("price to earnings was not computed on the trailing figures:\n%s", table)
	}
	if strings.Contains(table, "122") {
		t.Errorf("the stale annual multiple survived:\n%s", table)
	}
}

// Return on equity is profit over the equity that earned it, so the profit has
// to be the twelve months to the balance sheet, not a year that closed three
// quarters before it.
func TestReturnOnEquityUsesTheLastTwelveMonths(t *testing.T) {
	end := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		Currency: "USD",
		Years: []Year{{
			Label:   "FY to 28 Aug 2025",
			Figures: map[string]Value{"netIncome": known(8.54e9)},
		}},
		YTD: &Year{
			Label: "Nine months to 28 May 2026", End: end,
			Figures: map[string]Value{"netIncome": known(47.27e9)},
		},
		PriorYTD: &Year{
			Label:   "Nine months to 28 May 2025",
			Figures: map[string]Value{"netIncome": known(5.34e9)},
		},
		Balance: Balance{AsOf: end, Figures: map[string]Value{"equity": known(100.72e9)}},
	}

	table := snap.Table()
	// (8.54 - 5.34 + 47.27) / 100.72 = 50.1%
	if !strings.Contains(table, "50.1%  (profit for the twelve months to 28 May 2026") {
		t.Errorf("return on equity is not on the last twelve months:\n%s", table)
	}
	if strings.Contains(table, "8.5%") {
		t.Errorf("the stale full-year return survived:\n%s", table)
	}

	snap.YTD, snap.PriorYTD = nil, nil
	if table := snap.Table(); !strings.Contains(table, "(profit for the year to 28 Aug 2025") {
		t.Errorf("without an interim the full year should be used and named:\n%s", table)
	}
}

// The heading row says which figures belong to which period, so no label may
// run into the next one however long it is.
func TestColumnHeadingsStayApart(t *testing.T) {
	period := func(label string) *Year {
		return &Year{Label: label, Figures: map[string]Value{"revenue": known(1e9)}}
	}
	snap := Snapshot{
		Currency: "USD",
		Years:    []Year{*period("FY to 28 Aug 2025")},
		YTD:      period("Three months to 30 Sep 2026"),
		PriorYTD: period("Nine months to 28 May 2025"),
	}

	table := snap.Table()
	for _, joined := range []string{"2026Nine", "2025FY"} {
		if strings.Contains(table, joined) {
			t.Errorf("headings ran together at %q:\n%s", joined, table)
		}
	}
}

// The share count on a filing's cover page is counted weeks after the quarter
// closes. It is a real figure, but it is not the balance sheet's date.
func TestBalanceDateIsNotTheCoverPageDate(t *testing.T) {
	quarter := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	cover := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	byKey := map[string][]Observation{
		"equity":            {{End: quarter, Value: 100.72e9, Unit: "USD", Form: "10-Q"}},
		"sharesOutstanding": {{End: cover, Value: 1.13e9, Unit: "shares", Form: "10-Q"}},
	}

	got := buildBalance(byKey, "USD")
	if !got.AsOf.Equal(quarter) {
		t.Errorf("balance sheet dated %s, want the quarter end %s", got.AsOf.Format(time.DateOnly), quarter.Format(time.DateOnly))
	}
	if !got.Figure("sharesOutstanding").Known {
		t.Error("the share count was dropped rather than kept undated")
	}

	// A filer with nothing but the cover page still needs some date.
	only := buildBalance(map[string][]Observation{"sharesOutstanding": byKey["sharesOutstanding"]}, "USD")
	if !only.AsOf.Equal(cover) {
		t.Errorf("with only a share count, dated %s, want %s", only.AsOf.Format(time.DateOnly), cover.Format(time.DateOnly))
	}
}

// Alibaba's half-year reports carry no figures the SEC can read, so the newest
// interim period it held was from 2020. Shown as "the year so far" beside the
// 2026 accounts, it was worse than nothing.
func TestYearSoFarIgnoresPeriodsBeforeTheLastAnnualReport(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.DateOnly, s)
		return d
	}
	half := func(start, end string, v float64) Observation {
		return Observation{Start: day(start), End: day(end), Value: v, Unit: "CNY", Form: "6-K"}
	}
	byKey := map[string][]Observation{
		"revenue": {
			half("2020-04-01", "2020-09-30", 308_812),
			half("2019-04-01", "2019-09-30", 233_939),
		},
	}

	if current, prior := buildYTD(byKey, "CNY", day("2026-03-31")); current != nil || prior != nil {
		t.Errorf("year so far = %+v / %+v, want none older than the annual report", current, prior)
	}

	// The same figures after a 2020 annual report are the year so far.
	if current, _ := buildYTD(byKey, "CNY", day("2020-03-31")); current == nil || !current.End.Equal(day("2020-09-30")) {
		t.Errorf("year so far = %+v, want the half year to 30 Sep 2020", current)
	}
}

// Without an interim filing there is nothing to roll forward, and the full year
// is the honest basis -- said plainly rather than passed off as trailing.
func TestMultiplesFallBackToTheFullYear(t *testing.T) {
	snap := Snapshot{
		Currency: "USD",
		Price:    &model.Quote{Price: 100},
		Years:    []Year{{Label: "FY to Dec 2025", Figures: map[string]Value{"epsDiluted": known(5)}}},
		Balance:  Balance{Figures: map[string]Value{"sharesOutstanding": known(1e9)}},
	}

	table := snap.Table()
	if !strings.Contains(table, "FY to Dec 2025 earnings") {
		t.Errorf("the basis is not named:\n%s", table)
	}
	if !strings.Contains(table, "20.00x") {
		t.Errorf("price to earnings on the full year is wrong:\n%s", table)
	}
}
