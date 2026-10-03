package app

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/search"
)

// moverGap is how far beyond the market a watchlist share must move, in
// percentage points, before the brief searches for why.
//
// Set against the S&P 500 fund rather than as a flat move. On a day the market
// falls 3%, a flat rule would search for every share that went with it, and
// the reason for all of them is the market; what the reader needs explained
// is the share that fell 8% while the rest fell 3, or rose while they fell.
const moverGap = 3.0

// moverBudget bounds the mover searches. Five searches, four at a time.
const moverBudget = time.Minute

// movers picks the watchlist shares that moved furthest beyond the market,
// furthest first, at most search.MaxMoverQueries of them.
//
// A price from before since is left out: it is a session the previous brief
// reported, as on the morning after a US holiday.
func movers(quotes []model.Quote, watched []string, since time.Time) []model.Quote {
	market := 0.0
	for _, q := range quotes {
		if q.Symbol == prices.MarketSymbol {
			market = q.Percent
		}
	}
	isWatched := make(map[string]bool, len(watched))
	for _, t := range watched {
		isWatched[strings.ToUpper(t)] = true
	}

	var out []model.Quote
	for _, q := range quotes {
		if !isWatched[q.Symbol] || math.Abs(q.Percent-market) < moverGap {
			continue
		}
		if !since.IsZero() && !q.AsOf.IsZero() && !q.AsOf.After(since) {
			continue
		}
		out = append(out, q)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return math.Abs(out[i].Percent-market) > math.Abs(out[j].Percent-market)
	})
	if len(out) > search.MaxMoverQueries {
		out = out[:search.MaxMoverQueries]
	}
	return out
}

// searchMovers asks, for each share that moved furthest, why it moved.
//
// The results join the day's articles like any other search's, so they are
// deduped, rated and cited with the rest. Where a feed already explained the
// move, the search finds the same story and dedupe folds it; checking the
// feeds first would save the credit but means waiting for them, and the
// allowance has room.
func (a *App) searchMovers(ctx context.Context, moved []model.Quote, names map[string]string) search.Result {
	if !a.Search.Enabled() || len(moved) == 0 {
		return search.Result{}
	}

	queries := make([]search.Query, 0, len(moved))
	var symbols []string
	for _, q := range moved {
		name := names[q.Symbol]
		if name == "" {
			name = a.companyName(ctx, q.Symbol)
		}
		queries = append(queries, search.MoverQuery(name, q.Symbol, q.Percent))
		symbols = append(symbols, q.Symbol+" "+q.Move())
	}

	ctx, cancel := context.WithTimeout(ctx, moverBudget)
	defer cancel()

	started := time.Now()
	found := a.Search.Collect(ctx, queries, a.searchSince())
	for _, err := range found.Errors {
		a.Log.Warn("mover search", "error", err)
	}
	a.Log.Info("searched movers",
		"movers", strings.Join(symbols, ", "),
		"articles", len(found.Articles),
		"credits", found.Credits,
		"took", time.Since(started).Round(time.Second))
	return found
}

// companyName is the name a company files under, for searching by when the
// watchlist has none for it. The ticker alone finds fewer of the stories about
// it (search.MoverQuery). Empty where the SEC index does not know the ticker,
// and the search goes by ticker.
func (a *App) companyName(ctx context.Context, ticker string) string {
	if a.Filings == nil {
		return ""
	}
	_, name, err := a.Filings.LookupCIK(ctx, ticker)
	if err != nil {
		return ""
	}
	return name
}

// marketMovesShown is how many of the market's outsized moves the line under
// the overview names: one line on a phone.
const marketMovesShown = 5

// marketMoves are the last session's biggest moves across the whole US
// market, followed or not, for the line under the overview. They are the
// moves the closer look's reactions start from (market.Moves): several times
// the share's usual daily move, on heavy trading, among companies worth
// US$2bn or more. The market's history is brought up to date first.
//
// None when there is no history yet, or when its latest session is one the
// previous brief already reported, as on the morning after a holiday.
func (a *App) marketMoves(ctx context.Context, since time.Time) []model.MarketMove {
	if a.MarketStore == nil {
		return nil
	}
	a.topUpMarket(ctx)
	listings, err := a.listings(ctx)
	if err != nil {
		a.Log.Warn("no moves across the market: Nasdaq's listings could not be read", "error", err)
		return nil
	}
	panel, moves, err := a.recentMoves(listings)
	if errors.Is(err, errHistoryFilling) {
		return nil // the closer look says so
	}
	if err != nil {
		a.Log.Warn("no moves across the market", "error", err)
		return nil
	}
	closed := sessionClose(panel.Last())
	if !since.IsZero() && !closed.After(since) {
		return nil
	}
	out := make([]model.MarketMove, 0, marketMovesShown)
	for _, m := range moves[:min(marketMovesShown, len(moves))] {
		out = append(out, model.MarketMove{
			Quote: model.Quote{Symbol: m.Symbol, Price: m.Price, Percent: 100 * m.Percent, AsOf: closed},
			Name:  market.PlainName(m.Name), Times: m.Times, Busy: m.Busy,
		})
	}
	return out
}

// sessionClose is when a session, dated as the market's history dates it,
// closed: four in the afternoon in New York.
func sessionClose(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 16, 0, 0, 0, market.NewYork())
}

// trendsFor reads the price history of the shares that moved furthest, so the
// brief can say what kind of move it was. Five requests at most, side by side.
// A history that cannot be read costs that share its context and nothing else.
func (a *App) trendsFor(ctx context.Context, moved []model.Quote) map[string]model.Trading {
	if a.Market == nil || len(moved) == 0 {
		return nil
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[string]model.Trading, len(moved))
	)
	for _, q := range moved {
		chart := prices.ChartSymbol(q.Symbol, "US")
		if chart == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if t := a.tradingFor(ctx, chart); t != nil {
				mu.Lock()
				out[q.Symbol] = *t
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}
