package fundamentals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// A results release comes weeks before the filing that carries its figures.
// Micron announced the quarter to 3 September 2026 on 30 September, and its
// annual report was still to come on 4 October, when the owner found the
// analysis written on old data: every table, the twelve months, the multiples
// and the balance sheet stopped at 28 May, and the model had to work around
// them from the release's text.
//
// ReleaseReader has a model copy the release's GAAP figures out of its tables,
// and AddRelease adds them for the periods no filing reaches yet. It trusts
// them only where the release's other columns -- the quarter before, the same
// quarter a year earlier -- match what was filed for those periods.

// releaseForm marks a figure read from a results release.
const releaseForm = "release"

// releaseTolerance is how far a release's figure may sit from the filed one
// and still match: rounding to the million, never a different figure.
const releaseTolerance = 0.005

// releaseLines are the figures read from a release's income and cash-flow
// tables. A wrong revenue or profit means the release was misread, and
// nothing in it is used; a wrong one of the rest is left out of the new
// periods.
var releaseLines = map[string]bool{
	"revenue": true, "grossProfit": true, "operatingIncome": true, "netIncome": true, "epsDiluted": true,
	"researchDevelopment": false, "dilutedShares": false, "operatingCashFlow": false, "capitalExpenditure": false,
}

// releaseBalanceLines are the figures read from a release's balance sheet.
var releaseBalanceLines = []string{
	"assets", "liabilities", "equity", "marketableSecurities", "cash",
	"currentAssets", "currentLiabilities", "longTermDebt", "inventory",
}

var releaseScales = map[string]float64{"units": 1, "thousands": 1e3, "millions": 1e6, "billions": 1e9}

// ReleaseFigures is what a results release prints, as a model copied it out:
// amounts as the tables print them, in the scale their headings give.
type ReleaseFigures struct {
	Currency   string           `json:"currency"`
	MoneyScale string           `json:"moneyScale"`
	ShareScale string           `json:"shareScale"`
	Periods    []ReleasePeriod  `json:"periods"`
	Balances   []ReleaseBalance `json:"balances"`
}

// ReleasePeriod is one column of the income or cash-flow tables.
type ReleasePeriod struct {
	Months  int                `json:"months"`
	End     string             `json:"end"`
	Figures map[string]float64 `json:"figures"`
}

// ReleaseBalance is one column of the balance sheet.
type ReleaseBalance struct {
	Date    string             `json:"date"`
	Figures map[string]float64 `json:"figures"`
}

// ReleaseReader copies a release's figures out of its tables with a model.
type ReleaseReader struct {
	Completer interface {
		Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
	}
}

// Read asks for the figures in the release's text.
func (r ReleaseReader) Read(ctx context.Context, release string) (ReleaseFigures, error) {
	text, _, err := r.Completer.Complete(ctx, config.Prompt("release.figures"), release)
	if err != nil {
		return ReleaseFigures{}, err
	}
	return ParseReleaseFigures(text)
}

// ParseReleaseFigures reads the model's reply: one JSON object, whatever is
// written around it.
func ParseReleaseFigures(text string) (ReleaseFigures, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return ReleaseFigures{}, errors.New("no figures in the reply")
	}
	var f ReleaseFigures
	if err := json.Unmarshal([]byte(text[start:end+1]), &f); err != nil {
		return ReleaseFigures{}, fmt.Errorf("unreadable figures: %w", err)
	}
	return f, nil
}

// AddRelease adds a release's figures for the periods no filing reaches yet,
// and builds the years, quarters and balance sheet again with them. It adds
// nothing, and says why, unless revenue and net income match the filings in
// at least one of the release's columns, and no revenue, profit or earnings a
// share differs from a filed one. The balance sheet is added on a test of its
// own (releaseBalance), and note says what of it was left out.
func (s *Snapshot) AddRelease(r ReleaseFigures, filed time.Time) (note string, err error) {
	if s.obs == nil {
		return "", errors.New("the figures read were not kept")
	}
	if s.ReleaseAdded {
		return "", errors.New("the release was added already")
	}
	if !strings.EqualFold(r.Currency, s.Currency) {
		return "", fmt.Errorf("the release is in %q and the filings in %s", r.Currency, s.Currency)
	}
	money, ok := releaseScales[strings.ToLower(r.MoneyScale)]
	if !ok {
		return "", fmt.Errorf("no scale for the release's amounts: %q", r.MoneyScale)
	}
	shares, ok := releaseScales[strings.ToLower(r.ShareScale)]
	if !ok {
		shares = 0 // share counts are left out
	}

	lastFiled := s.lastFiled()
	missing := map[string]bool{}
	for _, m := range s.Missing {
		missing[m] = true
	}

	// The release's columns for filed periods, checked; its others kept.
	type line struct {
		key string
		o   Observation
	}
	wrong := map[string]bool{}
	var fresh []line
	matched := false
	for _, p := range r.Periods {
		end, err := time.Parse(time.DateOnly, p.End)
		if err != nil || p.Months <= 0 || end.After(filed) {
			continue
		}
		start := s.periodStart(end, p.Months)
		checked := map[string]bool{}
		for key, v := range p.Figures {
			if _, ok := releaseLines[key]; !ok {
				continue
			}
			o := Observation{Start: start, End: end, Value: v * money, Unit: s.Currency, Form: releaseForm, Filed: filed}
			switch key {
			case "epsDiluted":
				o.Value, o.Unit = v, s.Currency+"/shares"
			case "dilutedShares":
				if shares == 0 {
					continue
				}
				o.Value, o.Unit = v*shares, "shares"
			case "capitalExpenditure":
				o.Value = math.Abs(o.Value)
			}
			if daysBetween(end, lastFiled) > periodSlack {
				if !missing[key] {
					fresh = append(fresh, line{key, o})
				}
				continue
			}
			f, ok := First(keepCurrency(s.obs[key], s.Currency), func(f Observation) bool {
				return f.Duration() && abs(daysBetween(f.End, end)) <= periodSlack && abs(f.Days()-o.Days()) <= periodSlack
			})
			if !ok {
				continue
			}
			if !sameFigure(o.Value, f.Value, key) {
				if releaseLines[key] {
					return "", fmt.Errorf("the release's %s for the %d months to %s is %g, and the filed one %g", key, p.Months, p.End, o.Value, f.Value)
				}
				wrong[key] = true
				continue
			}
			checked[key] = true
		}
		matched = matched || (checked["revenue"] && checked["netIncome"])
	}
	if !matched {
		return "", errors.New("no column of the release could be checked against the filings")
	}

	added := map[string][]Observation{}
	for _, l := range fresh {
		if !wrong[l.key] {
			added[l.key] = append(added[l.key], l.o)
		}
	}
	if len(added) == 0 {
		return "", errors.New("the release gives no period the filings lack")
	}
	note = s.releaseBalance(r.Balances, money, filed, added)

	for key, obs := range added {
		all := append(append([]Observation{}, s.obs[key]...), obs...)
		sort.SliceStable(all, func(i, j int) bool { return all[i].End.After(all[j].End) })
		s.obs[key] = all
	}
	s.build()
	s.ReleaseAdded = true
	unfiled := func(y *Year) {
		if y != nil {
			y.FromRelease = daysBetween(y.End, lastFiled) > periodSlack
		}
	}
	for i := range s.Years {
		unfiled(&s.Years[i])
	}
	for i := range s.Quarters {
		unfiled(&s.Quarters[i])
	}
	unfiled(s.TTM)
	unfiled(s.YTD)
	s.Balance.FromRelease = s.Balance.Form == releaseForm
	return note, nil
}

// releaseCoreBalance are the balance-sheet lines a release's must give, and
// match the filings on, to be used at all.
var releaseCoreBalance = []string{"assets", "equity", "cash"}

// releaseBalance adds the release's latest balance sheet to added, where it
// is newer than the filed one and its core lines match the filed balance
// sheets it repeats. A line that does not match them, or that the release
// does not give, keeps its filed figure, at its own date: Micron's release
// gave long-term debt of US$5.14bn at 28 May 2026, where the line it is
// filed under read US$8.84bn. It says what it left out, and why.
func (s *Snapshot) releaseBalance(balances []ReleaseBalance, money float64, filed time.Time, added map[string][]Observation) string {
	const sameDay = 3
	latest := s.Balance.AsOf
	var newest ReleaseBalance
	var newestDate time.Time
	wrong := map[string]bool{}
	checked := false
	for _, b := range balances {
		date, err := time.Parse(time.DateOnly, b.Date)
		if err != nil || date.After(filed) {
			continue
		}
		if daysBetween(date, latest) > sameDay {
			if date.After(newestDate) {
				newest, newestDate = b, date
			}
			continue
		}
		for _, key := range releaseBalanceLines {
			v, ok := b.Figures[key]
			if !ok {
				continue
			}
			f, ok := First(Instant(keepCurrency(s.obs[key], s.Currency)), func(f Observation) bool {
				return abs(daysBetween(f.End, date)) <= sameDay
			})
			if !ok {
				continue
			}
			if !sameFigure(v*money, f.Value, key) {
				wrong[key] = true
				continue
			}
			checked = checked || key == "assets"
		}
	}
	switch {
	case newestDate.IsZero():
		return "it gives no balance sheet newer than the filed one"
	case !checked:
		return "none of its balance sheets could be checked against the filings"
	}
	for _, key := range releaseCoreBalance {
		if _, ok := newest.Figures[key]; !ok || wrong[key] {
			return "its balance sheet's " + key + " is missing or does not match the filings"
		}
	}
	var left []string
	for _, key := range releaseBalanceLines {
		v, ok := newest.Figures[key]
		if !ok || wrong[key] {
			left = append(left, key)
			continue
		}
		added[key] = append(added[key], Observation{End: newestDate, Value: v * money, Unit: s.Currency, Form: releaseForm, Filed: filed})
	}
	if len(left) > 0 {
		return "kept from the filings, missing or not matching: " + strings.Join(left, ", ")
	}
	return ""
}

// sameFigure says a release's figure is the filed one, to the rounding of
// the release's tables.
func sameFigure(release, filed float64, key string) bool {
	tolerance := releaseTolerance * math.Abs(filed)
	if key == "epsDiluted" {
		tolerance = math.Max(tolerance, 0.011)
	}
	return math.Abs(release-filed) <= tolerance
}

// lastFiled is the end of the latest period the filings give.
func (s *Snapshot) lastFiled() time.Time {
	var last time.Time
	for _, key := range []string{"revenue", "netIncome", "operatingIncome"} {
		for _, o := range s.obs[key] {
			if o.Duration() && o.Form != releaseForm && o.End.After(last) {
				last = o.End
			}
		}
	}
	return last
}

// periodStart is where the months to end began: the day after a filed period
// ended near then, since a release gives only a period's end, and its length
// in months, which a year of weeks makes inexact.
func (s *Snapshot) periodStart(end time.Time, months int) time.Time {
	guess := end.AddDate(0, -months, 0)
	best, gap := guess, periodSlack+3
	for _, key := range []string{"revenue", "netIncome", "operatingIncome"} {
		for _, o := range s.obs[key] {
			if g := abs(daysBetween(o.End, guess)); o.Duration() && g < gap {
				best, gap = o.End, g
			}
		}
	}
	return best.AddDate(0, 0, 1)
}
