package app

import (
	"context"
	"time"

	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// quoteBudget bounds how long prices may take. The free tier is paced at one
// symbol a second, so a long list is minutes: prices improve the brief and must
// never be what delays it.
const quoteBudget = 90 * time.Second

// collectQuotes reads what the market did, for the benchmarks and for the
// companies today's news is actually about.
//
// Not all 93 watchlist tickers: the rate limit is the scarce resource here, and
// a company with no news in the brief has no place to put its price. The
// benchmarks come first so a truncated run still says what the market did.
func (a *App) collectQuotes(ctx context.Context, articles []model.Article, watched []string) []model.Quote {
	if !a.Quotes.Enabled() {
		return nil
	}

	symbols := append(prices.BenchmarkSymbols(), prices.Mentioned(articles, watched)...)

	ctx, cancel := context.WithTimeout(ctx, quoteBudget)
	defer cancel()

	started := time.Now()
	quotes, missed := a.Quotes.Fetch(ctx, symbols)
	a.Log.Info("prices",
		"asked", len(symbols),
		"read", len(quotes),
		"missing", len(missed),
		"took", time.Since(started).Round(time.Second))
	return quotes
}

// historyBudget bounds the daily-history read. One request, so a short budget:
// the analysis is already a minute of the reader's time and a hung fetch must
// not add to it.
const historyBudget = 25 * time.Second

// tradingFor reads what the share has actually been doing, or nothing.
//
// Failure is ordinary here and costs one section. The source is an undocumented
// endpoint that can refuse without notice, and a symbol that files with the SEC
// may not be quoted under the same letters -- neither is a reason to fail an
// analysis whose substance is the filings.
func (a *App) tradingFor(ctx context.Context, ticker string) *model.Trading {
	if a.Market == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, historyBudget)
	defer cancel()

	series, err := a.Market.Fetch(ctx, ticker)
	if err != nil {
		a.Log.Warn("price history", "ticker", ticker, "error", err)
		return nil
	}

	trading := prices.Summarise(series, a.now())
	if trading.Days == 0 {
		return nil
	}
	if trading.Stale(a.now()) {
		// A history that stops weeks ago describes a market that has moved on,
		// and nothing in the figures themselves would say so.
		a.Log.Warn("price history is stale", "ticker", ticker, "as_of", trading.AsOf)
		return nil
	}
	a.Log.Info("price history",
		"ticker", ticker,
		"sessions", trading.Days,
		"as_of", trading.AsOf.Format(time.DateOnly))
	return &trading
}

// newsBudget bounds the company-news read, on the same reasoning.
const newsBudget = 25 * time.Second

// addNews attaches what has been written about the company lately.
func (a *App) addNews(ctx context.Context, snapshot *fundamentals.Snapshot) error {
	if !a.Press.Enabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, newsBudget)
	defer cancel()

	if err := fundamentals.AddNews(ctx, a.Press, snapshot, a.now()); err != nil {
		return err
	}
	a.Log.Info("company news", "ticker", snapshot.Ticker, "kept", len(snapshot.News))
	return nil
}

// priceCandidates attaches each new name's move, where it has a US listing.
// A company quoted elsewhere keeps its name and loses its price, which the
// section says plainly rather than leaving a blank.
func (a *App) priceCandidates(ctx context.Context, candidates []model.Candidate) []model.Candidate {
	if !a.Quotes.Enabled() || len(candidates) == 0 {
		return candidates
	}

	var symbols []string
	for _, c := range candidates {
		if c.Ticker != "" && (c.Exchange == "US" || c.Exchange == "") {
			symbols = append(symbols, c.Ticker)
		}
	}
	if len(symbols) == 0 {
		return candidates
	}

	ctx, cancel := context.WithTimeout(ctx, quoteBudget)
	defer cancel()

	quotes, _ := a.Quotes.Fetch(ctx, symbols)
	bySymbol := prices.Index(quotes)

	out := make([]model.Candidate, len(candidates))
	copy(out, candidates)
	for i, c := range out {
		if q, ok := bySymbol[c.Ticker]; ok {
			out[i].Quote = &q
		}
	}
	return out
}
