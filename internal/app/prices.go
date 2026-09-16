package app

import (
	"context"
	"time"

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
