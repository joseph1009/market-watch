package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/prices"
)

// The market's history: two years of the whole US market's daily bars, kept
// on the data volume (internal/market), and Nasdaq's list of what each
// symbol is. The first fill is about five hundred requests at five a minute,
// so it runs in the background from startup and takes a couple of hours; the
// themes wait for it, and the reactions need only its last few months.

const (
	// marketChunk is how many days a background sync asks for before it
	// starts again from today, so a session that arrives during the first
	// fill is fetched within minutes rather than after it.
	marketChunk = 10

	// marketIdle is how long the background sync rests once the store is
	// full: the next session is served some hours after the close, and
	// checked for a few times a day.
	marketIdle = 3 * time.Hour

	// marketRetry is how long it rests after a failure: a day asked for
	// three times over forty seconds and refused each time.
	marketRetry = 5 * time.Minute

	// marketTopUp is how many days the closer look asks for before it reads
	// the store, and marketTopUpBudget how long it waits for them: the
	// background sync may be holding the store for a chunk of its own.
	marketTopUp       = 3
	marketTopUpBudget = 4 * time.Minute

	// listingsFresh is how old Nasdaq's list may be and still be used
	// without reading it again.
	listingsFresh = 20 * time.Hour
)

func (a *App) marketDir() string { return filepath.Join(a.Cfg.DataDir, "market") }

// RunMarket keeps the market's history filled and current until the context
// ends. Without a Massive key it does nothing, and the recommendations say
// why they cannot run.
func (a *App) RunMarket(ctx context.Context) error {
	if a.MarketStore == nil || !a.Movers.Enabled() {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		asked, err := a.MarketStore.Sync(ctx, a.Movers, a.now(), marketChunk)
		wait := marketIdle
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, prices.ErrMassiveKey):
			a.Log.Error("the market's history cannot be filled: Massive refused the key")
			wait = 24 * time.Hour
		case err != nil:
			a.Log.Warn("market history", "error", err)
			wait = marketRetry
		case asked == marketChunk:
			a.Log.Info("market history filling", "missing", a.MarketStore.Missing(a.now()))
			wait = 0
		}
		if wait == 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// topUpMarket fetches the latest sessions before the closer look reads them.
func (a *App) topUpMarket(ctx context.Context) {
	if a.MarketStore == nil || !a.Movers.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, marketTopUpBudget)
	defer cancel()
	if _, err := a.MarketStore.Sync(ctx, a.Movers, a.now(), marketTopUp); err != nil {
		a.Log.Warn("could not bring the market's history up to date", "error", err)
	}
}

// listingsCache is Nasdaq's list as kept on the data volume.
type listingsCache struct {
	Fetched  time.Time        `json:"fetched"`
	Listings []market.Listing `json:"listings"`
}

// listings is Nasdaq's list of every US listing, read at most once a day. A
// failed read falls back on the last one kept, however old: a company's
// industry and size change slowly.
func (a *App) listings(ctx context.Context) ([]market.Listing, error) {
	path := filepath.Join(a.marketDir(), "listings.json")
	var kept listingsCache
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &kept)
	}
	if len(kept.Listings) > 0 && a.now().Sub(kept.Fetched) < listingsFresh {
		return kept.Listings, nil
	}
	if a.Consensus == nil {
		if len(kept.Listings) > 0 {
			return kept.Listings, nil
		}
		return nil, errors.New("Nasdaq's listings are off (CONSENSUS=false), and none are kept")
	}
	fresh, err := a.Consensus.Listings(ctx)
	if err != nil {
		if len(kept.Listings) > 0 {
			a.Log.Warn("Nasdaq's listings not read; using the last kept", "error", err, "from", kept.Fetched)
			return kept.Listings, nil
		}
		return nil, err
	}
	if data, err := json.Marshal(listingsCache{Fetched: a.now(), Listings: fresh}); err == nil {
		if err := os.MkdirAll(a.marketDir(), 0o755); err == nil {
			tmp := path + ".tmp"
			if os.WriteFile(tmp, data, 0o644) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
	return fresh, nil
}

// singaporeStocks measures the Straits Times Index's companies from their
// prices on the Singapore Exchange, converted to US dollars so they stand
// beside the US market's on the same footing.
func (a *App) singaporeStocks(ctx context.Context) []market.Stock {
	list, err := config.Singapore()
	if err != nil || a.Market == nil {
		if err != nil {
			a.Log.Warn("singapore list", "error", err)
		}
		return nil
	}
	rates := a.dollarRates(ctx, []string{"SGD"})
	var out []market.Stock
	for _, s := range list {
		chart := prices.ChartSymbol(s.Symbol, "SP")
		series, ok := a.seriesFor(ctx, chart)
		if !ok {
			continue
		}
		var bars []market.Bar
		for _, b := range series.Bars {
			usd := 1.0
			if series.Currency != "" && series.Currency != "USD" {
				usd = rates.On(series.Currency, b.Date)
			}
			if usd <= 0 {
				continue
			}
			bars = append(bars, market.Bar{Open: b.Open * usd, Close: b.Close * usd, Volume: b.Volume})
		}
		out = append(out, market.MeasureSeries(market.SeriesFrom(s.Symbol+".SP", bars), market.Listing{
			Symbol: s.Symbol + ".SP", Name: s.Name, Country: "Singapore",
			Sector: "Singapore", Industry: "Singapore: " + s.Sector, NotListedInAmerica: true,
		}))
	}
	return out
}

// panelPath is a US symbol's sessions from the store as the scorecard reads
// them, each opened at half past nine in New York.
func panelPath(p *market.Panel, symbol string) ideas.Path {
	ser := p.Get(symbol)
	if ser == nil {
		return nil
	}
	ny := market.NewYork()
	path := make(ideas.Path, 0, len(p.Dates))
	for i, day := range p.Dates {
		if ser.Close[i] <= 0 {
			continue
		}
		opened := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, ny)
		path = append(path, ideas.Session{Opened: opened, Open: ser.Open[i], Close: ser.Close[i]})
	}
	return path
}
