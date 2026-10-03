package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/consensus"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/market"
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

	// analysisReleaseRunes is how much of the release /analyse and the
	// closer look's verdicts read: the headline figures, the quarter's table
	// and, at most companies, the outlook.
	analysisReleaseRunes = 7000

	// companySearchWindow is how far back /analyse searches for news: a
	// month, which covers a results season and what came after it.
	companySearchWindow = 30 * 24 * time.Hour

	// expectationsBudget bounds the six Nasdaq reads for one company, which
	// take one to three seconds each.
	expectationsBudget = 45 * time.Second

	// peersBudget bounds reading the SEC's figures for an industry group:
	// about sixteen requests the first time in a week, none after.
	peersBudget = time.Minute
)

// addPeers sets a US listing beside the others in its industry group of
// Nasdaq's list, on the SEC's figures for the last calendar year.
func (a *App) addPeers(ctx context.Context, snap *fundamentals.Snapshot) {
	if a.Accounts == nil || a.Filings == nil {
		return
	}
	listings, err := a.listings(ctx)
	if err != nil {
		a.Log.Info("no peers: Nasdaq's listings could not be read", "ticker", snap.Ticker, "error", err)
		return
	}
	industry := ""
	for _, l := range listings {
		if strings.EqualFold(l.Symbol, snap.Ticker) {
			industry = l.Industry
			break
		}
	}
	if industry == "" {
		return
	}
	var peers []fundamentals.Peer
	seen := map[int]bool{} // a company listed in two share classes counts once
	for _, l := range listings {
		if l.Industry != industry || l.NotListedInAmerica {
			continue
		}
		if l.MarketCap < fundamentals.PeerMarketCap && !strings.EqualFold(l.Symbol, snap.Ticker) {
			continue
		}
		cik, _, err := a.Filings.LookupCIK(ctx, l.Symbol)
		if err != nil || seen[cik] {
			continue
		}
		seen[cik] = true
		peers = append(peers, fundamentals.Peer{Symbol: l.Symbol, Name: market.PlainName(l.Name), CIK: cik, MarketCap: l.MarketCap})
	}

	ctx, cancel := context.WithTimeout(ctx, peersBudget)
	defer cancel()
	group, err := a.Accounts.Peers(ctx, snap.CIK, industry, peers, filepath.Join(a.Cfg.DataDir, "frames"), a.now())
	if err != nil {
		a.Log.Warn("peers", "ticker", snap.Ticker, "error", err)
		return
	}
	snap.Peers = group
	if group != nil {
		a.Log.Info("peers", "ticker", snap.Ticker, "industry", industry, "group", group.Size, "measures", len(group.Lines))
	}
}

// addExpectations attaches the analysts' consensus, and the insider, short and
// fund figures, to a US listing, and returns them, or nil.
func (a *App) addExpectations(ctx context.Context, snap *fundamentals.Snapshot) *consensus.Report {
	if a.Consensus == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, expectationsBudget)
	defer cancel()

	r, err := a.Consensus.Fetch(ctx, snap.Ticker)
	if err != nil {
		if !errors.Is(err, consensus.ErrNotCovered) {
			a.Log.Warn("expectations", "ticker", snap.Ticker, "error", err)
		}
		return nil
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
	return &r
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
