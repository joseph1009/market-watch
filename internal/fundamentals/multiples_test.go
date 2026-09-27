package fundamentals

import (
	"math"
	"testing"
	"time"
)

// Multiples come from the whole company's market value against its last
// twelve months, converted to dollars; its past ones scale today's value by
// the share price at each year's end.
func TestMultiplesFromTheMarketValue(t *testing.T) {
	end := func(y int) time.Time { return time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC) }
	snap := Snapshot{
		Years: []Year{
			{Label: "FY2025", End: end(2025), Figures: map[string]Value{"revenue": known(100), "netIncome": known(10), "operatingIncome": known(15)}},
			{Label: "FY2024", End: end(2024), Figures: map[string]Value{"revenue": known(80), "netIncome": known(-2)}},
		},
		// Six months to June: revenue 60 against 45 a year before, so the
		// twelve months are 100 - 45 + 60 = 115.
		YTD:      &Year{Label: "6M 2026", End: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), Figures: map[string]Value{"revenue": known(60), "netIncome": known(8), "operatingIncome": known(9)}},
		PriorYTD: &Year{Label: "6M 2025", End: time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC), Figures: map[string]Value{"revenue": known(45), "netIncome": known(4), "operatingIncome": known(6)}},
		Balance:  Balance{Figures: map[string]Value{"longTermDebt": known(30), "cash": known(10)}},
	}
	// Accounts in a currency worth half a dollar; the company worth 115 dollars.
	m := snap.Multiples(115, 0.5)
	if math.Abs(m.PS-2) > 1e-9 || math.Abs(m.PE-115/(14*0.5)) > 1e-9 {
		t.Errorf("multiples = %+v", m)
	}
	// Company value 115 + (30 - 10) x 0.5 = 125, over operating profit 18 x 0.5.
	if math.Abs(m.EVEBIT-125/9.0) > 1e-9 {
		t.Errorf("EV/EBIT = %v", m.EVEBIT)
	}
	if !m.HasGrowth || math.Abs(m.Growth-(60.0/45-1)) > 1e-9 {
		t.Errorf("growth = %v, %v", m.Growth, m.HasGrowth)
	}

	// The price was half today's at the end of 2025 and a quarter at the end
	// of 2024; a year with a loss has no price to earnings.
	price := map[int]float64{2025: 5, 2024: 2.5}
	past := snap.PastMultiples(115, 10, func(t time.Time) float64 { return price[t.Year()] }, 0.5)
	if len(past) != 2 || math.Abs(past[0].PS-57.5/50) > 1e-9 || math.Abs(past[0].PE-57.5/5) > 1e-9 || past[1].PE != 0 {
		t.Errorf("past = %+v", past)
	}

	if (Snapshot{}).Multiples(0, 1) != (Multiples{}) {
		t.Error("multiples without a market value")
	}
}
