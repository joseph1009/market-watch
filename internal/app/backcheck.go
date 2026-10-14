package app

import (
	"context"
	"strings"

	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
)

// backcheckMax is how many misses a week's back-check looks at: the worst,
// three requests' worth.
const backcheckMax = 9

// backcheck asks why the calls going the wrong way went so. It runs once a
// week, before the week's verdicts, so they are made knowing what it found.
// It prices every call -- from the market's stored history for a US listing,
// from the chart source for one abroad or one too small for the history --
// writes down how each stands, and checks the worst of the misses.
func (a *App) backcheck(ctx context.Context, panel *market.Panel) {
	if a.Backcheck == nil || a.Scorecard == nil {
		return
	}
	now := a.now()
	bench := panelPath(panel, market.Benchmark)
	if len(bench) == 0 {
		bench = a.benchPath(ctx)
	}
	paths := map[string]ideas.Path{}
	for _, chart := range a.Scorecard.Due(now, scorecardSymbols) {
		var p ideas.Path
		if !strings.Contains(chart, ".") {
			p = panelPath(panel, strings.ReplaceAll(chart, "-", "."))
		}
		if len(p) == 0 {
			p = a.pathFor(ctx, chart)
		}
		if len(p) > 0 {
			paths[chart] = p
		}
	}
	rates := a.dollarRates(ctx, a.Scorecard.Currencies(now))
	if err := a.Scorecard.Settle(paths, bench, rates); err != nil {
		a.Log.Warn("back-check: could not save the verdicts' entries", "error", err)
	}
	if err := a.Scorecard.Refresh(now, paths, bench, rates); err != nil {
		a.Log.Warn("back-check: could not save how the verdicts stand", "error", err)
	}

	var misses []ideas.Miss
	for _, r := range a.Scorecard.Misses(now, backcheckMax) {
		if m, ok := ideas.NewMiss(r, now, paths[r.Chart], bench, rates); ok {
			misses = append(misses, m)
		}
	}
	if len(misses) == 0 {
		a.Log.Info("back-check: no misses to look at")
		return
	}
	found, usage, err := a.Backcheck.Check(ctx, misses, now)
	if err != nil {
		a.Log.Warn("back-check: some checks are missing", "error", err)
	}
	saved := 0
	for key, rv := range found {
		ok, err := a.Scorecard.SetReview(key, rv)
		if err != nil {
			a.Log.Warn("back-check: could not save a review", "key", key, "error", err)
			continue
		}
		if ok {
			saved++
		}
	}
	a.Log.Info("back-check done", "misses", len(misses), "reviewed", saved,
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)
}
