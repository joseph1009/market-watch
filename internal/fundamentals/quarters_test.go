package fundamentals

import (
	"strings"
	"testing"
	"time"
)

func span(from, to string, v float64, unit string) Observation {
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	return Observation{Start: start, End: end, Value: v, Unit: unit}
}

// Income is filed quarter by quarter, cash flow as the year so far, and the
// fourth quarter not at all: each quarter is read for its three months
// alone, and the last four make the twelve months.
func TestQuartersAreReadOnTheirOwn(t *testing.T) {
	byKey := map[string][]Observation{
		"revenue": {
			span("2026-04-01", "2026-06-30", 120, "USD"),
			span("2026-01-01", "2026-03-31", 110, "USD"),
			span("2025-01-01", "2025-12-31", 400, "USD"),
			span("2025-01-01", "2025-09-30", 295, "USD"),
			span("2025-07-01", "2025-09-30", 105, "USD"),
			span("2025-04-01", "2025-06-30", 100, "USD"),
			span("2025-01-01", "2025-03-31", 90, "USD"),
		},
		"operatingCashFlow": {
			span("2026-01-01", "2026-06-30", 70, "USD"),
			span("2026-01-01", "2026-03-31", 30, "USD"),
			span("2025-01-01", "2025-12-31", 110, "USD"),
			span("2025-01-01", "2025-09-30", 75, "USD"),
			span("2025-01-01", "2025-06-30", 45, "USD"),
			span("2025-01-01", "2025-03-31", 20, "USD"),
		},
		"epsDiluted": {
			span("2026-04-01", "2026-06-30", 1.2, "USD/shares"),
			span("2025-01-01", "2025-12-31", 4.0, "USD/shares"),
			span("2025-01-01", "2025-09-30", 2.9, "USD/shares"),
		},
	}
	quarters, ttm := buildQuarters(byKey, "USD")
	if len(quarters) != 5 {
		t.Fatalf("got %d quarters, want 5: %+v", len(quarters), quarters)
	}
	want := []struct {
		end       string
		rev, cash float64
	}{
		{"2026-06-30", 120, 40}, {"2026-03-31", 110, 30}, {"2025-12-31", 105, 35}, {"2025-09-30", 105, 30}, {"2025-06-30", 100, 25},
	}
	for i, w := range want {
		q := quarters[i]
		if q.End.Format(time.DateOnly) != w.end || q.Figure("revenue").Amount != w.rev || q.Figure("operatingCashFlow").Amount != w.cash {
			t.Errorf("quarter %d = %s revenue %v cash %v, want %s %v %v", i, q.End.Format(time.DateOnly),
				q.Figure("revenue"), q.Figure("operatingCashFlow"), w.end, w.rev, w.cash)
		}
	}
	if quarters[0].Figure("epsDiluted").Amount != 1.2 || quarters[2].Figure("epsDiluted").Known {
		t.Errorf("EPS: latest %v, fourth quarter %v (should not be derived)", quarters[0].Figure("epsDiluted"), quarters[2].Figure("epsDiluted"))
	}
	if ttm == nil || ttm.Figure("revenue").Amount != 440 || ttm.Figure("operatingCashFlow").Amount != 135 || ttm.Figure("epsDiluted").Known {
		t.Errorf("twelve months = %+v", ttm)
	}

	table := Snapshot{Currency: "USD", Quarters: quarters, TTM: ttm}.quarterTable()
	for _, w := range []string{"3 months to 30 Jun 2026", "12 months to 30 Jun 2026", "Revenue vs a year earlier", "20.0%"} {
		if !strings.Contains(table, w) {
			t.Errorf("table is missing %q:\n%s", w, table)
		}
	}
}

// A filer with no interim figures has no quarters, and the table says
// nothing about them.
func TestNoInterimFiguresNoQuarters(t *testing.T) {
	byKey := map[string][]Observation{"revenue": {span("2025-01-01", "2025-12-31", 400, "USD")}}
	quarters, ttm := buildQuarters(byKey, "USD")
	if len(quarters) != 0 || ttm != nil || (Snapshot{Quarters: quarters}).quarterTable() != "" {
		t.Errorf("quarters = %+v, ttm %+v", quarters, ttm)
	}
}
