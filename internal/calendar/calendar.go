// Package calendar reads the week's scheduled economic releases from
// ForexFactory's calendar, through the weekly export the site publishes for
// that purpose: every release and central-bank speech, the currency it
// moves, how much the market expects it to matter, and the forecast and the
// previous figure where there are ones.
//
// It is what lets the brief say what is coming before it lands. On 30
// September 2026 the brief named the PCE inflation release and Micron's
// results only as "the day's tests", with no forecast and no time; this is
// where the forecast and the time come from.
package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// weekURL is the current week's calendar, Sunday to Saturday, with times in
// New York's. The site asks that it be read sparingly; once a brief is that.
const weekURL = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"

// maxBody bounds the reply: a week is about twenty kilobytes.
const maxBody = 2 << 20

// ForexFactory reads the calendar.
type ForexFactory struct {
	HTTP *http.Client
	URL  string // the export; weekURL when empty
}

// Week reads every release in the current week.
func (f *ForexFactory) Week(ctx context.Context) ([]model.Release, error) {
	url := f.URL
	if url == "" {
		url = weekURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; market-watch)")
	req.Header.Set("Accept", "application/json")
	client := f.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("forexfactory calendar: %s", resp.Status)
	}
	var rows []struct {
		Title    string `json:"title"`
		Country  string `json:"country"`
		Date     string `json:"date"`
		Impact   string `json:"impact"`
		Forecast string `json:"forecast"`
		Previous string `json:"previous"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("forexfactory calendar: %w", err)
	}
	out := make([]model.Release, 0, len(rows))
	for _, r := range rows {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(r.Date))
		if err != nil {
			continue
		}
		out = append(out, model.Release{
			At:       at,
			Country:  strings.ToUpper(strings.TrimSpace(r.Country)),
			Title:    html.UnescapeString(strings.TrimSpace(r.Title)),
			Impact:   strings.TrimSpace(r.Impact),
			Forecast: html.UnescapeString(strings.TrimSpace(r.Forecast)),
			Previous: html.UnescapeString(strings.TrimSpace(r.Previous)),
		})
	}
	return out, nil
}

// watched is which releases matter to a reader of the US market: every
// American one the calendar rates high or medium, and the high ones of the
// other large economies, whose rates and data move the dollar and the
// shares that sell abroad.
func watched(r model.Release) bool {
	switch r.Country {
	case "USD":
		return r.Impact == "High" || r.Impact == "Medium"
	case "EUR", "GBP", "JPY", "CNY":
		return r.Impact == "High"
	}
	return false
}

// Key is the releases that matter, due from from up to until, soonest
// first.
func Key(all []model.Release, from, until time.Time) []model.Release {
	var out []model.Release
	for _, r := range all {
		if watched(r) && !r.At.Before(from) && r.At.Before(until) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
