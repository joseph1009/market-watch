package app

import (
	"context"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/marketdata"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/sec"
)

// filingLookback is how far back a filing may be and still count as news. It
// matches the article staleness window, so a Friday 8-K is still in view on
// Monday and nothing older than the rest of the corpus slips in beside it.
const filingLookback = feed.MaxArticleAge

// collectFilings gathers material 8-Ks for every ticker on the watchlists.
//
// Failures are logged and swallowed. The filings are an enrichment: a brief
// without them is a normal brief, while a brief that never arrives because SEC
// was slow is a failure, and SEC is the one source that has already been
// observed to throttle.
func (a *App) collectFilings(ctx context.Context, prefs config.Prefs) []model.Article {
	if a.Filings == nil {
		return nil
	}

	tickers := watchedTickers(prefs.Groups)
	if len(tickers) == 0 {
		return nil
	}

	// A bounded slice of the run's time. Left unbounded, a slow SEC would
	// delay the brief rather than merely be absent from it.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	started := time.Now()
	articles, errs := a.Filings.Collect(ctx, tickers, a.now().Add(-filingLookback))
	for _, err := range errs {
		a.Log.Warn("sec filings", "error", err)
	}
	a.Log.Info("filings",
		"tickers", len(tickers),
		"filings", len(articles),
		"failed", len(errs),
		"took", time.Since(started).Round(time.Second))

	return articles
}

// watchedTickers is every symbol across every watchlist, deduplicated. Names
// are deliberately excluded: the SEC index is keyed by symbol, and a company
// tracked only by name is usually one without a US listing.
func watchedTickers(groups []model.Group) []string {
	seen := make(map[string]bool)
	var out []string
	for _, g := range groups {
		for _, t := range g.Tickers {
			if seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// SECSourceEntry describes the filings source for the scorer, which needs a
// weight for every source id it sees. It is not a feed and has no URL, so it
// never appears in /sources or the fetch loop.
func SECSourceEntry() model.Source {
	return model.Source{
		ID:      sec.SourceID,
		Name:    sec.SourceName,
		Weight:  sec.SourceWeight,
		Enabled: false, // never fetched as a feed
	}
}

// collectLevels reads the market indicators shown alongside the articles.
//
// Absent a FRED key this returns nothing and the block is omitted, which is the
// intended behaviour rather than a degraded one: the levels sharpen the macro
// section, they do not carry it.
func (a *App) collectLevels(ctx context.Context) []marketdata.Reading {
	if !a.Levels.Enabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	readings, errs := a.Levels.Fetch(ctx, marketdata.DefaultSeries)
	for _, err := range errs {
		a.Log.Warn("market data", "error", err)
	}
	if len(readings) > 0 {
		a.Log.Info("levels", "series", len(readings), "failed", len(errs))
	}
	return readings
}
