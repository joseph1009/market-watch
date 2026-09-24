package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func moveQuotes(asOf time.Time) []model.Quote {
	return []model.Quote{
		{Symbol: "SPY", Price: 500, Percent: -0.7, AsOf: asOf},
		{Symbol: "MCD", Price: 280, Percent: -4.8, AsOf: asOf},
		{Symbol: "NKE", Price: 70, Percent: -2.1, AsOf: asOf},
		{Symbol: "KO", Price: 60, Percent: 0.4, AsOf: asOf},
		{Symbol: "SBUX", Price: 90, Percent: 1.3, AsOf: asOf},
		{Symbol: "TGT", Price: 110, Percent: -1.1, AsOf: asOf},
		{Symbol: "WMT", Price: 95, Percent: 1.0, AsOf: asOf},
	}
}

var consumer = model.Group{ID: "consumer-retail", Name: "Consumer & Retail", Companies: []model.Company{
	{Symbol: "WMT", Name: "Walmart"}, {Symbol: "MCD", Name: "McDonald's"}, {Symbol: "NKE", Name: "Nike"},
	{Symbol: "KO", Name: "Coca-Cola"}, {Symbol: "SBUX", Name: "Starbucks"}, {Symbol: "TGT", Name: "Target"},
}}

// The line shows the watchlist's own shares, biggest first, at least 1% and
// at most four, so it stays one line and never lists an ordinary day's drift.
func TestSectionMovesPicksTheBiggestOfTheWatchlist(t *testing.T) {
	closed := testTime().Add(-4 * time.Hour)
	got := model.Moves(sectionMoves(consumer, moveQuotes(closed), testTime().Add(-24*time.Hour)))
	if want := "MCD -4.8% · NKE -2.1% · SBUX +1.3% · TGT -1.1%"; got != want {
		t.Errorf("moves = %q, want %q", got, want)
	}
}

// The morning after a US holiday, the last prices are the session the previous
// brief reported. They are not today's moves.
func TestSectionMovesLeavesOutASessionAlreadyReported(t *testing.T) {
	previousBrief := testTime().Add(-24 * time.Hour)
	old := previousBrief.Add(-4 * time.Hour)
	if got := sectionMoves(consumer, moveQuotes(old), previousBrief); len(got) != 0 {
		t.Errorf("moves = %v, want none from before the previous brief", model.Moves(got))
	}
}

// The writer is told what the reader will see above each section, so it does
// not list the same moves again, and is given the history of the shares that
// moved furthest.
func TestPromptShowsTheMovesLineAndTheMoversHistory(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}
	closed := testTime().Add(-4 * time.Hour)
	quotes := []model.Quote{{Symbol: "NVDA", Price: 90, Percent: -6.2, AsOf: closed}}
	g := &Generator{
		Completer:  fake,
		Now:        testTime,
		Quotes:     quotes,
		MovesSince: testTime().Add(-24 * time.Hour),
		Trends: map[string]model.Trading{"NVDA": {
			MA50: 100, MA200: 80, High52: 150, HighAt: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
			Low52: 60, LowAt: time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC),
			VolumeLast: 310, VolumeAvg30: 100,
		}},
	}

	rep, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, want := range []string{
		"Shown to the reader above this section: biggest moves NVDA -6.2%",
		"50-day average 100.00 (the price is 10.0% below it)",
		"200-day average 80.00 (the price is 12.5% above it)",
		"52-week high 150.00 on 3 Jul 2026, low 60.00 on 12 Mar 2026",
		"volume 3.1 times its 30-day average",
	} {
		if !strings.Contains(fake.prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if got := model.Moves(rep.Sections[0].Movers); got != "NVDA -6.2%" {
		t.Errorf("section movers = %q, want NVDA's move carried to the renderer", got)
	}
}

// Part of a day's volume set against whole days always looks quiet, so a
// session still running says nothing about volume.
func TestTrendSaysNothingOfVolumeMidSession(t *testing.T) {
	got := trend(model.Quote{Price: 90}, model.Trading{MA50: 100, VolumeLast: 10, VolumeAvg30: 100, Partial: true})
	if strings.Contains(got, "volume") {
		t.Errorf("trend = %q, want no volume comparison for a partial session", got)
	}
}
