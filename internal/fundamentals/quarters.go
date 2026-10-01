package fundamentals

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The latest quarters, each on its own. The full years and the year so far
// say where a business has been; deep into a year they say little about
// where it is now. On 30 September 2026 an analysis read a year so far that
// ran to June and a full year that ended nine months before -- the owner
// asked for the latest quarter set against the same quarter a year earlier,
// and the last four together.
//
// Companies file the quarter's income on its own, but their cash flow as the
// year so far, and no fourth quarter at all: that is the year less its first
// nine months. buildQuarters reads each figure for the three months alone,
// taking it as filed where it was and the difference of two running totals
// where it was not.

const (
	// quarterDays bounds a fiscal quarter's length.
	quarterMinDays, quarterMaxDays = 80, 100

	// quartersKept is how many quarters are read: five, so the latest has the
	// same quarter a year earlier beside it.
	quartersKept = 5

	// quarterStep is how far one quarter's end lies from the next.
	quarterStep = 91
)

// quarterKeys are the figures read quarter by quarter.
var quarterKeys = []string{
	"revenue", "grossProfit", "operatingIncome", "netIncome", "researchDevelopment",
	"operatingCashFlow", "capitalExpenditure", "epsDiluted",
}

// buildQuarters reads the latest quarters, newest first, and the twelve
// months they add up to where the last four run on from one another.
func buildQuarters(byKey map[string][]Observation, currency string) (quarters []Year, ttm *Year) {
	var ends []time.Time
	note := func(t time.Time) {
		for _, e := range ends {
			if abs(daysBetween(e, t)) <= periodSlack {
				return
			}
		}
		ends = append(ends, t)
	}
	for _, key := range []string{"revenue", "netIncome", "operatingIncome"} {
		for _, o := range keepCurrency(byKey[key], currency) {
			if o.Duration() && (isQuarter(o) || isFullYear(o)) {
				note(o.End)
			}
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].After(ends[j]) })

	for _, end := range ends {
		q := Year{End: end, Label: "3m to " + end.Format("Jan 2006"), Figures: map[string]Value{}}
		for _, key := range quarterKeys {
			// A share count moves through the year, so a fourth quarter's
			// earnings a share is not the year's less nine months'; it is
			// taken only where it was filed.
			if v := quarterValue(keepCurrency(byKey[key], currency), end, key == "epsDiluted"); v.Known {
				q.Figures[key] = v
			}
		}
		if !q.Figure("revenue").Known && !q.Figure("netIncome").Known {
			continue
		}
		quarters = append(quarters, q)
		if len(quarters) == quartersKept {
			break
		}
	}

	if len(quarters) < 4 {
		return quarters, nil
	}
	for i := 0; i < 3; i++ {
		if abs(daysBetween(quarters[i].End, quarters[i+1].End)-quarterStep) > periodSlack {
			return quarters, nil // a gap: four quarters that are not a year
		}
	}
	ttm = &Year{End: quarters[0].End, Label: "12m to " + quarters[0].End.Format("Jan 2006"), Figures: map[string]Value{}}
	for _, key := range quarterKeys {
		sum := quarters[0].Figure(key)
		for _, q := range quarters[1:4] {
			sum = Add(sum, q.Figure(key))
		}
		if sum.Known {
			ttm.Figures[key] = sum
		}
	}
	return quarters, ttm
}

func isQuarter(o Observation) bool {
	d := o.Days()
	return d >= quarterMinDays && d <= quarterMaxDays
}

func isFullYear(o Observation) bool {
	d := o.Days()
	return d >= 350 && d <= 380
}

// quarterValue is a figure for the three months to end: as filed for those
// three months where it was, and otherwise the running total to end less the
// running total to the quarter before, both counted from the same start.
func quarterValue(obs []Observation, end time.Time, filedOnly bool) Value {
	if o, ok := First(obs, func(o Observation) bool {
		return o.Duration() && isQuarter(o) && abs(daysBetween(o.End, end)) <= periodSlack
	}); ok {
		return known(o.Value)
	}
	if filedOnly {
		return Value{}
	}
	for _, long := range obs {
		if !long.Duration() || isQuarter(long) || long.Days() > 380 || abs(daysBetween(long.End, end)) > periodSlack {
			continue
		}
		short, ok := First(obs, func(o Observation) bool {
			return o.Duration() && abs(daysBetween(o.Start, long.Start)) <= periodSlack &&
				abs(o.Days()-(long.Days()-quarterStep)) <= periodSlack
		})
		if ok {
			return known(long.Value - short.Value)
		}
	}
	return Value{}
}

// quarterTable writes the latest quarters and the twelve months they make,
// with each quarter set against the one before it and against the same
// quarter a year earlier.
func (s Snapshot) quarterTable() string {
	if len(s.Quarters) == 0 {
		return ""
	}
	columns := append([]Year{}, s.Quarters...)
	if s.TTM != nil {
		columns = append(columns, *s.TTM)
	}

	var b strings.Builder
	b.WriteString("\nThe latest quarters, most recent first, each three months on its own")
	if s.TTM != nil {
		b.WriteString(", then the last four added together")
	}
	b.WriteString(". A fourth quarter is the full year less its first nine months; earnings a share are shown only where they were filed for the quarter. These are the freshest filed figures: lead with them when the last full year is months old.\n")

	b.WriteString(pad("", 32))
	for _, q := range columns {
		b.WriteString(padLeft(q.Label, cellWidth))
	}
	b.WriteString("\n")
	for _, row := range incomeRows {
		b.WriteString(pad(row.label, 32))
		for _, q := range columns {
			b.WriteString(padLeft(amount(q.Figure(row.key)), cellWidth))
		}
		b.WriteString("\n")
	}
	b.WriteString(pad("Free cash flow", 32))
	for _, q := range columns {
		b.WriteString(padLeft(amount(q.FreeCashFlow()), cellWidth))
	}
	b.WriteString("\n")
	b.WriteString(pad("Diluted EPS ("+s.currency()+")", 32))
	for _, q := range columns {
		b.WriteString(padLeft(plain(q.Figure("epsDiluted")), cellWidth))
	}
	b.WriteString("\n")
	for _, d := range []struct {
		label string
		of    func(Year) Value
	}{
		{"Gross margin", func(y Year) Value { return Ratio(y.Figure("grossProfit"), y.Figure("revenue")) }},
		{"Operating margin", func(y Year) Value { return Ratio(y.Figure("operatingIncome"), y.Figure("revenue")) }},
		{"Free cash flow margin", func(y Year) Value { return Ratio(y.FreeCashFlow(), y.Figure("revenue")) }},
	} {
		b.WriteString(pad(d.label, 32))
		for _, q := range columns {
			b.WriteString(padLeft(percent(d.of(q)), cellWidth))
		}
		b.WriteString("\n")
	}

	growth := func(label string, against func(i int) (Year, bool)) {
		b.WriteString(pad(label, 32))
		for i := range s.Quarters {
			v := Value{}
			if prior, ok := against(i); ok {
				v = Ratio(Less(s.Quarters[i].Figure("revenue"), prior.Figure("revenue")), prior.Figure("revenue"))
			}
			b.WriteString(padLeft(percent(v), cellWidth))
		}
		b.WriteString("\n")
	}
	growth("Revenue vs quarter before", func(i int) (Year, bool) {
		if i+1 < len(s.Quarters) {
			return s.Quarters[i+1], true
		}
		return Year{}, false
	})
	growth("Revenue vs a year earlier", func(i int) (Year, bool) {
		for _, q := range s.Quarters[i+1:] {
			if abs(daysBetween(s.Quarters[i].End, q.End)-365) <= periodSlack {
				return q, true
			}
		}
		return Year{}, false
	})
	fmt.Fprintf(&b, "Quarters read from the filings up to %s.\n", s.Quarters[0].End.Format("2 January 2006"))
	return b.String()
}
