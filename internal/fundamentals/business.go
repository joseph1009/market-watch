package fundamentals

import (
	"context"
	"fmt"
	"time"

	"github.com/joseph1009/market-watch/internal/sec"
)

const (
	// businessRunes is how much of the business description the analysis gets.
	// Item 1 of a large filer runs to tens of thousands of words; the first few
	// thousand carry what it sells and to whom, and the rest is regulation,
	// seasonality and human capital.
	businessRunes = 9000

	// eventsSince is how far back company announcements are worth reading.
	eventsSince = 120 * 24 * time.Hour

	// maxEvents keeps the list to what a reader would scan.
	maxEvents = 8
)

// Filings is the part of the SEC client this needs: the company's own account
// of itself, and what it has announced lately.
type Filings interface {
	AnnualReport(ctx context.Context, ticker string) (sec.Filing, error)
	BusinessSection(ctx context.Context, url string, maxRunes int) (string, error)
	Recent(ctx context.Context, ticker string, since time.Time) ([]sec.Filing, error)
}

// AddBusiness fills in what the company does and what it has announced.
//
// Both are best-effort and neither is fatal. A missing description costs the
// analysis its first section; a failed fetch that stopped the whole thing would
// cost the reader the accounts as well, which are the part that cannot be got
// anywhere else.
func AddBusiness(ctx context.Context, f Filings, snap *Snapshot, now time.Time) []error {
	if f == nil || snap == nil {
		return nil
	}

	var problems []error

	if report, err := f.AnnualReport(ctx, snap.Ticker); err != nil {
		problems = append(problems, fmt.Errorf("find the annual report: %w", err))
	} else {
		text, err := f.BusinessSection(ctx, report.URL, businessRunes)
		switch {
		case err != nil:
			problems = append(problems, fmt.Errorf("read the annual report: %w", err))
		case text == "":
			problems = append(problems, fmt.Errorf("no business section found in %s", report.URL))
		default:
			snap.Business = text
			snap.BusinessFrom = fmt.Sprintf("%s filed %s",
				report.Items[0], report.Filed.Format("2 January 2006"))
		}
	}

	filings, err := f.Recent(ctx, snap.Ticker, now.Add(-eventsSince))
	if err != nil {
		problems = append(problems, fmt.Errorf("read recent filings: %w", err))
		return problems
	}
	for _, filing := range filings {
		if len(snap.Events) == maxEvents {
			break
		}
		snap.Events = append(snap.Events,
			fmt.Sprintf("%s: %s", filing.Filed.Format("2 Jan 2006"), filing.Describe()))
	}
	return problems
}
