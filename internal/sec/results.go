package sec

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ResultsDates are the days a US filer announced its results since a time,
// newest first: the filing dates of its 8-Ks under Item 2.02. A verdict sets
// today's news against how the share took the news before it, and results
// are the news every company has, on a known day, every quarter. A foreign
// filer's 6-Ks carry no item codes, so it has none here.
func (c *Client) ResultsDates(ctx context.Context, ticker string, since time.Time) ([]time.Time, error) {
	index, err := c.tickerIndex(ctx)
	if err != nil {
		return nil, err
	}
	co, ok := index[strings.ToUpper(strings.TrimSpace(ticker))]
	if !ok {
		return nil, fmt.Errorf("%w for ticker %q", ErrNoFiler, ticker)
	}
	var doc submissions
	if err := c.getJSON(ctx, fmt.Sprintf(c.submissionsURL(), co.CIK), &doc); err != nil {
		return nil, err
	}
	return resultsDates(doc, since), nil
}

// resultsDates reads the 8-Ks under Item 2.02 out of a submissions document,
// newest first, one a day: a company that files its release and a correction
// on one day announced once.
func resultsDates(doc submissions, since time.Time) []time.Time {
	r := doc.Filings.Recent
	var out []time.Time
	for i := range r.Form {
		if r.Form[i] != "8-K" || i >= len(r.FilingDate) || i >= len(r.Items) || !hasItem(r.Items[i], "2.02") {
			continue
		}
		filed, err := time.Parse(time.DateOnly, r.FilingDate[i])
		if err != nil {
			continue
		}
		if filed.Before(since) {
			break // newest first
		}
		if n := len(out); n > 0 && out[n-1].Equal(filed) {
			continue
		}
		out = append(out, filed)
	}
	return out
}
