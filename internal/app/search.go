package app

import (
	"context"
	"errors"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/search"
)

// searchFailure is the id a failed round of searches is recorded under in the
// run record, beside the feeds that failed. One id rather than one per search:
// a spent allowance fails all fifteen at once, and fifteen lines in /stats
// would say the same thing fifteen times.
const searchFailure = "tavily-search"

// collectSearch runs the news searches for the watchlists.
//
// Like the filings, it is an addition and fails quietly: the feeds carry the
// brief on their own, as they did before search existed, and a search outage
// costs its own stories and nothing else.
func (a *App) collectSearch(ctx context.Context, prefs config.Prefs) search.Result {
	if !a.Search.Enabled() {
		return search.Result{}
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	started := time.Now()
	queries := search.Queries(prefs.Groups)
	found := a.Search.Collect(ctx, queries, a.searchSince())
	for _, err := range found.Errors {
		if errors.Is(err, search.ErrOutOfCredits) {
			a.Log.Warn("news search: out of credits until the allowance renews", "error", err)
			continue
		}
		a.Log.Warn("news search", "error", err)
	}
	a.Log.Info("searched",
		"queries", len(queries),
		"articles", len(found.Articles),
		"failed", len(found.Errors),
		"credits", found.Credits,
		"took", time.Since(started).Round(time.Second))
	return found
}

// searchSince is where the searches start: the previous brief, so Monday's
// reaches back over the weekend, and never less than a day back, so a rerun
// with /now an hour after the brief searches the day and not the hour.
func (a *App) searchSince() time.Time {
	now := a.now()
	since := now.Add(-24 * time.Hour)
	if a.Runs != nil {
		if runs := a.Runs.All(); len(runs) > 0 && runs[0].At.Before(since) {
			since = runs[0].At
		}
	}
	// No further back than any article may be. A brief missed for a fortnight
	// is followed by one about this week, not the fortnight.
	if floor := now.Add(-feed.MaxArticleAge); since.Before(floor) {
		since = floor
	}
	return since
}

// recordSearch writes down what search contributed to one brief, set against
// what the feeds did, so that whether it can replace the media feeds is
// decided on a fortnight of numbers and not on one morning's impression.
//
// kept is what survived the cut, and cited what the brief actually cited. A
// story counts as search's alone when every copy of it came from a search, and
// as missed by search when no copy did; the second is recorded by the source
// that carried it, so the record says which feeds would be missed if they went.
func recordSearch(run *history.Run, found search.Result, kept, cited []model.Article) {
	run.Searched = len(found.Articles)
	run.SearchCredits = found.Credits
	if len(found.Errors) > 0 {
		run.Failed = append(run.Failed, searchFailure)
	}
	// With nothing found there is no comparison to make, and recording every
	// citation as missed by search would read as a verdict on it.
	if len(found.Articles) == 0 {
		return
	}

	for _, a := range kept {
		if onlySearch(a) {
			run.SearchOnly++
		}
	}

	run.Cited = len(cited)
	for _, a := range cited {
		switch {
		case onlySearch(a):
			run.CitedSearchOnly++
		case !anySearch(a):
			if run.MissedBySearch == nil {
				run.MissedBySearch = map[string]int{}
			}
			run.MissedBySearch[a.SourceID]++
		}
	}
}

func onlySearch(a model.Article) bool {
	for _, s := range a.Carriers() {
		if !search.IsSearch(s) {
			return false
		}
	}
	return true
}

func anySearch(a model.Article) bool {
	for _, s := range a.Carriers() {
		if search.IsSearch(s) {
			return true
		}
	}
	return false
}
