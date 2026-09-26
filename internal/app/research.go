package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joseph1009/market-watch/internal/consensus"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/search"
)

// What the analysis and the closer look read beside the accounts: what
// analysts expect, the company's own account of its latest results, the
// market backdrop, and, for /analyse, what has been written about it found by
// searching. Every one of them is best-effort. The accounts are the part that
// cannot be had anywhere else, and none of these failing may cost them.

const (
	// releaseSince is how far back a results release is still the latest
	// word: a quarter, and a few weeks for a late filer.
	releaseSince = 120 * 24 * time.Hour

	// analysisReleaseRunes is how much of the release /analyse reads: the
	// headline figures, the quarter's table and, at most companies, the
	// outlook. The closer look reads less (ideaReleaseRunes), being twenty
	// companies at a time.
	analysisReleaseRunes = 7000

	// companySearchWindow is how far back /analyse searches for news: a
	// month, which covers a results season and what came after it.
	companySearchWindow = 30 * 24 * time.Hour

	// expectationsBudget bounds the six Nasdaq reads for one company, which
	// take one to three seconds each.
	expectationsBudget = 45 * time.Second
)

// addExpectations attaches the analysts' consensus, and the insider, short and
// fund figures, to a US listing.
func (a *App) addExpectations(ctx context.Context, snap *fundamentals.Snapshot) {
	if a.Consensus == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, expectationsBudget)
	defer cancel()

	r, err := a.Consensus.Fetch(ctx, snap.Ticker)
	if err != nil {
		if !errors.Is(err, consensus.ErrNotCovered) {
			a.Log.Warn("expectations", "ticker", snap.Ticker, "error", err)
		}
		return
	}
	price := 0.0
	if snap.Price != nil {
		price = snap.Price.Price
	}
	snap.Expectations = r.Facts(price)
	if len(r.Years) > 0 {
		snap.ExpectedEPS, snap.ExpectedFor = r.Years[0].EPS, r.Years[0].Period
	}
	a.Log.Info("expectations", "ticker", snap.Ticker, "missing", len(r.Missing))
}

// addRelease attaches the company's latest results release.
func (a *App) addRelease(ctx context.Context, snap *fundamentals.Snapshot, runes int) {
	if a.Filings == nil {
		return
	}
	filing, text, err := a.Filings.EarningsRelease(ctx, snap.Ticker, a.now().Add(-releaseSince), runes)
	if err != nil {
		a.Log.Info("no results release", "ticker", snap.Ticker, "error", err)
		return
	}
	snap.Release = text
	snap.ReleaseFrom = "filed " + filing.Filed.Format("2 January 2006")
}

// backdrop reads the commodities, the dollar and the cost of money, once for
// however many companies it is set beside.
func (a *App) backdrop(ctx context.Context) []string {
	if !a.Levels.Enabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	readings, errs := a.Levels.Fetch(ctx, prices.Backdrop)
	for _, err := range errs {
		a.Log.Warn("backdrop", "error", err)
	}
	lines := make([]string, len(readings))
	for i, r := range readings {
		lines[i] = r.Line()
	}
	return lines
}

// searchCompany finds what has been written about one company in the last
// month: two searches, two credits, one for the news and one for its results
// and what it expects.
func (a *App) searchCompany(ctx context.Context, snap *fundamentals.Snapshot) []model.Article {
	if !a.Search.Enabled() {
		return nil
	}
	// The watchlist's name where it follows the company, since that is how
	// headlines write it; otherwise the SEC's, shorn of its "INC" and "CORP".
	name := companyNames(a.Prefs().Groups)[snap.Ticker]
	if name == "" {
		name = search.PlainName(snap.Company)
	}
	if name == "" {
		name = snap.Ticker
	}
	queries := []search.Query{
		{Label: "company:" + snap.Ticker, Text: fmt.Sprintf("%s (%s) news", name, snap.Ticker)},
		{Label: "results:" + snap.Ticker, Text: fmt.Sprintf("%s results, guidance and outlook", name)},
	}
	ctx, cancel := context.WithTimeout(ctx, newsBudget)
	defer cancel()

	found := a.Search.Collect(ctx, queries, a.now().Add(-companySearchWindow))
	for _, err := range found.Errors {
		a.Log.Warn("company search", "ticker", snap.Ticker, "error", err)
	}
	a.Log.Info("searched company", "ticker", snap.Ticker, "articles", len(found.Articles), "credits", found.Credits)
	return found.Articles
}
