package report

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The prompt carries what is due, labelled as calendar figures rather than
// articles, so the overview can say what to watch with the numbers as given.
func TestPromptCarriesTheCalendar(t *testing.T) {
	ny := time.FixedZone("EDT", -4*3600)
	cal := model.Calendar{
		Events:   []model.Release{{At: time.Date(2026, 10, 1, 8, 30, 0, 0, ny), Country: "USD", Title: "Core PCE Price Index m/m", Impact: "High", Forecast: "0.3%", Previous: "0.2%"}},
		Earnings: []model.Results{{Day: time.Date(2026, 10, 1, 0, 0, 0, 0, ny), Symbol: "MU", Name: "Micron Technology", When: "after the close", Forecast: "$4.11", Estimates: 25, Followed: true}},
	}
	got := renderCalendar(cal, time.FixedZone("SGT", 8*3600))
	for _, want := range []string{
		"not from the articles",
		"Thu 1 Oct, 08:30 New York (20:30 Singapore): USD Core PCE Price Index m/m, high impact; forecast 0.3%; previous 0.2%",
		"Thu 1 Oct, after the close: Micron Technology (MU), consensus $4.11 a share, 25 analysts -- on the reader's watchlist",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if renderCalendar(model.Calendar{}, time.UTC) != "" {
		t.Error("an empty calendar wrote a block")
	}
}
