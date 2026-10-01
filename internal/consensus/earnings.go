package consensus

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// earningsPath is Nasdaq's earnings calendar for one day: every company
// due to report, when, and what analysts expect it to earn a share.
const earningsPath = "calendar/earnings?date="

// Earnings reads the companies due to report on day, largest first as
// Nasdaq lists them. A day with none is an empty list, not an error.
func (c *Client) Earnings(ctx context.Context, day time.Time) ([]model.Results, error) {
	var doc struct {
		Rows []struct {
			Time        string `json:"time"`
			Symbol      string `json:"symbol"`
			Name        string `json:"name"`
			MarketCap   num    `json:"marketCap"`
			EPSForecast string `json:"epsForecast"`
			LastYearEPS string `json:"lastYearEPS"`
			Estimates   string `json:"noOfEsts"`
		} `json:"rows"`
	}
	err := c.get(ctx, earningsPath+day.Format(time.DateOnly), &doc)
	if err == ErrNotCovered {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []model.Results
	seen := map[string]bool{}
	for _, r := range doc.Rows {
		sym := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(r.Symbol)), "/", ".")
		// A second line for the same company, such as another share class,
		// is the same results: the first, which Nasdaq lists with the
		// forecast, stands for it.
		name := strings.TrimSpace(r.Name)
		if sym == "" || seen[name] {
			continue
		}
		seen[name] = true
		n, _ := strconv.Atoi(strings.TrimSpace(r.Estimates))
		out = append(out, model.Results{
			Day:       day,
			Symbol:    sym,
			Name:      name,
			When:      reportTime(r.Time),
			MarketCap: r.MarketCap.v,
			Forecast:  strings.TrimSpace(r.EPSForecast),
			LastYear:  strings.TrimSpace(r.LastYearEPS),
			Estimates: n,
		})
	}
	return out, nil
}

// reportTime reads Nasdaq's "time-pre-market" and "time-after-hours".
func reportTime(s string) string {
	switch strings.TrimSpace(s) {
	case "time-pre-market":
		return "before the open"
	case "time-after-hours":
		return "after the close"
	}
	return ""
}
