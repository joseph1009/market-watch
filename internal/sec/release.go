package sec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// defaultArchiveURL is where a filing's documents live.
const defaultArchiveURL = "https://www.sec.gov/Archives/edgar/data"

// releaseDocMaxBytes bounds the download. A results release with its tables
// runs to a few hundred kilobytes of HTML.
const releaseDocMaxBytes = 8 << 20

// A filing's index page lists each document with the exhibit type it was
// filed as. The type is what identifies the press release: its file name is
// whatever the filer chose -- a2026q3ex991-pressrelease.htm at Micron,
// q2fy27pr.htm at Nvidia, a2q26erfexhibit991narrative.htm at JPMorgan.
var (
	indexRow  = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	rowHref   = regexp.MustCompile(`(?i)href="([^"]+\.html?)"`)
	release99 = regexp.MustCompile(`(?i)>\s*EX-99(\.\d+)?\s*<`)
)

// EarningsRelease finds the company's latest results announcement -- the
// press release filed with an 8-K under Item 2.02, or with a 6-K whose cover
// lists results, since the given time -- and returns it as text, cut to
// maxRunes.
//
// The accounts say what was filed; the release says what the company chose
// to lead with, and is usually where its outlook for the next quarter, the
// business measures behind the totals -- bits shipped, subscribers, backlog --
// and the figures it adjusts are published. None of that reaches the XBRL
// the accounts are read from.
func (c *Client) EarningsRelease(ctx context.Context, ticker string, since time.Time, maxRunes int) (Filing, string, error) {
	index, err := c.tickerIndex(ctx)
	if err != nil {
		return Filing{}, "", err
	}
	co, ok := index[strings.ToUpper(strings.TrimSpace(ticker))]
	if !ok {
		return Filing{}, "", fmt.Errorf("%w for ticker %q", ErrNoFiler, ticker)
	}

	var doc submissions
	if err := c.getJSON(ctx, fmt.Sprintf(c.submissionsURL(), co.CIK), &doc); err != nil {
		return Filing{}, "", err
	}
	r := doc.Filings.Recent
	n := len(r.Form)
	if len(r.FilingDate) < n || len(r.AccessionNumber) < n || len(r.Items) < n {
		return Filing{}, "", fmt.Errorf("submissions arrays are ragged for CIK %d", co.CIK)
	}

	covers := 0
	for i := 0; i < n; i++ {
		form := r.Form[i]
		if form != "8-K" && form != "6-K" {
			continue
		}
		filed, err := time.Parse("2006-01-02", r.FilingDate[i])
		if err != nil {
			continue
		}
		if filed.Before(since) {
			break // newest first: every later one is older still
		}
		filing := Filing{
			Ticker: strings.ToUpper(ticker), Company: doc.Name,
			Filed: filed, Accession: r.AccessionNumber[i],
		}

		exhibit := "99.1"
		if form == "8-K" {
			if !hasItem(r.Items[i], "2.02") {
				continue
			}
			filing.Items = []string{"2.02"}
		} else {
			if covers == maxCovers {
				break
			}
			covers++
			primary := ""
			if i < len(r.PrimaryDocument) {
				primary = r.PrimaryDocument[i]
			}
			exhibits, err := c.coverExhibits(ctx, co.CIK, r.AccessionNumber[i], primary)
			if err != nil {
				return filing, "", err
			}
			found, ok := resultsExhibit(exhibits)
			if !ok {
				continue
			}
			exhibit = found.number
		}

		url, err := c.releaseURL(ctx, co.CIK, r.AccessionNumber[i], exhibit)
		if err != nil {
			return filing, "", err
		}
		filing.URL = url
		text, err := c.document(ctx, url)
		if err != nil {
			return filing, "", err
		}
		return filing, ClipRunes(plainText(text), maxRunes), nil
	}
	return Filing{}, "", fmt.Errorf("%s has filed no results since %s", strings.ToUpper(ticker), since.Format("2 Jan 2006"))
}

func hasItem(items, want string) bool {
	for _, code := range strings.Split(items, ",") {
		if strings.TrimSpace(code) == want {
			return true
		}
	}
	return false
}

// releaseURL reads a filing's index page and picks the exhibit wanted, such
// as "99.1", or failing that the first exhibit 99 of any number.
func (c *Client) releaseURL(ctx context.Context, cik int, accession, wanted string) (string, error) {
	folder := fmt.Sprintf("%s/%d/%s", c.archiveURL(), cik, strings.ReplaceAll(accession, "-", ""))
	page, err := c.document(ctx, folder+"/"+accession+"-index.html")
	if err != nil {
		return "", err
	}
	// The index writes 99.1 as EX-99.1 or, at some filers, EX-99.01.
	major, minor, _ := strings.Cut(wanted, ".")
	want := regexp.MustCompile(`(?i)>\s*EX-` + regexp.QuoteMeta(major) + `\.0?` + regexp.QuoteMeta(minor) + `\s*<`)

	var fallback string
	for _, row := range indexRow.FindAllStringSubmatch(page, -1) {
		href := rowHref.FindStringSubmatch(row[1])
		if href == nil {
			continue
		}
		name := href[1][strings.LastIndex(href[1], "/")+1:]
		if want.MatchString(row[1]) {
			return folder + "/" + name, nil
		}
		if fallback == "" && release99.MatchString(row[1]) {
			fallback = name
		}
	}
	if fallback != "" {
		return folder + "/" + fallback, nil
	}
	return "", fmt.Errorf("no press release attached to %s", accession)
}

func (c *Client) document(ctx context.Context, url string) (string, error) {
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, releaseDocMaxBytes))
	return string(raw), err
}

func (c *Client) archiveURL() string {
	if c.ArchiveURL != "" {
		return strings.TrimRight(c.ArchiveURL, "/")
	}
	return defaultArchiveURL
}

// ClipRunes cuts text to n runes at the last line break before it, and says
// that it did.
func ClipRunes(text string, n int) string {
	r := []rune(text)
	if n <= 0 || len(r) <= n {
		return text
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, "\n"); i > n/2 {
		cut = cut[:i]
	}
	return cut + "\n[The release continues; the rest is not shown.]"
}
