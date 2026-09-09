package feed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

const (
	// DefaultUserAgent is the fallback identity, and deliberately carries no
	// contact address: this constant is committed to a public repository.
	//
	// It is not sufficient for every source. SEC EDGAR requires a User-Agent of
	// the literal form "Name email@domain" -- it answers Go's default, a
	// browser string, and a "(+url)" form alike with 403 -- so set USER_AGENT in
	// the environment to something like "Market Watch you@example.com". Every
	// other default source accepts this string as-is.
	DefaultUserAgent = "market-watch/0.1 (+https://github.com/joseph1009/market-watch)"

	// defaultConcurrency is deliberately modest: the source list is short and
	// the run has hours of slack before delivery, so there is nothing to gain
	// from hammering publishers.
	defaultConcurrency = 6

	// maxFeedBytes bounds one response. The largest default feed is a few
	// hundred KB; this only exists so a misbehaving source cannot exhaust
	// memory on a small container.
	maxFeedBytes = 8 << 20
)

// Fetcher retrieves and normalizes feeds. The zero value is usable.
type Fetcher struct {
	Client      *http.Client
	UserAgent   string
	Concurrency int

	// Now supplies the fetch timestamp, and the fallback publish time for items
	// whose own date is missing or unparseable. Injected for tests.
	Now func() time.Time
}

// SourceError records one feed that failed. Collection continues around it:
// a dead source must cost its own stories, not the whole report.
type SourceError struct {
	SourceID   string
	SourceName string
	Err        error
}

func (e SourceError) Error() string { return fmt.Sprintf("%s: %v", e.SourceName, e.Err) }
func (e SourceError) Unwrap() error { return e.Err }

// Fetch pulls every source concurrently and returns the articles in source
// order, so a run is reproducible regardless of which feed answers first.
func (f *Fetcher) Fetch(ctx context.Context, sources []model.Source) ([]model.Article, []SourceError) {
	type result struct {
		articles []model.Article
		err      error
	}
	results := make([]result, len(sources))

	sem := make(chan struct{}, f.concurrency())
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			results[i].articles, results[i].err = f.fetchOne(ctx, s)
		}()
	}
	wg.Wait()

	var (
		articles []model.Article
		errs     []SourceError
	)
	for i, r := range results {
		if r.err != nil {
			errs = append(errs, SourceError{SourceID: sources[i].ID, SourceName: sources[i].Name, Err: r.err})
			continue
		}
		articles = append(articles, r.articles...)
	}
	return articles, errs
}

func (f *Fetcher) fetchOne(ctx context.Context, s model.Source) ([]model.Article, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent())
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml;q=0.9, */*;q=0.8")
	// Accept-Encoding is left unset so net/http negotiates gzip transparently.

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %s", resp.Status)
	}

	items, err := Parse(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return nil, err
	}
	return f.normalize(s, items), nil
}

// normalize turns parsed items into articles, dropping the ones that carry
// nothing usable and resolving links against the feed's own address.
func (f *Fetcher) normalize(s model.Source, items []Item) []model.Article {
	now := f.now()
	base, _ := url.Parse(s.URL) // a source URL that will not parse simply skips resolution

	out := make([]model.Article, 0, len(items))
	for _, it := range items {
		link := resolve(base, it.URL)
		if it.Title == "" || link == "" {
			continue
		}

		published := it.Published
		// Missing dates, and the future dates that clock-skewed publishers emit,
		// both become "now" so ordering by recency stays meaningful.
		if published.IsZero() || published.After(now) {
			published = now
		}

		out = append(out, model.Article{
			ID:         model.ArticleID(link),
			Title:      it.Title,
			URL:        link,
			SourceID:   s.ID,
			SourceName: s.Name,
			Summary:    it.Summary,
			Published:  published,
			Fetched:    now,
		})
	}
	return out
}

func resolve(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || base == nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(ref).String()
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

func (f *Fetcher) userAgent() string {
	if strings.TrimSpace(f.UserAgent) != "" {
		return f.UserAgent
	}
	return DefaultUserAgent
}

func (f *Fetcher) concurrency() int {
	if f.Concurrency > 0 {
		return f.Concurrency
	}
	return defaultConcurrency
}

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}
