package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Daily history comes from Yahoo's charting endpoint. It is the one free source
// that answers for a share's whole trading history, in the currency it actually
// trades in, on every market the analysis is likely to reach -- Taipei, Hong
// Kong and Tokyo answer as readily as New York, where every keyed vendor on a
// free tier stops at the US border.
//
// Two honest caveats, both of which the code is built around. It is not a
// documented API, so it can change or refuse without notice: every use of it is
// best-effort, and a failure costs the analysis one section and nothing else.
// And Stooq, the obvious alternative, now answers with a JavaScript proof of
// work rather than the CSV it used to serve, which no Go client can satisfy.
const (
	chartURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

	// chartRange is how much history to ask for. Two years covers a two-hundred
	// day average with a year of context behind it, which is what the twelve
	// month return needs.
	chartRange = "2y"

	// minBars is the least history worth summarising. Below about six weeks
	// there is no average to speak of and the volatility figure is noise.
	minBars = 25

	// chartAgent identifies the caller. Deliberately not the SEC User-Agent:
	// that one carries a personal email address, which the SEC asks for and
	// nobody else is entitled to.
	chartAgent = "market-watch/1.0"
)

// Bar is one session.
type Bar struct {
	Date time.Time

	// Opened is when the session opened, as the source stamps a daily bar:
	// 13:30 UTC for New York, 01:00 UTC for Singapore. Date is the same day
	// cut to midnight, which says which session but not whether it had begun
	// by a given moment.
	Opened time.Time

	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// Series is a share's daily history in the currency it trades in.
type Series struct {
	Symbol   string
	Currency string
	Name     string
	Bars     []Bar

	// Traded is when the last price was struck, where the source says. A bar
	// is dated by its day alone, and midnight on the day of a New York session
	// is earlier than the brief before it: a price dated that way would read as
	// one the last brief already had.
	Traded time.Time
}

// History reads daily bars.
type History struct {
	HTTP *http.Client
	URL  string
}

// Fetch reads a symbol's recent daily history, oldest first.
//
// Sessions the source has no price for are dropped rather than carried
// forward: a filled-in price would flow into every average below and could not
// afterwards be told from a real one.
func (h *History) Fetch(ctx context.Context, symbol string) (Series, error) {
	return h.fetch(ctx, symbol, chartRange, "1d", minBars)
}

// Monthly reads five years of month-end prices, oldest first: enough to say
// what a company's share cost at each of its last five year ends, which the
// daily two years cannot reach.
func (h *History) Monthly(ctx context.Context, symbol string) (Series, error) {
	return h.fetch(ctx, symbol, "5y", "1mo", 12)
}

func (h *History) fetch(ctx context.Context, symbol, span, interval string, least int) (Series, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return Series{}, fmt.Errorf("no symbol")
	}

	url := fmt.Sprintf("%s%s?range=%s&interval=%s", h.url(), symbol, span, interval)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Series{}, err
	}
	req.Header.Set("User-Agent", chartAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := h.client().Do(req)
	if err != nil {
		return Series{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Series{}, fmt.Errorf("%s: %s", symbol, resp.Status)
	}

	var body chartBody
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return Series{}, fmt.Errorf("%s: %w", symbol, err)
	}
	if body.Chart.Error != nil {
		return Series{}, fmt.Errorf("%s: %s", symbol, body.Chart.Error.Description)
	}
	if len(body.Chart.Result) == 0 || len(body.Chart.Result[0].Indicators.Quote) == 0 {
		return Series{}, fmt.Errorf("%s: no history", symbol)
	}

	result := body.Chart.Result[0]
	q := result.Indicators.Quote[0]

	series := Series{
		Symbol:   symbol,
		Currency: strings.ToUpper(result.Meta.Currency),
		Name:     result.Meta.LongName,
	}
	if result.Meta.RegularMarketTime > 0 {
		series.Traded = time.Unix(result.Meta.RegularMarketTime, 0).UTC()
	}
	for i, ts := range result.Timestamp {
		price := at(q.Close, i)
		if price <= 0 {
			continue
		}
		bar := Bar{
			Date:   time.Unix(ts, 0).UTC().Truncate(24 * time.Hour),
			Opened: time.Unix(ts, 0).UTC(),
			Close:  price,
			Open:   at(q.Open, i),
			High:   at(q.High, i),
			Low:    at(q.Low, i),
			Volume: at(q.Volume, i),
		}
		if bar.High <= 0 {
			bar.High = bar.Close
		}
		if bar.Low <= 0 {
			bar.Low = bar.Close
		}
		series.Bars = append(series.Bars, bar)
	}
	if len(series.Bars) < least {
		return Series{}, fmt.Errorf("%s: only %d sessions of history", symbol, len(series.Bars))
	}
	sort.Slice(series.Bars, func(i, j int) bool {
		return series.Bars[i].Date.Before(series.Bars[j].Date)
	})
	return series, nil
}

// chartBody is the reply shape. Prices come back as nullable numbers: a
// suspended or untraded session is a null in the middle of the array, not a
// missing entry, so the index still lines up with the timestamps.
type chartBody struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency string `json:"currency"`
				Symbol   string `json:"symbol"`
				LongName string `json:"longName"`
				// When the last price was struck, in Unix seconds.
				RegularMarketTime int64 `json:"regularMarketTime"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*float64 `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func (h *History) url() string {
	if h.URL != "" {
		return h.URL
	}
	return chartURL
}

func (h *History) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return http.DefaultClient
}

// Latest is the last session as a quote: where the share stands, and how far
// that is from the close before it.
//
// This is how a listing outside the US gets a price at all. The keyed quote
// feeds stop at the US border on every free tier, while the chart source
// answers for Hong Kong, Tokyo, London and the rest -- in the currency the
// share actually trades in, which the quote carries so that nobody reads a
// Hong Kong level as dollars.
//
// What comes back is a close, not a tick: on a market still open it is the
// latest price the chart holds rather than the day's final one. That is what a
// reader means by "today" either way. It is dated when that price was struck
// where the chart says, and by the session's day where it does not.
//
// It is also the US price scan's second source, for the shares the quote feed
// did not answer for.
func Latest(s Series) (model.Quote, bool) {
	if len(s.Bars) < 2 {
		return model.Quote{}, false
	}
	last, previous := s.Bars[len(s.Bars)-1], s.Bars[len(s.Bars)-2]
	if last.Close <= 0 || previous.Close <= 0 {
		return model.Quote{}, false
	}
	asOf := last.Date
	if !s.Traded.Before(last.Date) {
		asOf = s.Traded
	}
	return model.Quote{
		Symbol:   s.Symbol,
		Price:    last.Close,
		Previous: previous.Close,
		Change:   last.Close - previous.Close,
		Percent:  (last.Close - previous.Close) / previous.Close * 100,
		High:     last.High,
		Low:      last.Low,
		Currency: s.Currency,
		AsOf:     asOf,
	}, true
}

// windows are the stretches a reader thinks in. A window the history does not
// cover is left out: computing "twelve months" from eight months of data and
// labelling it twelve would be the worst kind of wrong, since nothing in the
// figure itself says how it was made.
var windows = []struct {
	label string
	days  int
}{
	{"1 week", 7},
	{"1 month", 30},
	{"3 months", 91},
	{"6 months", 182},
	{"12 months", 365},
}

// Summarise turns a history into the statistics the analysis quotes.
//
// Every figure here is descriptive: what the price has been, how much of it
// changed hands, and how widely it has swung. None of them is a forecast, and
// the prompt says so, because a moving average is exactly the kind of number a
// reader can mistake for a recommendation if nobody tells them otherwise.
func Summarise(s Series, now time.Time) model.Trading {
	if len(s.Bars) < minBars {
		return model.Trading{}
	}

	last := s.Bars[len(s.Bars)-1]
	t := model.Trading{
		Symbol:      s.Symbol,
		Currency:    s.Currency,
		AsOf:        last.Date,
		From:        s.Bars[0].Date,
		Days:        len(s.Bars),
		Last:        last.Close,
		VolumeLast:  last.Volume,
		VolumeAvg30: mean(volumes(tail(s.Bars, 30))),
		VolumeAvg90: mean(volumes(tail(s.Bars, 90))),
		VWAP30:      vwap(tail(s.Bars, 30)),
		VWAP90:      vwap(tail(s.Bars, 90)),
		Volatility:  volatility(tail(s.Bars, 252)),
	}

	// A session dated today has not necessarily closed, and the source returns
	// it from the first minute of trading onwards.
	t.Partial = !last.Date.Before(now.UTC().Truncate(24 * time.Hour))

	// An average is only an average over the days it claims. Fifty sessions of
	// history do not make a two-hundred day average, and calling the mean of
	// what happens to be there by that name would misstate it.
	if len(s.Bars) >= 50 {
		t.MA50 = mean(closes(tail(s.Bars, 50)))
	}
	if len(s.Bars) >= 200 {
		t.MA200 = mean(closes(tail(s.Bars, 200)))
	}

	for _, w := range windows {
		if move, ok := change(s.Bars, last.Date.AddDate(0, 0, -w.days)); ok {
			move.Over = w.label
			t.Returns = append(t.Returns, move)
		}
	}
	// The year to date runs from the last session of the previous year, which
	// is where the reader's own year-to-date figure starts from.
	lastYear := time.Date(last.Date.Year(), time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if move, ok := change(s.Bars, lastYear); ok {
		move.Over = "year to date"
		t.Returns = append(t.Returns, move)
	}

	year := last.Date.AddDate(-1, 0, 0)
	for _, b := range s.Bars {
		if b.Date.Before(year) {
			continue
		}
		if t.High52 == 0 || b.High > t.High52 {
			t.High52, t.HighAt = b.High, b.Date
		}
		if t.Low52 == 0 || (b.Low > 0 && b.Low < t.Low52) {
			t.Low52, t.LowAt = b.Low, b.Date
		}
	}
	t.Path = path(s.Bars, year)
	return t
}

// path is each session's close from a date on, with the 50- and 200-day
// averages as they stood that day.
func path(bars []Bar, from time.Time) []model.PricePoint {
	var out []model.PricePoint
	for i, b := range bars {
		if b.Date.Before(from) || b.Close <= 0 {
			continue
		}
		p := model.PricePoint{Date: b.Date, Close: b.Close}
		if i >= 49 {
			p.MA50 = mean(closes(bars[i-49 : i+1]))
		}
		if i >= 199 {
			p.MA200 = mean(closes(bars[i-199 : i+1]))
		}
		out = append(out, p)
	}
	return out
}

// change is the move from the last close at or before a date to the latest one,
// and reports false where the history does not reach back that far. The close
// it started from comes back with it: a percentage alone does not say whether
// the share went from 150 to 900 or from 900 to 5,400.
func change(bars []Bar, from time.Time) (model.Return, bool) {
	if len(bars) == 0 || bars[0].Date.After(from) {
		return model.Return{}, false
	}
	var base Bar
	for _, b := range bars {
		if b.Date.After(from) {
			break
		}
		base = b
	}
	if base.Close <= 0 {
		return model.Return{}, false
	}
	return model.Return{
		Percent: (bars[len(bars)-1].Close - base.Close) / base.Close * 100,
		From:    base.Close,
		Since:   base.Date,
	}, true
}

// volatility is the annualised standard deviation of daily moves, as a
// percentage. A trading year is about 252 sessions, which is what the square
// root scales by.
func volatility(bars []Bar) float64 {
	if len(bars) < 30 {
		return 0
	}
	var moves []float64
	for i := 1; i < len(bars); i++ {
		if bars[i-1].Close <= 0 {
			continue
		}
		moves = append(moves, math.Log(bars[i].Close/bars[i-1].Close))
	}
	if len(moves) < 2 {
		return 0
	}
	avg := mean(moves)
	sum := 0.0
	for _, m := range moves {
		sum += (m - avg) * (m - avg)
	}
	return math.Sqrt(sum/float64(len(moves)-1)) * math.Sqrt(252) * 100
}

// vwap weights each session's close by the shares that traded in it, and falls
// back to a plain average of closes where the source reports no volume.
func vwap(bars []Bar) float64 {
	var paid, shares float64
	for _, b := range bars {
		paid += b.Close * b.Volume
		shares += b.Volume
	}
	if shares <= 0 {
		return mean(closes(bars))
	}
	return paid / shares
}

func tail(bars []Bar, n int) []Bar {
	if len(bars) <= n {
		return bars
	}
	return bars[len(bars)-n:]
}

func closes(bars []Bar) []float64 {
	out := make([]float64, 0, len(bars))
	for _, b := range bars {
		out = append(out, b.Close)
	}
	return out
}

func volumes(bars []Bar) []float64 {
	out := make([]float64, 0, len(bars))
	for _, b := range bars {
		out = append(out, b.Volume)
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// at reads one nullable price, as zero where the session has none.
func at(xs []*float64, i int) float64 {
	if i >= len(xs) || xs[i] == nil {
		return 0
	}
	return *xs[i]
}
