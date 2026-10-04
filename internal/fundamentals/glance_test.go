package fundamentals

import (
	"math"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/model"
)

// The box's figures, on Micron's filings and release: the multiples on the
// year to 3 September 2026, and its debt at its own, older date.
func TestTheGlanceGivesTheFiguresAReaderLooksForFirst(t *testing.T) {
	s := filedMicron()
	if _, err := s.AddRelease(micronRelease(), releaseFiled); err != nil {
		t.Fatal(err)
	}
	s.obs["sharesOutstanding"] = []Observation{{End: day("2026-06-17"), Value: 1.13e9, Unit: "shares", Form: "10-Q"}}
	s.build()
	s.Price = &model.Quote{Symbol: "MU", Price: 1074.89}
	s.ExpectedEPS, s.ExpectedFor = 160.39, "Aug 2027"

	g := s.Glance()
	near := func(v Value, want float64) bool { return v.Known && math.Abs(v.Amount-want) < 0.05*want }
	if !near(g.MarketCap, 1074.89*1.13e9) || !near(g.PS, 1074.89*1.13e9/133188e6) || !near(g.ForwardPE, 6.7) {
		t.Errorf("market value %v, price to sales %v, forward P/E %v", g.MarketCap, g.PS, g.ForwardPE)
	}
	if g.On != "the year to 3 Sep 2026" || g.ForwardFor != "Aug 2027" {
		t.Errorf("on %q, forward for %q", g.On, g.ForwardFor)
	}
	if g.DebtAt != day("2026-05-28") || g.CashAt != day("2026-09-03") {
		t.Errorf("cash at %v, debt at %v", g.CashAt, g.DebtAt)
	}
	if !strings.Contains(s.Table(), "Price to sales") {
		t.Error("the model is not given the price to sales")
	}
}
