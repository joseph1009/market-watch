package sec

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A foreign filer reports its results on Form 6-K, which carries no item
// codes. What a 6-K holds is on its cover page, which lists the exhibits by
// title, and the title is what tells the results apart from the monthly share
// returns and meeting notices that make up most 6-Ks. Filers lay the list out
// differently: Alibaba writes "Exhibit 99.1 - Press Release - Alibaba Group
// Announces June Quarter 2026 Results" on one line, JD puts "99.1" in one
// table cell and the title in the next, and PDD's title is only "Press
// Release (Earnings Release)". Some list nothing -- Sea's says "Press
// Release" and no more, and Novo Nordisk's 6-K is the announcement itself --
// and theirs are not found this way.
var (
	exhibitsHead = regexp.MustCompile(`(?i)^exhibits?(\s+index)?$`)
	exhibitLine  = regexp.MustCompile(`(?i)^(?:exhibit\s+)?(99\.\d+)(?:\s*-+\s*|\s+|$)(.*)$`)
	resultsTitle = regexp.MustCompile(`(?i)\b(quarter|quarterly|half[- ]year|interim|annual|full[- ]year|fiscal year)\b.{0,40}\bresults\b|\bearnings\s+(release|results)\b|\bfinancial\s+results\b`)
	meetingTitle = regexp.MustCompile(`(?i)\b(meeting|voting|poll)\b`)
)

// maxCovers bounds how many 6-K cover pages one search reads. Alibaba files
// five or so a month, nearly all of them routine.
const maxCovers = 25

// exhibit is one document a 6-K's cover page lists.
type exhibit struct {
	number string // "99.1"
	title  string
}

// coverExhibits reads a 6-K's cover page and returns the exhibits it lists.
func (c *Client) coverExhibits(ctx context.Context, cik int, accession, primary string) ([]exhibit, error) {
	url := fmt.Sprintf("%s/%d/%s/%s", c.archiveURL(), cik, strings.ReplaceAll(accession, "-", ""), primary)
	page, err := c.document(ctx, url)
	if err != nil {
		return nil, err
	}
	// Read one after another, these would go faster than the SEC allows.
	select {
	case <-time.After(requestPause):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return exhibits(plainText(page)), nil
}

// exhibits reads the exhibit list out of a cover page's text.
func exhibits(text string) []exhibit {
	lines := strings.Split(text, "\n")
	// The list follows its heading. Above it, the body can break a sentence
	// as "furnished as Exhibit" / "99.1 of this report", which read as an
	// exhibit titled "of this report".
	for i, line := range lines {
		if exhibitsHead.MatchString(line) {
			lines = lines[i+1:]
			break
		}
	}
	var out []exhibit
	for i, line := range lines {
		m := exhibitLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		title := strings.TrimSpace(m[2])
		if title == "" && i+1 < len(lines) && !exhibitLine.MatchString(lines[i+1]) {
			title = lines[i+1] // the next table cell
		}
		if title != "" {
			out = append(out, exhibit{number: m[1], title: title})
		}
	}
	return out
}

// routineTitle marks the 6-Ks that say nothing about the business: the share
// returns a Hong Kong listing files every month and after each day of
// buybacks, record dates, share awards to staff, and the change to a
// convertible bond's terms that every dividend sets off. With the meeting
// notices, they are most of what Alibaba files.
var routineTitle = regexp.MustCompile(`(?i)\bmonthly return\b|\bnext day disclosure\b|\brecord date\b|\bgrant of awards\b|\badjustment to the (conversion|exchange) (rate|price)\b`)

// announcements returns a foreign filer's 6-Ks since a date, newest first,
// each described by the titles on its cover and the routine ones left out.
// A company that files none, as no US company does, costs no requests.
func (c *Client) announcements(ctx context.Context, doc submissions, co company, since time.Time) ([]Filing, error) {
	r := doc.Filings.Recent
	var out []Filing
	covers := 0
	for i := range r.Form {
		if r.Form[i] != "6-K" {
			continue
		}
		filed, err := time.Parse("2006-01-02", r.FilingDate[i])
		if err != nil || filed.Before(since) {
			continue
		}
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
			return nil, err
		}
		var titles []string
		for _, e := range exhibits {
			if !routineTitle.MatchString(e.title) && !meetingTitle.MatchString(e.title) {
				titles = append(titles, e.title)
			}
		}
		if len(titles) == 0 {
			continue
		}
		out = append(out, Filing{
			Company:   doc.Name,
			Filed:     filed,
			Accession: r.AccessionNumber[i],
			URL:       filingURL(co.CIK, r.AccessionNumber[i], primary),
			Exhibits:  titles,
		})
	}
	return out, nil
}

// resultsExhibit picks the exhibit that announces results, passing over a
// meeting's voting results, which are not the company's.
func resultsExhibit(exhibits []exhibit) (exhibit, bool) {
	for _, e := range exhibits {
		if resultsTitle.MatchString(e.title) && !meetingTitle.MatchString(e.title) {
			return e, true
		}
	}
	return exhibit{}, false
}
