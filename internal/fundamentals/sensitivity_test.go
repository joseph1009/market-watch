package fundamentals

import (
	"math"
	"strings"
	"testing"
	"time"
)

// figures builds a period's lines from millions.
func figures(m map[string]float64) map[string]Value {
	out := map[string]Value{}
	for k, v := range m {
		out[k] = known(v * million)
	}
	return out
}

// sensitive is a company with a year and two half years whose twelve months to
// June come to revenue 1,200m, gross profit 720m, operating income 270m, and
// tax of 54m on 270m before it: 20%.
func sensitive() Snapshot {
	return Snapshot{
		Ticker: "TEST", Currency: "USD",
		Years: []Year{{
			Label: "FY to 31 Dec 2025", End: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			Figures: figures(map[string]float64{
				"revenue": 1000, "grossProfit": 600, "operatingIncome": 200,
				"pretaxIncome": 200, "incomeTax": 40, "dilutedShares": 100,
			}),
		}},
		PriorYTD: &Year{
			Label: "Half year to 30 Jun 2025",
			Figures: figures(map[string]float64{
				"revenue": 400, "grossProfit": 240, "operatingIncome": 80, "pretaxIncome": 80, "incomeTax": 16,
			}),
		},
		YTD: &Year{
			Label: "Half year to 30 Jun 2026", End: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
			Figures: figures(map[string]float64{
				"revenue": 600, "grossProfit": 360, "operatingIncome": 150,
				"pretaxIncome": 150, "incomeTax": 30, "dilutedShares": 110,
			}),
		},
		ExpectedEPS: 1.50, ExpectedFor: "Dec 2026",
	}
}

func near(v Value, want float64) bool { return v.Known && math.Abs(v.Amount-want) < 1e-6 }

func TestSensitivityWorksFromTheLastTwelveMonths(t *testing.T) {
	sens, ok := sensitive().Sensitivity()
	if !ok {
		t.Fatal("no sensitivity")
	}
	if sens.Basis != "Jul 2025–Jun 2026" {
		t.Errorf("basis %q", sens.Basis)
	}
	if !near(sens.GrossMargin, 0.6) || !near(sens.OperatingMargin, 0.225) || !near(sens.Leverage, 720.0/270) {
		t.Errorf("margins %v %v, leverage %v", sens.GrossMargin, sens.OperatingMargin, sens.Leverage)
	}
	if !sens.TaxFiled || math.Abs(sens.TaxRate-0.2) > 1e-9 {
		t.Errorf("tax %v, filed %v", sens.TaxRate, sens.TaxFiled)
	}
	// Per 110m diluted shares, after 20% tax: 1% of revenue is 12m, of gross
	// profit 7.2m, of operating income 2.7m.
	if !near(sens.Shares, 110e6) || !near(sens.Margin, 9.6/110) || !near(sens.Volume, 5.76/110) || !near(sens.VolumeScaled, 2.16/110) {
		t.Errorf("shares %v, margin %v, volume %v, scaled %v", sens.Shares, sens.Margin, sens.Volume, sens.VolumeScaled)
	}

	facts := sensitive().SensitivityFacts()
	for _, want := range []string{
		"Gross margin 60.0%, operating margin 22.5%.",
		"Operating leverage 2.67x",
		"moves operating income about 2.7%",
		"100 basis points of gross margin: ±US$0.09 a diluted share after tax, 5.8% of the US$1.50 analysts expect for 2026",
		"1% of revenue: ±US$0.05 a share with operating costs fixed, ±US$0.02 if every cost moves with revenue.",
		"effective rate over the same period, 20.0%",
		"110.0m shares, the weighted average diluted count for Half year to 30 Jun 2026",
	} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %q:\n%s", want, facts)
		}
	}
}

// A line the interims cannot roll forward sends every figure back to the full
// year, rather than a margin made of two periods.
func TestSensitivityFallsBackToTheYearAsAWhole(t *testing.T) {
	snap := sensitive()
	delete(snap.YTD.Figures, "operatingIncome")
	sens, _ := snap.Sensitivity()
	if sens.Basis != "FY to 31 Dec 2025" || !near(sens.OperatingMargin, 0.2) || !near(sens.GrossMargin, 0.6) {
		t.Errorf("basis %q, margins %v %v", sens.Basis, sens.GrossMargin, sens.OperatingMargin)
	}
}

// A loss has no leverage worth the name, and its tax rate says nothing about
// what a profit would be taxed at.
func TestALossIsNotLeveraged(t *testing.T) {
	snap := sensitive()
	snap.YTD, snap.PriorYTD = nil, nil
	snap.Years[0].Figures["operatingIncome"] = known(-50 * million)
	snap.Years[0].Figures["pretaxIncome"] = known(-60 * million)
	snap.Years[0].Figures["incomeTax"] = known(5 * million)

	sens, _ := snap.Sensitivity()
	if sens.Leverage.Known || sens.VolumeScaled.Known || sens.TaxFiled || sens.TaxRate != statutoryTax {
		t.Errorf("leverage %v, scaled %v, tax %v (filed %v)", sens.Leverage, sens.VolumeScaled, sens.TaxRate, sens.TaxFiled)
	}
	facts := snap.SensitivityFacts()
	for _, want := range []string{"not meaningful: there was no operating profit", "US federal rate of 21%", "with operating costs fixed."} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %q:\n%s", want, facts)
		}
	}
}

func TestNoGrossProfitNoLeverage(t *testing.T) {
	snap := sensitive()
	for _, y := range []*Year{&snap.Years[0], snap.YTD, snap.PriorYTD} {
		delete(y.Figures, "grossProfit")
	}
	facts := snap.SensitivityFacts()
	for _, want := range []string{
		"- Operating margin 22.5%.\n",
		"files no gross profit",
		"100 basis points of operating margin: ±US$0.09",
		"±US$0.02 a share if every cost moves with revenue; more where costs are fixed",
	} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %q:\n%s", want, facts)
		}
	}

	// A bank files neither margin, and gets no block at all.
	for _, y := range []*Year{&snap.Years[0], snap.YTD, snap.PriorYTD} {
		delete(y.Figures, "operatingIncome")
	}
	if facts := snap.SensitivityFacts(); facts != "" {
		t.Errorf("a company with no margins got:\n%s", facts)
	}
}

// Margins are ratios and travel across currencies; a figure per share does not
// when the listing may stand for several ordinary shares.
func TestNoPerShareFiguresAcrossCurrencies(t *testing.T) {
	snap := sensitive()
	snap.Currency = "TWD"
	facts := snap.SensitivityFacts()
	if !strings.Contains(facts, "Gross margin 60.0%") || !strings.Contains(facts, "accounts are in TWD") || strings.Contains(facts, "US$") {
		t.Errorf("facts:\n%s", facts)
	}
}
