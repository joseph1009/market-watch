// Package search finds the day's news by asking a search engine, Tavily,
// rather than by polling a list of feed addresses.
//
// It exists because the media half of the feed list is the half that breaks.
// Outlets move their feeds, put them behind bot protection or never offer one
// at all -- Reuters, Bloomberg, Nikkei Asia, the Wall Street Journal and
// Deutsche Welle among them -- and each of those costs an evening of hunting
// for a replacement address. A search for "energy news" does not move.
//
// It does not replace the government and company feeds, and is not meant to.
// A search returns what has been written about a Fed statement or a BLS
// release, hours later and at second hand; the feeds return the document
// itself, and a small contract award or sanctions notice that nobody writes up
// is never found by searching at all. Search and feeds run side by side, and
// the run record says what each contributed (see history.Run).
//
// The results enter the pipeline as ordinary articles, the way SEC filings do:
// deduped against the feeds, matched to watchlists, rated, ranked and cited
// with everything else.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
)

const (
	searchURL = "https://api.tavily.com/search"

	// maxResults is the most one search may return. Every search costs one
	// credit whatever it returns, so each is asked for as many as it can give.
	maxResults = 20

	// defaultConcurrency keeps well inside the rate limit, which is per minute
	// and far above what one brief needs.
	defaultConcurrency = 4

	maxBodyBytes = 4 << 20 // twenty results with snippets are a few tens of KB

	// dayWindow is how far apart two briefs may be and still be served by a
	// search of "the past day". Beyond it, as on a Monday, the search is given
	// the date of the previous brief instead, so the weekend is not skipped.
	dayWindow = 30 * time.Hour
)

// Client runs news searches. An empty APIKey disables it, and a brief without
// search is the brief as it was before search existed.
type Client struct {
	APIKey      string
	HTTP        *http.Client
	Concurrency int

	// URL and UsageURL exist so the tests can point at a stub. Empty means
	// Tavily.
	URL      string
	UsageURL string

	// Now is injected for tests.
	Now func() time.Time
}

// Enabled reports whether there is a key to use.
func (c *Client) Enabled() bool { return c != nil && c.APIKey != "" }

// Result is what one round of searches found and what it cost.
type Result struct {
	Articles []model.Article
	Errors   []error

	// Credits is what the searches drew from the monthly allowance, as Tavily
	// reported it. The free plan is 1,000 a month.
	Credits int
}

// Collect runs every query and returns the articles they found, deduplicated
// by address across queries.
//
// A failing query costs its own results, never the others: one search timing
// out is no reason to lose the rest. Since is where the searches start; it is
// normally the previous brief.
func (c *Client) Collect(ctx context.Context, queries []Query, since time.Time) Result {
	if !c.Enabled() || len(queries) == 0 {
		return Result{}
	}

	type answer struct {
		articles []model.Article
		credits  int
		err      error
	}
	answers := make([]answer, len(queries))

	sem := make(chan struct{}, c.concurrency())
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				answers[i].err = fmt.Errorf("%s: %w", q.Label, ctx.Err())
				return
			}
			arts, credits, err := c.searchOne(ctx, q, since)
			if err != nil {
				err = fmt.Errorf("%s: %w", q.Label, err)
			}
			answers[i] = answer{articles: arts, credits: credits, err: err}
		}()
	}
	wg.Wait()

	// In query order, so a run is reproducible whichever search answers
	// first. The same article found by two searches is kept once: the feed
	// dedupe would fold it anyway, but counting it twice would overstate what
	// search contributed.
	var res Result
	seen := make(map[string]bool)
	for _, a := range answers {
		res.Credits += a.credits
		if a.err != nil {
			res.Errors = append(res.Errors, a.err)
			continue
		}
		for _, art := range a.articles {
			if seen[art.ID] {
				continue
			}
			seen[art.ID] = true
			res.Articles = append(res.Articles, art)
		}
	}
	return res
}

// request is the body Tavily's search endpoint takes. Only the fields used are
// declared; the rest keep their defaults.
type request struct {
	Query          string   `json:"query"`
	Topic          string   `json:"topic"`
	SearchDepth    string   `json:"search_depth"`
	MaxResults     int      `json:"max_results"`
	TimeRange      string   `json:"time_range,omitempty"`
	StartDate      string   `json:"start_date,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	IncludeUsage   bool     `json:"include_usage"`
}

type response struct {
	Results []struct {
		Title     string `json:"title"`
		URL       string `json:"url"`
		Content   string `json:"content"`
		Published string `json:"published_date"`
	} `json:"results"`
	Usage struct {
		Credits int `json:"credits"`
	} `json:"usage"`
}

func (c *Client) searchOne(ctx context.Context, q Query, since time.Time) ([]model.Article, int, error) {
	body := request{
		Query: q.Text,
		// "news" rather than "general" or "finance". General returns quote
		// pages and evergreen explainers; finance, tried on the same query,
		// returned sixteen results of which eleven were Yahoo Finance and five
		// had no date at all.
		Topic: "news",
		// Basic costs one credit; advanced costs two and returns the same
		// kind of snippet, only chosen more carefully.
		SearchDepth: "basic",
		MaxResults:  maxResults,
		// Restricted to named outlets. Unrestricted, a search for chip news
		// returned five results, three of them Facebook and Threads posts;
		// the same search restricted returned twenty from Reuters, the FT,
		// CNBC and Barron's.
		IncludeDomains: Domains(),
		IncludeUsage:   true,
	}
	if gap := c.now().Sub(since); gap > dayWindow {
		body.StartDate = since.UTC().Format("2006-01-02")
	} else {
		body.TimeRange = "day"
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, statusError(resp)
	}
	var doc response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&doc); err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}

	now := c.now()
	out := make([]model.Article, 0, len(doc.Results))
	for _, r := range doc.Results {
		link := strings.TrimSpace(r.URL)
		if link == "" || strings.TrimSpace(r.Title) == "" {
			continue
		}
		published, ok := parseDate(r.Published)
		if !ok {
			// A result with no date is a quote page, a section front or an
			// explainer, not a report of something that happened. The feed
			// fetcher dates an undated item "now"; doing that here would
			// present an evergreen page as today's news.
			continue
		}
		if published.After(now) {
			published = now
		}

		o := outletFor(link)
		title := trimOutlet(strings.TrimSpace(r.Title), o)
		if !isStory(link, title) {
			continue // a section, quote page, live blog or programme (pages.go)
		}
		out = append(out, model.Article{
			ID:         model.ArticleID(link),
			Title:      title,
			URL:        link,
			SourceID:   o.SourceID(),
			SourceName: o.Name,
			Summary:    feed.Summarize(cleanSnippet(r.Content)),
			Published:  published,
			Fetched:    now,
		})
	}
	return out, doc.Usage.Credits, nil
}

const usageURL = "https://api.tavily.com/usage"

// Usage is how much of the month's allowance has gone.
type Usage struct {
	Plan  string
	Used  int
	Limit int
}

// Usage reads the account's credit use. It costs nothing, which makes it the
// way to prove the key works: a search to test it would spend a credit.
//
// The figure itself runs late. On 2026-09-23 it still read 0 after 27
// searches had each reported a credit spent, so it proves the key and gives
// the plan's limit, but the count to trust is Result.Credits, which each
// search reports for itself and the run record keeps.
func (c *Client) Usage(ctx context.Context) (Usage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.usageEndpoint(), nil)
	if err != nil {
		return Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Usage{}, statusError(resp)
	}

	var doc struct {
		Account struct {
			Plan  string `json:"current_plan"`
			Used  int    `json:"plan_usage"`
			Limit int    `json:"plan_limit"`
		} `json:"account"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&doc); err != nil {
		return Usage{}, fmt.Errorf("decode: %w", err)
	}
	return Usage{Plan: doc.Account.Plan, Used: doc.Account.Used, Limit: doc.Account.Limit}, nil
}

// ErrOutOfCredits is the month's allowance running out. It is named because it
// is the one failure that will not fix itself by tomorrow.
var ErrOutOfCredits = errors.New("the monthly search allowance is used up")

// statusError explains a refusal in terms of what to do about it. Tavily uses
// two statuses of its own for running out of credits.
func statusError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("http %s: TAVILY_API_KEY was refused", resp.Status)
	case 432, 433:
		return fmt.Errorf("http %d: %w", resp.StatusCode, ErrOutOfCredits)
	case http.StatusTooManyRequests:
		return fmt.Errorf("http %s: rate limited, retry after %s", resp.Status, resp.Header.Get("Retry-After"))
	default:
		return fmt.Errorf("http %s", resp.Status)
	}
}

// parseDate reads Tavily's published_date, which is written the way HTTP
// headers write dates: "Wed, 23 Sep 2026 12:32:43 GMT".
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// cleanSnippet removes what Tavily's extracted text carries from the page
// around the article: markdown heading marks, and the "[...]" it puts between
// the passages it chose.
func cleanSnippet(s string) string {
	s = strings.ReplaceAll(s, "[...]", "…")
	fields := strings.Fields(s)
	kept := fields[:0]
	for _, f := range fields {
		if strings.Trim(f, "#") == "" {
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

func (c *Client) endpoint() string {
	if c.URL != "" {
		return c.URL
	}
	return searchURL
}

func (c *Client) usageEndpoint() string {
	if c.UsageURL != "" {
		return c.UsageURL
	}
	return usageURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) concurrency() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	return defaultConcurrency
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

// host is the address's host without "www.", lowercased.
func host(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}
