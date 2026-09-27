// Package consensus reads what analysts expect of a company, and what its
// insiders, short sellers and funds have been doing, from the data behind
// Nasdaq's website.
//
// The accounts say what a company has done and the price what the market pays
// for it; neither says what the market expects, which is the other half of
// whether a move was deserved. A share that fell 8% on results that beat by
// 18% is a different case from one that fell 8% on a miss, and only the
// forecast it was measured against tells them apart.
//
// Nasdaq publishes no API for this. These are the endpoints its own pages
// read, answered without a key to anything that sends a browser's headers --
// the same footing as the chart source in internal/prices. They can change or
// close without notice, so every figure here is best-effort, and a company
// they cannot be read for is written up without them.
package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.nasdaq.com/api"

	// requestTimeout is per request. Nasdaq takes one to three seconds to
	// answer each; a request still waiting at ten is not coming back.
	requestTimeout = 10 * time.Second

	// maxBody bounds a reply. The largest, the institutional holdings, is a
	// few kilobytes.
	maxBody = 1 << 20

	// browser is the User-Agent sent. Nasdaq closes the connection on Go's
	// default one without answering.
	browser = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

// ErrNotCovered means Nasdaq has nothing for the symbol: a fund, a foreign
// listing, or a company no analyst follows.
var ErrNotCovered = errors.New("not covered")

// Client reads the endpoints. The zero value works.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

// Estimate is the analysts' consensus for one fiscal period, and how it has
// been moving: Up and Down count the estimates raised and cut in the last four
// weeks, which says which way expectations are travelling before the average
// shows it.
type Estimate struct {
	Period    string // "Aug 2026", the period's end as Nasdaq writes it
	EPS       float64
	High, Low float64
	Analysts  int
	Up, Down  int
}

// Surprise is one reported quarter against what was expected of it.
type Surprise struct {
	Period   string
	Reported time.Time
	EPS      float64
	Expected float64
	Percent  float64
}

// Report is everything read for one company. A part that could not be read is
// left at its zero value, and Missing names it.
type Report struct {
	Symbol string

	Quarters []Estimate
	Years    []Estimate

	// The price target is the average of the analysts', with its range, and
	// Buy, Hold and Sell how many rate it each way.
	Target, TargetLow, TargetHigh float64
	Buy, Hold, Sell               int

	Surprises []Surprise

	// NextResults is the day the company is next due to report, and
	// ResultsWhen whether before the open or after the close where that is
	// known. ResultsEstimated means nobody has announced the date: it is
	// Zacks' guess from the company's past reporting days, which can be a week
	// or more out.
	NextResults      time.Time
	ResultsWhen      string
	ResultsEstimated bool

	// Insider trades over the last three and twelve months: open-market buys,
	// sales, and the net shares bought (negative where they sold more).
	InsiderBuys3, InsiderSells3   int
	InsiderBuys12, InsiderSells12 int
	InsiderNet3, InsiderNet12     float64

	// Short interest at the last settlement and the one before, and how many
	// days of normal trading it would take to buy it back.
	ShortShares, ShortPrior float64
	ShortAsOf               time.Time
	DaysToCover             float64

	// Funds: the share of the stock institutions hold, and how many of them
	// added to or cut their holdings in the latest filings.
	Institutional              float64 // percent
	FundsAdded, FundsCut       int
	SharesAddedBy, SharesCutBy float64
	FundsOpened, FundsSoldOut  int
	InstitutionalShares        float64
	Missing                    []string
}

// Covered reports whether anything at all was read.
func (r Report) Covered() bool {
	return len(r.Quarters) > 0 || len(r.Years) > 0 || r.Target > 0 || len(r.Surprises) > 0
}

// Fetch reads everything for a US-listed symbol. The parts are read one after
// another rather than at once: each takes a second or two, and six at a time
// from one address is how an unofficial endpoint learns to refuse it.
func (c *Client) Fetch(ctx context.Context, symbol string) (Report, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	r := Report{Symbol: symbol}
	parts := []struct {
		name string
		read func(context.Context, string, *Report) error
	}{
		{"estimates", c.estimates},
		{"price target", c.target},
		{"earnings surprises", c.surprises},
		{"next results date", c.resultsDate},
		{"insider trades", c.insiders},
		{"short interest", c.short},
		{"fund holdings", c.holdings},
	}
	for _, p := range parts {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if err := p.read(ctx, symbol, &r); err != nil {
			r.Missing = append(r.Missing, p.name)
		}
	}
	if !r.Covered() {
		return r, fmt.Errorf("%s: %w", symbol, ErrNotCovered)
	}
	return r, nil
}

// Estimates reads the forecasts alone: one request, which is what proves the
// endpoints still answer.
func (c *Client) Estimates(ctx context.Context, symbol string) (Report, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	r := Report{Symbol: symbol}
	if err := c.estimates(ctx, symbol, &r); err != nil {
		return r, err
	}
	if !r.Covered() {
		return r, fmt.Errorf("%s: %w", symbol, ErrNotCovered)
	}
	return r, nil
}

func (c *Client) estimates(ctx context.Context, symbol string, r *Report) error {
	var doc struct {
		Quarterly struct{ Rows []estimateRow } `json:"quarterlyForecast"`
		Yearly    struct{ Rows []estimateRow } `json:"yearlyForecast"`
	}
	if err := c.get(ctx, "analyst/"+symbol+"/earnings-forecast", &doc); err != nil {
		return err
	}
	r.Quarters = estimatesFrom(doc.Quarterly.Rows)
	r.Years = estimatesFrom(doc.Yearly.Rows)
	return nil
}

type estimateRow struct {
	FiscalEnd string `json:"fiscalEnd"`
	EPS       num    `json:"consensusEPSForecast"`
	High      num    `json:"highEPSForecast"`
	Low       num    `json:"lowEPSForecast"`
	Analysts  num    `json:"noOfEstimates"`
	Up        num    `json:"up"`
	Down      num    `json:"down"`
}

func estimatesFrom(rows []estimateRow) []Estimate {
	var out []Estimate
	for _, row := range rows {
		if !row.EPS.ok || row.FiscalEnd == "" {
			continue
		}
		out = append(out, Estimate{
			Period: row.FiscalEnd, EPS: row.EPS.v, High: row.High.v, Low: row.Low.v,
			Analysts: int(row.Analysts.v), Up: int(row.Up.v), Down: int(row.Down.v),
		})
	}
	return out
}

func (c *Client) target(ctx context.Context, symbol string, r *Report) error {
	var doc struct {
		Overview struct {
			Low    num `json:"lowPriceTarget"`
			High   num `json:"highPriceTarget"`
			Target num `json:"priceTarget"`
			Buy    num `json:"buy"`
			Hold   num `json:"hold"`
			Sell   num `json:"sell"`
		} `json:"consensusOverview"`
	}
	if err := c.get(ctx, "analyst/"+symbol+"/targetprice", &doc); err != nil {
		return err
	}
	o := doc.Overview
	r.Target, r.TargetLow, r.TargetHigh = o.Target.v, o.Low.v, o.High.v
	r.Buy, r.Hold, r.Sell = int(o.Buy.v), int(o.Hold.v), int(o.Sell.v)
	return nil
}

func (c *Client) surprises(ctx context.Context, symbol string, r *Report) error {
	var doc struct {
		Table struct {
			Rows []struct {
				Period   string `json:"fiscalQtrEnd"`
				Reported string `json:"dateReported"`
				EPS      num    `json:"eps"`
				Expected num    `json:"consensusForecast"`
				Percent  num    `json:"percentageSurprise"`
			} `json:"rows"`
		} `json:"earningsSurpriseTable"`
	}
	if err := c.get(ctx, "company/"+symbol+"/earnings-surprise", &doc); err != nil {
		return err
	}
	for _, row := range doc.Table.Rows {
		if !row.EPS.ok {
			continue
		}
		reported, _ := time.Parse("1/2/2006", row.Reported)
		r.Surprises = append(r.Surprises, Surprise{
			Period: row.Period, Reported: reported,
			EPS: row.EPS.v, Expected: row.Expected.v, Percent: row.Percent.v,
		})
	}
	return nil
}

// resultsDay finds the date in the page's sentence, which is the only place
// the reply states it with its year and whether it is only an estimate:
// "... is expected* to report earnings on  09/30/2026 after market close."
var resultsDay = regexp.MustCompile(`report earnings on\s+(\d{2}/\d{2}/\d{4})\s*(before market open|after market close)?`)

func (c *Client) resultsDate(ctx context.Context, symbol string, r *Report) error {
	var doc struct {
		Text string `json:"reportText"`
	}
	if err := c.get(ctx, "analyst/"+symbol+"/earnings-date", &doc); err != nil {
		return err
	}
	m := resultsDay.FindStringSubmatch(doc.Text)
	if m == nil {
		return ErrNotCovered
	}
	day, err := time.Parse("01/02/2006", m[1])
	if err != nil {
		return err
	}
	r.NextResults, r.ResultsWhen = day, m[2]
	r.ResultsEstimated = strings.Contains(doc.Text, "estimated to report")
	return nil
}

func (c *Client) insiders(ctx context.Context, symbol string, r *Report) error {
	type row struct {
		Kind     string `json:"insiderTrade"`
		Months3  num    `json:"months3"`
		Months12 num    `json:"months12"`
	}
	var doc struct {
		Trades struct{ Rows []row } `json:"numberOfTrades"`
		Shares struct{ Rows []row } `json:"numberOfSharesTraded"`
	}
	if err := c.get(ctx, "company/"+symbol+"/insider-trades?limit=1&type=ALL", &doc); err != nil {
		return err
	}
	for _, x := range doc.Trades.Rows {
		switch {
		case strings.Contains(x.Kind, "Buys"):
			r.InsiderBuys3, r.InsiderBuys12 = int(x.Months3.v), int(x.Months12.v)
		case strings.Contains(x.Kind, "Sells"):
			r.InsiderSells3, r.InsiderSells12 = int(x.Months3.v), int(x.Months12.v)
		}
	}
	for _, x := range doc.Shares.Rows {
		if strings.Contains(x.Kind, "Net") {
			r.InsiderNet3, r.InsiderNet12 = x.Months3.v, x.Months12.v
		}
	}
	return nil
}

func (c *Client) short(ctx context.Context, symbol string, r *Report) error {
	var doc struct {
		Table struct {
			Rows []struct {
				Date        string `json:"settlementDate"`
				Interest    num    `json:"interest"`
				DaysToCover num    `json:"daysToCover"`
			} `json:"rows"`
		} `json:"shortInterestTable"`
	}
	if err := c.get(ctx, "quote/"+symbol+"/short-interest?assetClass=stocks", &doc); err != nil {
		return err
	}
	rows := doc.Table.Rows
	if len(rows) == 0 {
		return ErrNotCovered
	}
	r.ShortShares, r.DaysToCover = rows[0].Interest.v, rows[0].DaysToCover.v
	r.ShortAsOf, _ = time.Parse("01/02/2006", rows[0].Date)
	if len(rows) > 1 {
		r.ShortPrior = rows[1].Interest.v
	}
	return nil
}

func (c *Client) holdings(ctx context.Context, symbol string, r *Report) error {
	type row struct {
		Positions string `json:"positions"`
		Holders   num    `json:"holders"`
		Shares    num    `json:"shares"`
	}
	var doc struct {
		Summary struct {
			Percent struct {
				Value num `json:"value"`
			} `json:"SharesOutstandingPCT"`
		} `json:"ownershipSummary"`
		Active struct{ Rows []row } `json:"activePositions"`
		NewOut struct{ Rows []row } `json:"newSoldOutPositions"`
	}
	if err := c.get(ctx, "company/"+symbol+"/institutional-holdings?limit=1&type=TOTAL", &doc); err != nil {
		return err
	}
	r.Institutional = doc.Summary.Percent.Value.v
	for _, x := range append(doc.Active.Rows, doc.NewOut.Rows...) {
		switch {
		case strings.HasPrefix(x.Positions, "Increased"):
			r.FundsAdded, r.SharesAddedBy = int(x.Holders.v), x.Shares.v
		case strings.HasPrefix(x.Positions, "Decreased"):
			r.FundsCut, r.SharesCutBy = int(x.Holders.v), x.Shares.v
		case strings.HasPrefix(x.Positions, "New"):
			r.FundsOpened = int(x.Holders.v)
		case strings.HasPrefix(x.Positions, "Sold Out"):
			r.FundsSoldOut = int(x.Holders.v)
		case strings.HasPrefix(x.Positions, "Total"):
			r.InstitutionalShares = x.Shares.v
		}
	}
	return nil
}

// get reads one endpoint's "data" into into. Nasdaq answers an unknown symbol
// with a 200 and a null data field, which is ErrNotCovered.
func (c *Client) get(ctx context.Context, path string, into any) error {
	return c.getWith(ctx, path, into, maxBody, requestTimeout)
}

// getWith is get with its own bounds on the reply's size and wait.
func (c *Client) getWith(ctx context.Context, path string, into any, limit int64, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browser)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	var doc struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(&doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Data) == 0 || string(doc.Data) == "null" {
		return ErrNotCovered
	}
	return json.Unmarshal(doc.Data, into)
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// num reads a figure written either as a number or as the website shows it:
// "$1,045.82", "87.97%", "29,705,339", and "(178,789)" for a negative.
type num struct {
	v  float64
	ok bool
}

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	s = strings.Trim(s, `"`)
	negative := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	s = strings.NewReplacer("$", "", ",", "", "%", "", "(", "", ")", "", " ", "").Replace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // "N/A", "--": not known, and not an error in the reply
	}
	if negative {
		v = -v
	}
	n.v, n.ok = v, true
	return nil
}
