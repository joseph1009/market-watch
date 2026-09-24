// Package prices reads what shares actually did, so the brief can say how the
// market answered the news rather than only what was written.
//
// The keyed quote feed here covers US listings. Finnhub's free tier is US-only,
// and so, despite the marketing, is Twelve Data's: Hong Kong answers "available
// starting with the Pro or Venture plan", London "the Grow or Venture plan",
// and Tokyo and Singapore do not resolve at all. A listing outside the US is
// therefore priced from the daily chart history instead -- see Latest, which
// answers for those markets in the currency the share actually trades in.
package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

const (
	quoteURL = "https://finnhub.io/api/v1/quote"

	// Finnhub's free tier allows sixty calls a minute. One a second stays
	// inside it with room to spare, and a day's list is a few dozen symbols.
	requestPause = 1100 * time.Millisecond

	// maxSymbols bounds a run. A brief prices the benchmark funds and every
	// watchlist share -- about 110 today, two minutes at this pace -- so a
	// share that moved without making the news is still seen. The bound is
	// there so a watchlist grown far past that cannot stretch the run for ever.
	maxSymbols = 150
)

// Client reads quotes.
type Client struct {
	APIKey string
	HTTP   *http.Client
	URL    string

	// Pause overrides the rate limit in tests.
	Pause time.Duration
}

// Enabled reports whether quotes can be read at all.
func (c *Client) Enabled() bool { return c != nil && c.APIKey != "" }

// Fetch reads one quote per symbol, in order, skipping what it cannot get. The
// symbols it failed on are returned so the caller can say so rather than
// leaving a silent gap.
func (c *Client) Fetch(ctx context.Context, symbols []string) ([]model.Quote, []string) {
	if !c.Enabled() || len(symbols) == 0 {
		return nil, nil
	}
	if len(symbols) > maxSymbols {
		symbols = symbols[:maxSymbols]
	}

	var (
		quotes []model.Quote
		missed []string
	)
	for i, symbol := range symbols {
		if i > 0 {
			select {
			case <-ctx.Done():
				return quotes, append(missed, symbols[i:]...)
			case <-time.After(c.pause()):
			}
		}

		q, err := c.one(ctx, symbol)
		if err != nil {
			missed = append(missed, symbol)
			continue
		}
		quotes = append(quotes, q)
	}
	return quotes, missed
}

// one reads a single symbol. Finnhub answers an unknown symbol with a quote of
// zeros rather than an error, so a zero close is treated as no answer: a price
// of nothing is never right, and printing it would be worse than omitting it.
func (c *Client) one(ctx context.Context, symbol string) (model.Quote, error) {
	url := fmt.Sprintf("%s?symbol=%s&token=%s", c.url(), strings.ToUpper(symbol), c.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return model.Quote{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return model.Quote{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return model.Quote{}, fmt.Errorf("%s: %s", symbol, resp.Status)
	}

	var body struct {
		Current  float64 `json:"c"`
		Change   float64 `json:"d"`
		Percent  float64 `json:"dp"`
		High     float64 `json:"h"`
		Low      float64 `json:"l"`
		Previous float64 `json:"pc"`
		At       int64   `json:"t"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return model.Quote{}, err
	}
	if body.Current == 0 {
		return model.Quote{}, fmt.Errorf("%s: no quote", symbol)
	}

	q := model.Quote{
		Symbol:   strings.ToUpper(symbol),
		Price:    body.Current,
		Previous: body.Previous,
		Change:   body.Change,
		Percent:  body.Percent,
		High:     body.High,
		Low:      body.Low,
	}
	if body.At > 0 {
		q.AsOf = time.Unix(body.At, 0).UTC()
	}
	return q, nil
}

func (c *Client) url() string {
	if c.URL != "" {
		return c.URL
	}
	return quoteURL
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) pause() time.Duration {
	if c.Pause > 0 {
		return c.Pause
	}
	return requestPause
}

// MarketSymbol is the fund a share's move is set against to say whether it
// moved with the market or on its own: the S&P 500's.
const MarketSymbol = "SPY"

// Benchmarks are the market-wide and sector moves the brief is written against.
//
// They are exchange-traded funds rather than the indices themselves: index
// levels sit behind subscriptions on every free tier, while the funds that
// track them are ordinary shares and quote freely. The brief has to say so --
// an ETF is not its index, and on a bad day it can drift from it.
var Benchmarks = []struct {
	Symbol string
	Label  string
}{
	{MarketSymbol, "S&P 500 (SPY fund)"},
	{"QQQ", "Nasdaq 100 (QQQ fund)"},
	{"IWM", "US small caps (IWM fund)"},
	{"SMH", "Semiconductors (SMH fund)"},
	{"XLE", "Energy (XLE fund)"},
	{"XLF", "Financials (XLF fund)"},
	{"XLV", "Healthcare (XLV fund)"},
	{"XLI", "Industrials (XLI fund)"},
	{"XLY", "Consumer discretionary (XLY fund)"},
	{"USO", "Crude oil (USO fund)"},
	{"GLD", "Gold (GLD fund)"},
	{"TLT", "Long US Treasuries (TLT fund)"},
}

// BenchmarkSymbols is the list in fetch order.
func BenchmarkSymbols() []string {
	out := make([]string, 0, len(Benchmarks))
	for _, b := range Benchmarks {
		out = append(out, b.Symbol)
	}
	return out
}

// LabelFor gives a benchmark's plain-English name, and the bare symbol for
// anything else.
func LabelFor(symbol string) string {
	for _, b := range Benchmarks {
		if b.Symbol == symbol {
			return b.Label
		}
	}
	return symbol
}

// Index maps quotes by symbol for lookup while rendering.
func Index(quotes []model.Quote) map[string]model.Quote {
	out := make(map[string]model.Quote, len(quotes))
	for _, q := range quotes {
		out[q.Symbol] = q
	}
	return out
}
