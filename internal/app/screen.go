package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/joseph1009/market-watch/internal/consensus"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// screenRows writes one line for each followed company with a ticker: how
// the share moved today and over the stretches a reader thinks in, where it
// sits against its averages and its year's range, what analysts expect, and
// the day's articles that name it. The histories and the forecasts are read
// side by side, a few companies at a time; a company either cannot be read
// for keeps its row with what was read.
func (a *App) screenRows(ctx context.Context, groups []model.Group, cited []model.Article) []ideas.Row {
	type company struct{ ticker, name, sector string }
	var companies []company
	seen := map[string]bool{}
	for _, g := range groups {
		for _, c := range g.Companies {
			if c.Symbol == "" || seen[c.Symbol] {
				continue
			}
			seen[c.Symbol] = true
			companies = append(companies, company{c.Symbol, c.Name, g.Name})
		}
	}

	tickers := make([]string, len(companies))
	for i, c := range companies {
		tickers[i] = c.ticker
	}
	var expected map[string]consensus.Report
	done := make(chan struct{})
	go func() {
		defer close(done)
		if a.Consensus != nil {
			expected = a.Consensus.FetchAll(ctx, tickers, screenWorkers)
		}
	}()

	type market struct {
		trading *model.Trading
		quote   *model.Quote
	}
	markets := make([]market, len(companies))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range screenWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				t, q := a.marketFor(ctx, prices.ChartSymbol(companies[i].ticker, "US"))
				markets[i] = market{t, q}
			}
		}()
	}
queue:
	for i := range companies {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	wg.Wait()
	<-done

	rows := make([]ideas.Row, len(companies))
	for i, c := range companies {
		var r *consensus.Report
		if e, ok := expected[c.ticker]; ok {
			r = &e
		}
		rows[i] = ideas.Row{
			Ticker: c.ticker, Name: c.name, Sector: c.sector,
			Line:     screenLine(markets[i].quote, markets[i].trading, r),
			Articles: fundamentals.Relevant(cited, c.ticker, c.name, 3),
		}
		if q := markets[i].quote; q != nil {
			rows[i].PricedAt = q.AsOf
		}
	}
	return rows
}

// shortWindows are the return labels in a screen row.
var shortWindows = map[string]string{
	"1 week": "1W", "1 month": "1M", "3 months": "3M", "6 months": "6M", "12 months": "12M", "year to date": "YTD",
}

// screenLine is one company's figures in a line of a table.
func screenLine(q *model.Quote, t *model.Trading, r *consensus.Report) string {
	var parts []string
	if q != nil {
		parts = append(parts, "last session "+q.Move())
	}
	if t == nil {
		parts = append(parts, "no price history")
	} else {
		var moves []string
		for _, ret := range t.Returns {
			if label, ok := shortWindows[ret.Over]; ok {
				moves = append(moves, fmt.Sprintf("%s %+.0f%%", label, ret.Percent))
			}
		}
		if len(moves) > 0 {
			parts = append(parts, strings.Join(moves, " "))
		}
		if t.MA50 > 0 {
			parts = append(parts, fmt.Sprintf("%+.0f%% vs 50-day avg", (t.Last/t.MA50-1)*100))
		}
		if t.MA200 > 0 {
			parts = append(parts, fmt.Sprintf("%+.0f%% vs 200-day avg", (t.Last/t.MA200-1)*100))
		}
		if t.High52 > t.Low52 {
			parts = append(parts, fmt.Sprintf("at %.0f%% of its year's range", (t.Last-t.Low52)/(t.High52-t.Low52)*100))
		}
	}
	if r != nil {
		price := 0.0
		if t != nil {
			price = t.Last
		}
		parts = append(parts, r.Line(price))
	}
	return strings.Join(parts, "; ")
}
