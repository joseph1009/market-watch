package fundamentals

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/sec"
)

type fakeFilings struct {
	report    sec.Filing
	reportErr error
	business  string
	textErr   error
	recent    []sec.Filing
	recentErr error
}

func (f fakeFilings) AnnualReport(context.Context, string) (sec.Filing, error) {
	return f.report, f.reportErr
}

func (f fakeFilings) BusinessSection(context.Context, string, int) (string, error) {
	return f.business, f.textErr
}

func (f fakeFilings) Recent(context.Context, string, time.Time) ([]sec.Filing, error) {
	return f.recent, f.recentErr
}

func TestAddBusinessFillsInWhatTheCompanyDoes(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	f := fakeFilings{
		report: sec.Filing{
			Items: []string{"10-K"},
			Filed: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),
			URL:   "https://sec.gov/report.htm",
		},
		business: "We design and manufacture memory and storage products.",
		recent: []sec.Filing{
			{Filed: time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC), Items: []string{"2.02"}},
			{Filed: time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC), Items: []string{"5.02"}},
		},
	}

	snap := Snapshot{Ticker: "MU"}
	if problems := AddBusiness(context.Background(), f, &snap, now); len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if !strings.Contains(snap.Business, "memory and storage") {
		t.Errorf("business description missing: %q", snap.Business)
	}
	if !strings.Contains(snap.BusinessFrom, "10-K filed 1 October 2025") {
		t.Errorf("the source of the description is not named: %q", snap.BusinessFrom)
	}
	if len(snap.Events) != 2 {
		t.Fatalf("got %d events, want 2: %v", len(snap.Events), snap.Events)
	}
	if !strings.Contains(snap.Events[0], "reported results") {
		t.Errorf("the item code was not put in plain words: %q", snap.Events[0])
	}
}

// The accounts are the part that cannot be had anywhere else, so a failure to
// read the description must cost the description and nothing more.
func TestAddBusinessFailuresAreNotFatal(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	f := fakeFilings{
		reportErr: errors.New("submissions unavailable"),
		recentErr: errors.New("also unavailable"),
	}

	snap := Snapshot{Ticker: "MU", Years: []Year{{Label: "FY to Aug 2025"}}}
	problems := AddBusiness(context.Background(), f, &snap, now)

	if len(problems) != 2 {
		t.Errorf("got %d problems, want both reported: %v", len(problems), problems)
	}
	if snap.Business != "" || len(snap.Events) != 0 {
		t.Errorf("something was invented from a failure: %+v", snap)
	}
	if len(snap.Years) != 1 {
		t.Error("the accounts were disturbed by an unrelated failure")
	}
}

// A report whose Item 1 could not be located must say so rather than pass an
// empty description off as a description.
func TestAddBusinessReportsAnEmptySection(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	f := fakeFilings{
		report:   sec.Filing{Items: []string{"10-K"}, URL: "https://sec.gov/report.htm"},
		business: "",
	}

	snap := Snapshot{Ticker: "MU"}
	problems := AddBusiness(context.Background(), f, &snap, now)

	if len(problems) == 0 || !strings.Contains(problems[0].Error(), "no business section") {
		t.Errorf("an empty section was accepted silently: %v", problems)
	}
	if snap.Business != "" {
		t.Errorf("Business = %q, want empty", snap.Business)
	}
}

func TestTableCarriesTheBusinessAndEvents(t *testing.T) {
	snap := Snapshot{
		Ticker: "MU", Currency: "USD",
		Business:     "We design and manufacture memory and storage products.",
		BusinessFrom: "10-K filed 1 October 2025",
		Events:       []string{"24 Jun 2026: reported results"},
		Years:        []Year{{Label: "FY to Aug 2025", Figures: map[string]Value{"revenue": known(37e9)}}},
	}

	table := snap.Table()
	for _, want := range []string{
		"What the company says it does, from its 10-K filed 1 October 2025",
		"memory and storage products",
		"What it has told the SEC recently",
		"24 Jun 2026: reported results",
		"filing headings, not the filings themselves",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("table is missing %q:\n%s", want, table)
		}
	}
}
