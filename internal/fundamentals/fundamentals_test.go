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
