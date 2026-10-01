package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The look ahead is laid out a day at a time, soonest first, times in
// Singapore with New York's beside them, each release with its forecast and
// previous figure, and the results with what is expected.
func TestComingUpIsLaidOutByDay(t *testing.T) {
	ny := time.FixedZone("EDT", -4*3600)
	sg := time.FixedZone("SGT", 8*3600)
	now := time.Date(2026, 10, 1, 7, 30, 0, 0, ny)
	cal := model.Calendar{
		Events: []model.Release{
			{At: time.Date(2026, 10, 1, 8, 30, 0, 0, ny), Country: "USD", Title: "Core PCE Price Index m/m", Impact: "High", Forecast: "0.3%", Previous: "0.2%"},
			{At: time.Date(2026, 10, 5, 10, 0, 0, 0, ny), Country: "USD", Title: "ISM Services PMI", Impact: "High", Forecast: "52.1", Previous: "51.8"},
		},
		Earnings: []model.Results{
			{Day: time.Date(2026, 10, 1, 0, 0, 0, 0, ny), Symbol: "NKE", Name: "Nike, Inc.", When: "after the close", Forecast: "$0.43", LastYear: "$0.49", Followed: true},
			// A day with results and no releases, between two that have
			// releases: it is first seen last, and must still read in order.
			{Day: time.Date(2026, 10, 2, 0, 0, 0, 0, ny), Symbol: "PEP", Name: "PepsiCo, Inc.", When: "before the open", Forecast: "$2.30"},
		},
	}
	rep := model.Report{GeneratedAt: now, Overview: "### Fed\n- A point [1].", Calendar: cal}
	text := strings.Join(RenderWith(rep, Options{Display: sg}), "\n")

	for _, want := range []string{
		"📅 COMING UP",
		"Today, Thursday 1 October",
		"🔴 🇺🇸 <b>20:30</b> (08:30 New York) Core PCE Price Index m/m — forecast <b>0.3%</b> · previous 0.2%",
		"Friday 2 October",
		"⭐ <b>NKE</b> Nike, after the close — expected <b>$0.43</b> a share ($0.49 a year ago)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	thu, fri, mon := strings.Index(text, "Today, Thursday 1 October"), strings.Index(text, "Friday 2 October"), strings.Index(text, "Monday 5 October")
	if !(thu < fri && fri < mon) {
		t.Errorf("the days are out of order:\n%s", text)
	}
	if strings.Index(text, "COMING UP") < strings.Index(text, "OVERVIEW") {
		t.Error("the look ahead comes before the overview")
	}
}
