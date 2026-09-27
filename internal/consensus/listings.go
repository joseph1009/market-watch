package consensus

import (
	"context"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/market"
)

// Nasdaq's stock screener lists every US listing in one request, with what
// it is worth and the industry it is filed under: about seven thousand rows
// and two megabytes. The market's history (internal/market) says how each
// share has traded; this says which company it is, how big, and in what
// line of business, which is what the recommendations need to group them.
const (
	listingsPath    = "screener/stocks?tableonly=true&download=true"
	listingsBody    = 16 << 20
	listingsTimeout = time.Minute
)

// Listings reads the screener.
func (c *Client) Listings(ctx context.Context) ([]market.Listing, error) {
	var doc struct {
		Rows []struct {
			Symbol    string `json:"symbol"`
			Name      string `json:"name"`
			MarketCap num    `json:"marketCap"`
			Country   string `json:"country"`
			Sector    string `json:"sector"`
			Industry  string `json:"industry"`
		} `json:"rows"`
	}
	if err := c.getWith(ctx, listingsPath, &doc, listingsBody, listingsTimeout); err != nil {
		return nil, err
	}
	out := make([]market.Listing, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		// Nasdaq writes a share class with a slash, the market's bars with a
		// dot: BRK/B is BRK.B.
		sym := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(r.Symbol)), "/", ".")
		if sym == "" {
			continue
		}
		out = append(out, market.Listing{
			Symbol:    sym,
			Name:      strings.TrimSpace(r.Name),
			Country:   strings.TrimSpace(r.Country),
			Sector:    strings.TrimSpace(r.Sector),
			Industry:  strings.TrimSpace(r.Industry),
			MarketCap: r.MarketCap.v,
		})
	}
	return out, nil
}
