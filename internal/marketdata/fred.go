// Package marketdata reads levels from FRED so the brief can say where rates
// actually are, not only what outlets said about them.
//
// These are observations, not articles: a series of dated numbers with no
// headline, nothing to deduplicate against and no watchlist to match. They are
// therefore kept out of the article pipeline entirely and handed to the
// summarizer as their own block. Synthesizing a headline ("10-year yield rose
// 8bp") would fit the existing machinery, but it would be manufacturing a
// sentence nobody wrote and pricing it as though a source had said it.
package marketdata

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

// Series is one indicator worth putting in front of the summarizer.
type Series struct {
	ID    string
	Label string
	Unit  string
}

// DefaultSeries are the levels the Macro & Rates section is written against.
var DefaultSeries = []Series{
	{ID: "DGS10", Label: "US 10-year Treasury yield", Unit: "%"},
	{ID: "DGS2", Label: "US 2-year Treasury yield", Unit: "%"},
	{ID: "T10Y2Y", Label: "2s10s curve", Unit: "%"},
	{ID: "DFF", Label: "Effective fed funds rate", Unit: "%"},
}

// Reading is the latest value of a series and how far it has moved.
type Reading struct {
	Series
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

// Client reads FRED. An empty APIKey disables it: the key is free but not
// universal, and a brief without market levels is a normal brief.
type Client struct {
	APIKey string
	HTTP   *http.Client
	URL    string
}

// Enabled reports whether there is a key to use.
func (c *Client) Enabled() bool { return c != nil && c.APIKey != "" }

// Fetch reads the latest observations for each series.
//
// A failing series is skipped rather than fatal. These are context for the
// macro section, and losing the 2-year is not a reason to lose the brief.
func (c *Client) Fetch(ctx context.Context, series []Series) ([]Reading, []error) {
	if !c.Enabled() {
		return nil, nil
	}

	var (
		out  []Reading
		errs []error
	)
	// Sequential: four small requests, and FRED rate-limits per key.
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

func (c *Client) fetchOne(ctx context.Context, s Series) (Reading, error) {
	params := url.Values{}
	params.Set("series_id", s.ID)
	params.Set("api_key", c.APIKey)
	params.Set("file_type", "json")
	// Newest first, and enough rows to survive the gaps: these are business-day
	// series, and holidays arrive as "." rather than a number.
	params.Set("sort_order", "desc")
	params.Set("limit", "12")

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

	r := Reading{Series: s, Latest: points[0].value, AsOf: points[0].date}
	if len(points) > 1 {
		r.Previous, r.HasPrevious = points[1].value, true
	}
	// Five business days back, or the oldest available if the series is short.
	if idx := 5; len(points) > idx {
		r.WeekAgo, r.HasWeekAgo = points[idx].value, true
	} else if len(points) > 1 {
		r.WeekAgo, r.HasWeekAgo = points[len(points)-1].value, true
	}
	return r, nil
}

func (c *Client) getJSON(ctx context.Context, target string, into any) error {
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

func (c *Client) baseURL() string {
	if c.URL != "" {
		return c.URL
	}
	return observationsURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
