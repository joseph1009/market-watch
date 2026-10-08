// Package sec turns SEC filings into articles, using the submissions API
// rather than an RSS feed.
//
// The RSS route was tried and abandoned. EDGAR's feeds title an entry with the
// form type and nothing else -- literally "8-K - Current report" -- so the
// summarizer receives a label with no content and writes filler around it. The
// item codes that say what a filing is about live in the submissions JSON, not
// in any feed, which is why this is an API client and not another source URL.
package sec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

const (
	tickerIndexURL = "https://www.sec.gov/files/company_tickers.json"
	submissionsURL = "https://data.sec.gov/submissions/CIK%010d.json"

	// SEC publishes a 10 requests/second limit and enforces it: per-company
	// requests timed out at concurrency 4 and succeeded at 1. Two with a pause
	// between them stays well inside the limit and still finishes in seconds.
	defaultConcurrency = 2
	requestPause       = 120 * time.Millisecond

	maxBodyBytes = 32 << 20 // a large issuer's submissions file is a few MB
)

// materialItems are the 8-K item codes worth a reader's attention, with the
// plain-English name that goes into the headline.
//
// The exclusions matter as much as the inclusions. 7.01 (Regulation FD) and
// 8.01 (Other Events) cover most 8-K volume and say nothing about what
// happened -- they are what produced "Analog Devices also filed a Reg FD 8-K
// the same day" in an earlier brief, a line that cost space and told the reader
// nothing.
var materialItems = map[string]string{
	"1.01": "entered a material agreement",
	"1.03": "filed for bankruptcy or receivership",
	"2.01": "completed an acquisition or disposition",
	"2.02": "reported results",
	"2.05": "announced restructuring costs",
	"2.06": "recorded a material impairment",
	"4.02": "said previously issued financials should not be relied upon",
	"5.02": "announced a change of directors or officers",
}

// Filing is one 8-K worth reporting.
type Filing struct {
	Ticker    string
	Company   string
	Items     []string
	Filed     time.Time
	URL       string
	Accession string

	// Exhibits is what a foreign filer's 6-K says it holds, by the titles on
	// its cover page, since a 6-K has no item codes to describe it by.
	Exhibits []string
}

// Client reads the SEC submissions API.
type Client struct {
	HTTP        *http.Client
	UserAgent   string
	Concurrency int

	// BaseURL fields exist so the tests can point at a stub. Empty means the
	// real SEC endpoints.
	TickerIndexURL string
	SubmissionsURL string
	ArchiveURL     string

	// tickers caches the ticker-to-CIK index for the process lifetime. It is a
	// ~1MB file that changes rarely, and refetching it per run would be the
	// largest request we make. A failure is not kept that way: SEC answered
	// one request with a 404 on 8 October 2026, and the index, cached as
	// failed, left that day's brief without filings and every /analyse
	// refused until a restart. It is tried again once indexRetry has passed.
	mu       sync.Mutex
	tickers  map[string]company
	failed   error
	failedAt time.Time
}

// indexRetry is how long a failed load of the ticker index stands before the
// next request tries again: long enough not to hammer SEC while it is down.
const indexRetry = 2 * time.Minute

type company struct {
	CIK  int
	Name string

	// Main is whether this is the company's first ticker in the index, which
	// lists each company's own shares before its other lines: BMO before the
	// exchange-traded notes the bank issues, GOOGL before GOOG.
	Main bool
}

// Collect returns articles for the material filings of the given tickers.
//
// Errors are returned alongside the articles rather than instead of them: one
// company's submissions being unavailable is not a reason to lose the rest.
func (c *Client) Collect(ctx context.Context, tickers []string, since time.Time) ([]model.Article, []error) {
	index, err := c.tickerIndex(ctx)
	if err != nil {
		return nil, []error{err}
	}

	// Resolve first so unknown tickers are reported once, not chased over the
	// network. A watchlist naming a symbol SEC does not list -- a foreign
	// issuer, or a typo -- is worth knowing about.
	type target struct {
		ticker string
		co     company
	}
	var targets []target
	var errs []error
	for _, t := range tickers {
		co, ok := index[strings.ToUpper(t)]
		if !ok {
			continue // not a US registrant; silence rather than noise
		}
		targets = append(targets, target{ticker: strings.ToUpper(t), co: co})
	}

	results := make([][]model.Article, len(targets))
	failures := make([]error, len(targets))

	sem := make(chan struct{}, c.concurrency())
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				failures[i] = ctx.Err()
				return
			}

			filings, err := c.recent(ctx, tgt.co, since)
			if err != nil {
				failures[i] = fmt.Errorf("%s: %w", tgt.ticker, err)
				return
			}
			for _, f := range filings {
				f.Ticker = tgt.ticker
				results[i] = append(results[i], f.article())
			}
		}()
	}
	wg.Wait()

	var articles []model.Article
	for i := range targets {
		if failures[i] != nil {
			errs = append(errs, failures[i])
			continue
		}
		articles = append(articles, results[i]...)
	}
	return articles, errs
}

// article renders a filing as something the summarizer can read. The headline
// states what happened, which is the whole reason for going past the RSS feed.
func (f Filing) article() model.Article {
	var what []string
	for _, item := range f.Items {
		if name, ok := materialItems[item]; ok {
			what = append(what, name)
		}
	}

	title := fmt.Sprintf("%s (%s) %s", f.Company, f.Ticker, strings.Join(what, "; "))
	summary := fmt.Sprintf("SEC Form 8-K filed %s, item %s.",
		f.Filed.Format("2 January 2006"), strings.Join(f.Items, ", "))

	return model.Article{
		ID:         model.ArticleID(f.URL),
		Title:      title,
		URL:        f.URL,
		SourceID:   SourceID,
		SourceName: SourceName,
		Summary:    summary,
		Published:  f.Filed,
		Fetched:    time.Now().UTC(),
		Tickers:    []string{f.Ticker},
	}
}

// SourceID and SourceName identify these articles in the pipeline. They are
// weighted like a filing rather than like news, which is what they are.
const (
	SourceID     = "sec-filings"
	SourceName   = "SEC Filings"
	SourceWeight = 10
)

type submissions struct {
	Name    string `json:"name"`
	Filings struct {
		Recent struct {
			AccessionNumber []string `json:"accessionNumber"`
			FilingDate      []string `json:"filingDate"`
			Form            []string `json:"form"`
			Items           []string `json:"items"`
			PrimaryDocument []string `json:"primaryDocument"`
		} `json:"recent"`
	} `json:"filings"`
}

func (c *Client) recent(ctx context.Context, co company, since time.Time) ([]Filing, error) {
	var doc submissions
	if err := c.getJSON(ctx, fmt.Sprintf(c.submissionsURL(), co.CIK), &doc); err != nil {
		return nil, err
	}
	return materialFilings(doc, co, since)
}

// materialFilings picks a company's material 8-Ks since a date out of its
// submissions, newest first.
func materialFilings(doc submissions, co company, since time.Time) ([]Filing, error) {
	r := doc.Filings.Recent
	n := len(r.Form)
	// The arrays are parallel by contract. A short one means the shape changed,
	// and reading past it would pair a form with another filing's date.
	if len(r.FilingDate) < n || len(r.AccessionNumber) < n || len(r.Items) < n {
		return nil, fmt.Errorf("submissions arrays are ragged for CIK %d", co.CIK)
	}

	var out []Filing
	for i := 0; i < n; i++ {
		if r.Form[i] != "8-K" {
			continue
		}
		filed, err := time.Parse("2006-01-02", r.FilingDate[i])
		if err != nil || filed.Before(since) {
			continue
		}

		items := materialCodes(r.Items[i])
		if len(items) == 0 {
			continue // routine disclosure with no market read
		}

		primary := ""
		if i < len(r.PrimaryDocument) {
			primary = r.PrimaryDocument[i]
		}
		out = append(out, Filing{
			Company:   doc.Name,
			Items:     items,
			Filed:     filed,
			Accession: r.AccessionNumber[i],
			URL:       filingURL(co.CIK, r.AccessionNumber[i], primary),
		})
	}
	return out, nil
}

// materialCodes keeps only the item codes worth reporting. The field is a
// comma-separated list, and a single filing routinely carries several.
func materialCodes(raw string) []string {
	var out []string
	for _, code := range strings.Split(raw, ",") {
		code = strings.TrimSpace(code)
		if _, ok := materialItems[code]; ok {
			out = append(out, code)
		}
	}
	return out
}

func filingURL(cik int, accession, primary string) string {
	bare := strings.ReplaceAll(accession, "-", "")
	if primary == "" {
		return fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%d/%s/", cik, bare)
	}
	return fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%d/%s/%s", cik, bare, primary)
}

// tickerIndex maps symbols to CIKs, fetched once per process once it loads.
func (c *Client) tickerIndex(ctx context.Context) (map[string]company, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tickers != nil {
		return c.tickers, nil
	}
	if c.failed != nil && time.Since(c.failedAt) < indexRetry {
		return nil, c.failed
	}

	// The file is a JSON object keyed by row number, not an array.
	var raw map[string]struct {
		CIK    int    `json:"cik_str"`
		Ticker string `json:"ticker"`
		Title  string `json:"title"`
	}
	if err := c.getJSON(ctx, c.tickerIndexURL(), &raw); err != nil {
		c.failed, c.failedAt = fmt.Errorf("load SEC ticker index: %w", err), time.Now()
		return nil, c.failed
	}
	c.failed = nil

	// In row order, so the first ticker seen for a company is its main one.
	keys := make([]int, 0, len(raw))
	for k := range raw {
		if n, err := strconv.Atoi(k); err == nil {
			keys = append(keys, n)
		}
	}
	sort.Ints(keys)
	tickers := make(map[string]company, len(raw))
	seen := make(map[int]bool, len(raw))
	for _, k := range keys {
		row := raw[strconv.Itoa(k)]
		tickers[strings.ToUpper(row.Ticker)] = company{CIK: row.CIK, Name: row.Title, Main: !seen[row.CIK]}
		seen[row.CIK] = true
	}
	c.tickers = tickers
	return c.tickers, nil
}

func (c *Client) getJSON(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// SEC answers Go's default User-Agent with 403 and requires a contact
	// address; the same string the feed fetcher uses applies here.
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}

	// Pacing lives here rather than at the call site so every request through
	// this client is spaced, including the index fetch.
	select {
	case <-time.After(requestPause):
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
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

func (c *Client) tickerIndexURL() string {
	if c.TickerIndexURL != "" {
		return c.TickerIndexURL
	}
	return tickerIndexURL
}

func (c *Client) submissionsURL() string {
	if c.SubmissionsURL != "" {
		return c.SubmissionsURL
	}
	return submissionsURL
}

// ErrNoFiler is a ticker the SEC's index does not have: a foreign listing, a
// private company or a typo. Any other error from a lookup means the index
// could not be read.
var ErrNoFiler = errors.New("no SEC filer")

// LookupCIK resolves a ticker to the company the SEC knows it as, using the
// same index the filing collector loads. Exported because the fundamentals
// reader needs the CIK to read a company's reported figures, and there is no
// second authoritative mapping worth maintaining.
func (c *Client) LookupCIK(ctx context.Context, ticker string) (cik int, name string, err error) {
	index, err := c.tickerIndex(ctx)
	if err != nil {
		return 0, "", err
	}
	co, ok := index[strings.ToUpper(strings.TrimSpace(ticker))]
	if !ok {
		return 0, "", fmt.Errorf("%w for ticker %q", ErrNoFiler, ticker)
	}
	return co.CIK, co.Name, nil
}

// MainTicker reports whether a ticker is its company's own main listing,
// rather than a second share class or a note the company issues. A bank's
// leveraged notes are filed under the bank, and without this a note tracking
// gold miners at three times the move reads as the bank moving.
func (c *Client) MainTicker(ctx context.Context, ticker string) bool {
	index, err := c.tickerIndex(ctx)
	if err != nil {
		return false
	}
	co, ok := index[strings.ToUpper(strings.TrimSpace(ticker))]
	return ok && co.Main
}

// Recent returns what a company has announced to the SEC since a date, newest
// first: its material 8-Ks, and a foreign filer's 6-Ks less the routine ones.
// Exported so an analysis can say what the company has told the SEC lately,
// which is the nearest thing to company news that a filings API holds.
func (c *Client) Recent(ctx context.Context, ticker string, since time.Time) ([]Filing, error) {
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
	filings, err := materialFilings(doc, co, since)
	if err != nil {
		return nil, err
	}
	announced, err := c.announcements(ctx, doc, co, since)
	if err != nil {
		return nil, err
	}
	filings = append(filings, announced...)
	sort.SliceStable(filings, func(i, j int) bool { return filings[i].Filed.After(filings[j].Filed) })

	for i := range filings {
		filings[i].Ticker = strings.ToUpper(ticker)
	}
	return filings, nil
}

// Describe names an 8-K's item codes in plain English, which is what makes a
// filing readable to anyone who does not know the codes by heart. A 6-K has
// no codes, and is described by the titles on its cover instead.
func (f Filing) Describe() string {
	if len(f.Exhibits) > 0 {
		return strings.Join(f.Exhibits, "; ")
	}
	var out []string
	for _, code := range f.Items {
		if plain, ok := materialItems[code]; ok {
			out = append(out, plain)
		}
	}
	return strings.Join(out, "; ")
}

// AnnualReport finds the most recent annual filing and returns the URL of its
// primary document, so its business description can be read.
func (c *Client) AnnualReport(ctx context.Context, ticker string) (Filing, error) {
	index, err := c.tickerIndex(ctx)
	if err != nil {
		return Filing{}, err
	}
	co, ok := index[strings.ToUpper(strings.TrimSpace(ticker))]
	if !ok {
		return Filing{}, fmt.Errorf("%w for ticker %q", ErrNoFiler, ticker)
	}

	var doc submissions
	if err := c.getJSON(ctx, fmt.Sprintf(c.submissionsURL(), co.CIK), &doc); err != nil {
		return Filing{}, err
	}

	r := doc.Filings.Recent
	n := len(r.Form)
	if len(r.FilingDate) < n || len(r.AccessionNumber) < n {
		return Filing{}, fmt.Errorf("submissions arrays are ragged for CIK %d", co.CIK)
	}

	// The arrays are newest first, so the first annual form is the current one.
	// 20-F and 40-F count: a foreign filer's annual report carries the same
	// business description as a 10-K.
	for i := 0; i < n; i++ {
		form := r.Form[i]
		if form != "10-K" && form != "20-F" && form != "40-F" {
			continue
		}
		primary := ""
		if i < len(r.PrimaryDocument) {
			primary = r.PrimaryDocument[i]
		}
		filed, _ := time.Parse("2006-01-02", r.FilingDate[i])
		return Filing{
			Ticker:    strings.ToUpper(ticker),
			Company:   doc.Name,
			Items:     []string{form},
			Filed:     filed,
			Accession: r.AccessionNumber[i],
			URL:       filingURL(co.CIK, r.AccessionNumber[i], primary),
		}, nil
	}
	return Filing{}, fmt.Errorf("%s has filed no annual report this API lists", strings.ToUpper(ticker))
}

// businessDocMaxBytes bounds the download. A large annual report runs to
// several megabytes of HTML, nearly all of it financial statements this never
// reads.
const businessDocMaxBytes = 24 << 20

// BusinessSection fetches an annual report and returns the part that describes
// what the company does.
//
// Item 1 is where a filer says what it sells and to whom, in its own words and
// under a legal obligation to be accurate. Nothing else available here answers
// "what is this company", and an analysis of the accounts without it is a
// column of numbers about an unnamed business.
func (c *Client) BusinessSection(ctx context.Context, url string, maxRunes int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, businessDocMaxBytes))
	if err != nil {
		return "", err
	}
	return businessText(string(raw), maxRunes), nil
}
