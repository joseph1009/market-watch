package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Company news comes from the same vendor as the quotes, on the same key.
//
// The accounts say what a company earned in a period that has already closed.
// They cannot say that its largest customer changed supplier last month, that a
// plant burned down, or that the chief executive resigned on Tuesday -- and any
// of those matters more to what happens next than another year of margins. This
// is that missing half, with the standing caveat that it is reported claims
// rather than filed facts, which the analysis is told to say out loud.
const (
	companyNewsURL = "https://finnhub.io/api/v1/company-news"

	// newsWindow is how far back to read. Long enough to cover a quarterly
	// results announcement and the reaction to it, short enough that "recent"
	// stays honest.
	newsWindow = 45 * 24 * time.Hour
)

// News reads what has been written about a company lately.
type News struct {
	APIKey string
	HTTP   *http.Client
	URL    string
}

// Enabled reports whether news can be read at all.
func (n *News) Enabled() bool { return n != nil && n.APIKey != "" }

// Company reads the headlines carrying a symbol, newest first.
//
// The vendor's tagging is loose: a piece tagged NVDA may be about Intel and
// mention NVIDIA in its third paragraph. Nothing here tries to fix that -- the
// caller knows the company's name and filters on it, since relevance is a
// judgment about this company and not about the feed.
func (n *News) Company(ctx context.Context, symbol string, now time.Time) ([]model.Article, error) {
	if !n.Enabled() {
		return nil, fmt.Errorf("no news key")
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("no symbol")
	}

	url := fmt.Sprintf("%s?symbol=%s&from=%s&to=%s&token=%s",
		n.url(), symbol,
		now.Add(-newsWindow).Format(time.DateOnly),
		now.Format(time.DateOnly),
		n.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := n.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", symbol, resp.Status)
	}

	var items []struct {
		Datetime int64  `json:"datetime"`
		Headline string `json:"headline"`
		ID       int64  `json:"id"`
		Source   string `json:"source"`
		Summary  string `json:"summary"`
		URL      string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&items); err != nil {
		return nil, fmt.Errorf("%s: %w", symbol, err)
	}

	out := make([]model.Article, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.Headline) == "" {
			continue
		}
		out = append(out, model.Article{
			ID:         fmt.Sprintf("finnhub-%d", it.ID),
			Title:      strings.TrimSpace(it.Headline),
			URL:        it.URL,
			SourceName: strings.TrimSpace(it.Source),
			Summary:    strings.TrimSpace(it.Summary),
			Published:  time.Unix(it.Datetime, 0).UTC(),
			Tickers:    []string{symbol},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	return out, nil
}

func (n *News) url() string {
	if n.URL != "" {
		return n.URL
	}
	return companyNewsURL
}

func (n *News) client() *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	return http.DefaultClient
}
