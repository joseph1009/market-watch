package app

import (
	"context"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// quoteBudget bounds how long the new names' prices may take. The free tier is
// paced at one symbol a second: prices improve the brief and must never be
// what delays it.
const quoteBudget = 90 * time.Second

// scanBudget bounds the price scan. The benchmarks and 95 watchlist shares are
// about two minutes at the free tier's pace; past the budget, what is left
// unpriced is read from the charts instead.
const scanBudget = 150 * time.Second

// chartBudget bounds reading from the charts what the quote feed did not
// price. A chart answers in about a tenth of a second, so a whole watchlist
// takes seconds; the bound is for a chart source that has stopped answering
// too.
const chartBudget = 45 * time.Second

// chartWorkers is how many chart requests are open at once: enough for a
// hundred shares in seconds, few enough not to hammer an endpoint that is
// under no obligation to answer.
const chartWorkers = 4

// collectPrices reads what the market did: the benchmark funds, then every
// watchlist share.
//
// Every share, not only those in the day's news. A share that fell 8% on a day
// no feed wrote about it is the one the reader most needs told about, and it
// can only be seen by pricing it (movers.go). It is the slowest thing a brief
// gathers, so it runs first, beside the filings and the searches.
//
// The quote feed goes first, being keyed and documented. Whatever it does not
// answer for is read from the daily charts, so a feed that stops answering
// costs the brief seconds rather than its prices and movers.
func (a *App) collectPrices(ctx context.Context, watched []string) []model.Quote {
	if !a.Quotes.Enabled() {
		return nil
	}

	symbols := append(prices.BenchmarkSymbols(), watched...)
	started := time.Now()

	scan, cancel := context.WithTimeout(ctx, scanBudget)
	quotes, missed := a.Quotes.Fetch(scan, symbols)
	cancel()
	charted := a.fromCharts(ctx, missed)

	a.Log.Info("prices",
		"asked", len(symbols),
		"read", len(quotes),
		"from_charts", len(charted),
		"missing", len(missed)-len(charted),
		"took", time.Since(started).Round(time.Second))
	return inOrder(symbols, append(quotes, charted...))
}

// fromCharts prices US shares from their daily charts: the last close set
// against the one before, dated when the chart says it was struck. Symbols the
// charts do not answer for are left out.
func (a *App) fromCharts(ctx context.Context, symbols []string) []model.Quote {
	if a.Market == nil || len(symbols) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, chartBudget)
	defer cancel()

	found := make([]*model.Quote, len(symbols))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range chartWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				chart := prices.ChartSymbol(symbols[i], "US")
				if chart == "" {
					continue
				}
				series, err := a.Market.Fetch(ctx, chart)
				if err != nil {
					continue
				}
				if q, ok := prices.Latest(series); ok {
					q.Symbol = symbols[i]
					found[i] = &q
				}
			}
		}()
	}
queue:
	for i := range symbols {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	wg.Wait()

	var out []model.Quote
	for _, q := range found {
		if q != nil {
			out = append(out, *q)
		}
	}
	return out
}

// inOrder puts quotes back in the order they were asked for, benchmarks first,
// whichever source answered.
func inOrder(symbols []string, quotes []model.Quote) []model.Quote {
	bySymbol := prices.Index(quotes)
	out := make([]model.Quote, 0, len(quotes))
	for _, s := range symbols {
		if q, ok := bySymbol[s]; ok {
			out = append(out, q)
			delete(bySymbol, s)
		}
	}
	return out
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
	series, ok := a.seriesFor(ctx, ticker)
	if !ok {
		return nil
	}
	return a.summarise(ticker, series)
}

// marketFor reads one listing's history and returns both what the share has
// been doing and where it last traded, from the single fetch.
//
// It is what gives a company outside the US a price at all: the quote feed
// stops at the US border, while the chart source answers for Hong Kong, Tokyo
// and London. The price that comes back is a close in the local currency, not a
// live quote, which the quote itself records.
func (a *App) marketFor(ctx context.Context, symbol string) (*model.Trading, *model.Quote) {
	series, ok := a.seriesFor(ctx, symbol)
	if !ok {
		return nil, nil
	}
	trading := a.summarise(symbol, series)
	if trading == nil {
		// A history too short or too stale to summarise is too stale to price.
		return nil, nil
	}
	quote, ok := prices.Latest(series)
	if !ok {
		return trading, nil
	}
	return trading, &quote
}

// seriesFor reads a symbol's daily history, or nothing.
func (a *App) seriesFor(ctx context.Context, symbol string) (prices.Series, bool) {
	if a.Market == nil {
		return prices.Series{}, false
	}

	ctx, cancel := context.WithTimeout(ctx, historyBudget)
	defer cancel()

	series, err := a.Market.Fetch(ctx, symbol)
	if err != nil {
		a.Log.Warn("price history", "ticker", symbol, "error", err)
		return prices.Series{}, false
	}
	return series, true
}

// summarise turns a history into the figures the brief quotes, or nothing where
// it describes a market that has moved on.
func (a *App) summarise(ticker string, series prices.Series) *model.Trading {
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

// priceCandidates attaches each new name's move on the day, wherever it is
// listed, which the new-names block then shows beside the ticker.
//
// Two sources, for the reason the closer look has two: the quote feed is live
// but stops at the US border, and the chart source answers for the other
// thirteen exchanges with the last close. A name that neither can price keeps
// its name and goes without a move.
func (a *App) priceCandidates(ctx context.Context, candidates []model.Candidate) []model.Candidate {
	if len(candidates) == 0 {
		return candidates
	}

	// One budget for the lot. Prices improve the brief and must never be what
	// delays it, and the chart source is a request per listing.
	ctx, cancel := context.WithTimeout(ctx, quoteBudget)
	defer cancel()

	out := make([]model.Candidate, len(candidates))
	copy(out, candidates)

	var symbols []string
	for _, c := range out {
		if c.Ticker != "" && (c.Exchange == "US" || c.Exchange == "") {
			symbols = append(symbols, c.Ticker)
		}
	}
	if a.Quotes.Enabled() && len(symbols) > 0 {
		quotes, _ := a.Quotes.Fetch(ctx, symbols)
		bySymbol := prices.Index(quotes)
		for i, c := range out {
			if q, ok := bySymbol[c.Ticker]; ok {
				out[i].Quote = &q
			}
		}
	}

	for i, c := range out {
		if out[i].Quote != nil || c.Exchange == "US" || c.Exchange == "" {
			continue
		}
		chart := prices.ChartSymbol(c.Ticker, c.Exchange)
		if chart == "" {
			continue
		}
		if _, quote := a.marketFor(ctx, chart); quote != nil {
			out[i].Quote = quote
		}
	}
	return out
}
