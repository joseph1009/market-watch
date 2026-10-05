package app

import (
	"context"
	"sync"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// marketBoard reads the market in its parts from the daily charts: the broad
// market, the sectors, a few industries and the macro funds, with the gaps
// between pairs of them. A chart that does not answer costs its line and its
// gaps, and a board without the S&P 500 fund is no board at all.
func (a *App) marketBoard(ctx context.Context) model.Board {
	if a.Market == nil {
		return model.Board{}
	}
	ctx, cancel := context.WithTimeout(ctx, chartBudget)
	defer cancel()

	symbols := prices.BoardSymbols()
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		series = make(map[string]prices.Series, len(symbols))
		jobs   = make(chan string)
	)
	for range chartWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for symbol := range jobs {
				s, err := a.Market.Fetch(ctx, symbol)
				if err != nil {
					a.Log.Warn("market board", "symbol", symbol, "error", err)
					continue
				}
				mu.Lock()
				series[symbol] = s
				mu.Unlock()
			}
		}()
	}
queue:
	for _, s := range symbols {
		select {
		case jobs <- s:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	wg.Wait()

	board := prices.MakeBoard(series, a.now())
	a.Log.Info("market board",
		"asked", len(symbols),
		"read", len(series),
		"funds", len(board.Funds),
		"gaps", len(board.Gaps),
		"unusual", len(board.Standouts(len(board.Gaps))))
	return board
}
