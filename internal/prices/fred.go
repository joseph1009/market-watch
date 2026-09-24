// FRED: levels, so the brief can say where rates actually are, not only what
// outlets said about them.
//
// These are observations, not articles: a series of dated numbers with no
// headline, nothing to deduplicate against and no watchlist to match. They are
// therefore kept out of the article pipeline entirely and handed to the
// summarizer as their own block. Synthesizing a headline ("10-year yield rose
// 8bp") would fit the existing machinery, but it would be manufacturing a
// sentence nobody wrote and pricing it as though a source had said it.

package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const observationsURL = "https://api.stlouisfed.org/fred/series/observations"

// Indicator is one FRED series worth putting in front of the summarizer.
type Indicator struct {
	ID    string
	Label string
	Unit  string

	// Units asks FRED to transform the series before sending it, in FRED's
	// own codes: "pc1" is the percent change from a year earlier. Empty sends
	// the series as published. The consumer price index is published as an
	// index level -- 331.6 -- which means nothing to a reader until it is a
	// yearly rate.
	Units string

	// Monthly marks a series published once a month. Its previous reading is
	// last month's, and it has no week-ago reading to compare with.
	Monthly bool
}

// Indicators are the levels the Macro & Rates section is written against.
var Indicators = []Indicator{
	{ID: "DGS10", Label: "US 10-year Treasury yield", Unit: "%"},
	{ID: "DGS2", Label: "US 2-year Treasury yield", Unit: "%"},
	{ID: "T10Y2Y", Label: "Gap between the 10-year and 2-year Treasury yields (the 2s10s curve)", Unit: "%"},
	{ID: "DFF", Label: "Effective fed funds rate", Unit: "%"},
	{ID: "SP500", Label: "S&P 500 index level", Unit: ""},
	{ID: "VIXCLS", Label: "VIX, the market volatility index", Unit: ""},
	// Inflation is what the Fed is steering by, so the brief should have the
	// official figure rather than whichever outlet mentioned it. The core
	// measure leaves out food and energy, the two prices that swing most.
	{ID: "CPIAUCSL", Label: "US consumer price inflation, change from a year earlier (CPI)", Unit: "%", Units: "pc1", Monthly: true},
	{ID: "CPILFESL", Label: "US core inflation, excluding food and energy, change from a year earlier (core CPI)", Unit: "%", Units: "pc1", Monthly: true},
}

// Reading is the latest value of a series and how far it has moved.
type Reading struct {
	Indicator
	Latest   float64
	AsOf     time.Time
	Previous float64
	WeekAgo  float64

	// HasPrevious and HasWeekAgo distinguish "unchanged" from "not known".
	// A series with one observation must not report a move of zero.
	HasPrevious bool
	HasWeekAgo  bool
}

// Change is the move since the previous observation.
func (r Reading) Change() float64 { return r.Latest - r.Previous }

// WeeklyChange is the move over roughly a week of observations.
func (r Reading) WeeklyChange() float64 { return r.Latest - r.WeekAgo }

// FRED reads the St. Louis Fed's data service. An empty APIKey disables it: the key is free but not
// universal, and a brief without market levels is a normal brief.
type FRED struct {
	APIKey string
	HTTP   *http.Client
	URL    string
}

// Enabled reports whether there is a key to use.
func (c *FRED) Enabled() bool { return c != nil && c.APIKey != "" }

// Fetch reads the latest observations for each series.
//
// A failing series is skipped rather than fatal. These are context for the
// macro section, and losing the 2-year is not a reason to lose the brief.
func (c *FRED) Fetch(ctx context.Context, series []Indicator) ([]Reading, []error) {
	if !c.Enabled() {
		return nil, nil
	}

	var (
		out  []Reading
		errs []error
	)
	// Sequential: a handful of small requests, and FRED rate-limits per key.
	for _, s := range series {
		reading, err := c.fetchOne(ctx, s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.ID, err))
			continue
		}
		out = append(out, reading)
	}
	return out, errs
}

type observationsResponse struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

func (c *FRED) fetchOne(ctx context.Context, s Indicator) (Reading, error) {
	params := url.Values{}
	params.Set("series_id", s.ID)
	params.Set("api_key", c.APIKey)
	params.Set("file_type", "json")
	// Newest first, and enough rows to survive the gaps: these are business-day
	// series, and holidays arrive as "." rather than a number.
	params.Set("sort_order", "desc")
	params.Set("limit", "12")
	if s.Units != "" {
		params.Set("units", s.Units)
	}

	var doc observationsResponse
	if err := c.getJSON(ctx, c.baseURL()+"?"+params.Encode(), &doc); err != nil {
		return Reading{}, err
	}

	type point struct {
		value float64
		date  time.Time
	}
	var points []point
	for _, o := range doc.Observations {
		// FRED writes a missing observation as ".", which ParseFloat rejects.
		v, err := strconv.ParseFloat(o.Value, 64)
		if err != nil {
			continue
		}
		d, err := time.Parse("2006-01-02", o.Date)
		if err != nil {
			continue
		}
		points = append(points, point{value: v, date: d})
	}
	if len(points) == 0 {
		return Reading{}, fmt.Errorf("no usable observations")
	}

	r := Reading{Indicator: s, Latest: points[0].value, AsOf: points[0].date}
	if len(points) > 1 {
		r.Previous, r.HasPrevious = points[1].value, true
	}
	if s.Monthly {
		return r, nil // the previous reading is last month's; there is no week
	}
	// Five business days back, or the oldest available if the series is short.
	if idx := 5; len(points) > idx {
		r.WeekAgo, r.HasWeekAgo = points[idx].value, true
	} else if len(points) > 1 {
		r.WeekAgo, r.HasWeekAgo = points[len(points)-1].value, true
	}
	return r, nil
}

func (c *FRED) getJSON(ctx context.Context, target string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

func (c *FRED) baseURL() string {
	if c.URL != "" {
		return c.URL
	}
	return observationsURL
}

func (c *FRED) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
