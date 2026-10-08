// Package fundamentals reads what a company has actually reported to the SEC.
//
// The figures come from EDGAR's XBRL API, which serves the tagged values out of
// each filing: the same numbers in the 10-K, not a vendor's reconstruction of
// them and not a model's recollection. That distinction is the whole point.
// A brief can summarize what an article said; an analysis of a balance sheet
// cannot be written from memory without inventing it.
package fundamentals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	conceptURL = "https://data.sec.gov/api/xbrl/companyconcept/CIK%010d/%s/%s.json"

	// SEC publishes a 10 requests/second limit. Two at a time with a pause
	// matches what the filing client settled on after timeouts at four.
	defaultConcurrency = 2
	requestPause       = 120 * time.Millisecond

	// paceGap is the least time between two requests from one client, across
	// every goroutine using it: seven a second, leaving room under the ten for
	// the filing client beside it. The pause above spaces one company's reads;
	// the closer look reads four companies at once, which without this would
	// run at four times the pace and have the SEC block the address.
	paceGap = 143 * time.Millisecond

	maxBodyBytes = 8 << 20
)

// ErrNotReported means the company has never tagged that concept. It is an
// ordinary outcome, not a failure: a software firm reports no inventory, and a
// bank reports no gross profit.
var ErrNotReported = errors.New("concept not reported by this filer")

// ErrNoFigures is a company that files with the SEC but reports none of the
// figures this reads: a fund, say, or a shell company.
var ErrNoFigures = errors.New("files with the SEC but reports no figures this reads")

// Observation is one reported value for one period.
type Observation struct {
	Start  time.Time // zero for a balance-sheet item, which is a point in time
	End    time.Time
	Value  float64
	Unit   string
	Form   string // 10-K, 10-Q, 20-F
	Fiscal string // FY, Q1, Q2, Q3
	Year   int
	Filed  time.Time
}

// Duration reports whether the observation covers a period rather than an
// instant, which is what separates revenue from total assets.
func (o Observation) Duration() bool { return !o.Start.IsZero() }

// Days is the length of the period covered, zero for an instant.
func (o Observation) Days() int {
	if !o.Duration() {
		return 0
	}
	return int(o.End.Sub(o.Start).Hours() / 24)
}

// CIKLookup resolves a ticker to an SEC CIK. The sec.Client satisfies it; the
// interface keeps this package testable without the network.
type CIKLookup interface {
	LookupCIK(ctx context.Context, ticker string) (int, string, error)
}

// Client reads reported figures for one company at a time.
type Client struct {
	Lookup    CIKLookup
	HTTP      *http.Client
	UserAgent string

	// BaseURL overrides the concept endpoint in tests. It takes the same three
	// verbs as conceptURL: CIK, taxonomy, tag.
	BaseURL string

	// FramesURL overrides the frames endpoint in tests. It takes a tag and a
	// calendar year.
	FramesURL string

	// FactsURL overrides the company facts endpoint in tests. It takes a
	// CIK.
	FactsURL string

	// framesMu keeps one frame from being read twice at once when the
	// closer look judges several companies side by side.
	framesMu sync.Mutex

	mu   sync.Mutex
	tags map[int][]TagInfo

	// Unpaced turns the pace off, for tests against a local stub.
	Unpaced bool

	paceMu sync.Mutex
	next   time.Time
}

// pace holds a request until its turn under paceGap.
func (c *Client) pace(ctx context.Context) error {
	if c.Unpaced {
		return nil
	}
	c.paceMu.Lock()
	now := time.Now()
	if c.next.Before(now) {
		c.next = now
	}
	wait := c.next.Sub(now)
	c.next = c.next.Add(paceGap)
	c.paceMu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Concept fetches every observation a company has reported for one tag.
func (c *Client) Concept(ctx context.Context, cik int, taxonomy, tag string) ([]Observation, error) {
	var body struct {
		Units map[string][]struct {
			Start string  `json:"start"`
			End   string  `json:"end"`
			Val   float64 `json:"val"`
			Form  string  `json:"form"`
			FP    string  `json:"fp"`
			FY    int     `json:"fy"`
			Filed string  `json:"filed"`
		} `json:"units"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf(c.baseURL(), cik, taxonomy, tag), &body); err != nil {
		return nil, err
	}

	var out []Observation
	for unit, rows := range body.Units {
		for _, r := range rows {
			end, err := time.Parse(time.DateOnly, r.End)
			if err != nil {
				continue // an unparseable period is not worth guessing at
			}
			o := Observation{
				End:    end,
				Value:  r.Val,
				Unit:   unit,
				Form:   r.Form,
				Fiscal: r.FP,
				Year:   r.FY,
			}
			if r.Start != "" {
				o.Start, _ = time.Parse(time.DateOnly, r.Start)
			}
			o.Filed, _ = time.Parse(time.DateOnly, r.Filed)
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return nil, ErrNotReported
	}

	// Newest period first, and within a period the most recently filed copy
	// first: a restatement supersedes the original, and amendments repeat
	// periods that earlier filings already covered.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].End.Equal(out[j].End) {
			return out[i].End.After(out[j].End)
		}
		return out[i].Filed.After(out[j].Filed)
	})
	return out, nil
}

// First returns the newest observation matching want, which is how a caller
// asks for "the latest annual figure" or "the latest balance".
func First(obs []Observation, want func(Observation) bool) (Observation, bool) {
	for _, o := range obs {
		if want(o) {
			return o, true
		}
	}
	return Observation{}, false
}

// Annual keeps one observation per fiscal year: a full-year period from an
// annual report, deduplicated so a restatement does not appear twice. A year
// read from a results release counts too: it is added only where no annual
// report covers the year yet (release.go).
func Annual(obs []Observation) []Observation {
	const (
		minAnnualDays = 330 // a 52/53-week year runs a few days short of 365
		maxAnnualDays = 400
	)
	seen := make(map[int]bool)
	var out []Observation
	for _, o := range obs {
		switch {
		case !o.Duration():
			continue
		case o.Days() < minAnnualDays || o.Days() > maxAnnualDays:
			continue
		case formRank(o.Form) < 3 && o.Form != releaseForm:
			continue
		case seen[o.End.Year()]:
			continue
		}
		seen[o.End.Year()] = true
		out = append(out, o)
	}
	return out
}

// Quarterly keeps one observation per quarter-length period.
func Quarterly(obs []Observation) []Observation {
	const (
		minQuarterDays = 80
		maxQuarterDays = 100
	)
	seen := make(map[string]bool)
	var out []Observation
	for _, o := range obs {
		if !o.Duration() || o.Days() < minQuarterDays || o.Days() > maxQuarterDays {
			continue
		}
		key := o.End.Format(time.DateOnly)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	return out
}

// Instant keeps one observation per balance-sheet date.
func Instant(obs []Observation) []Observation {
	seen := make(map[string]bool)
	var out []Observation
	for _, o := range obs {
		if o.Duration() {
			continue
		}
		key := o.End.Format(time.DateOnly)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	return out
}

func (c *Client) getJSON(ctx context.Context, url string, into any) error {
	if err := c.pace(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// EDGAR answers Go's default User-Agent with 403 and wants a contact
	// address, exactly as it does for the submissions API.
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// A tag the filer has never used is a 404, and that is information rather
	// than breakage.
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotReported
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(into)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return conceptURL
}

// companyFactsURL lists everything a filer has ever tagged.
const companyFactsURL = "https://data.sec.gov/api/xbrl/companyfacts/CIK%010d.json"

// companyFactsMaxBytes is generous: a large filer's complete facts run to tens
// of megabytes, and this is read once per analysis.
const companyFactsMaxBytes = 96 << 20

// TagInfo is one concept a company actually reports.
type TagInfo struct {
	Taxonomy string
	Tag      string
	Label    string
	Units    []string
}

// Tags lists what this filer has tagged, so a reader can look up what exists
// instead of guessing at tag names. The answer is cached for the process: it is
// a large download, and one analysis asks for it repeatedly.
func (c *Client) Tags(ctx context.Context, cik int) ([]TagInfo, error) {
	c.mu.Lock()
	cached, ok := c.tags[cik]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	var body struct {
		Facts map[string]map[string]struct {
			Label string                     `json:"label"`
			Units map[string]json.RawMessage `json:"units"`
		} `json:"facts"`
	}
	url := companyFactsURL
	if c.FactsURL != "" {
		url = c.FactsURL
	}
	if err := c.getBig(ctx, fmt.Sprintf(url, cik), &body); err != nil {
		return nil, err
	}

	var out []TagInfo
	for taxonomy, tags := range body.Facts {
		for tag, fact := range tags {
			info := TagInfo{Taxonomy: taxonomy, Tag: tag, Label: fact.Label}
			for unit := range fact.Units {
				info.Units = append(info.Units, unit)
			}
			sort.Strings(info.Units)
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Taxonomy != out[j].Taxonomy {
			return out[i].Taxonomy < out[j].Taxonomy
		}
		return out[i].Tag < out[j].Tag
	})

	c.mu.Lock()
	if c.tags == nil {
		c.tags = map[int][]TagInfo{}
	}
	c.tags[cik] = out
	c.mu.Unlock()
	return out, nil
}

// Search returns the tags whose name or label mentions every word of the query,
// which is how "receivable" or "share repurchase" finds the tag to read.
func Search(tags []TagInfo, query string, limit int) []TagInfo {
	words := strings.Fields(strings.ToLower(query))
	var out []TagInfo
	for _, t := range tags {
		haystack := strings.ToLower(t.Tag + " " + t.Label)
		matched := true
		for _, w := range words {
			if !strings.Contains(haystack, w) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, t)
			if limit > 0 && len(out) == limit {
				break
			}
		}
	}
	return out
}

func (c *Client) getBig(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	// Accept-Encoding is left unset so net/http asks for gzip and unpacks it.
	// Set by hand, the SEC's gzip reached the decoder still packed, and
	// find_concepts failed on every company.

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotReported
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, companyFactsMaxBytes)).Decode(into)
}
