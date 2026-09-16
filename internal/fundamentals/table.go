package fundamentals

import (
	"fmt"
	"strings"
	"time"
)

// million scales reported amounts. Filers tag in whole dollars, and a column of
// twelve-digit numbers is unreadable for a person and no easier for a model.
const million = 1_000_000

// rows are the lines shown per fiscal year, in reading order: what came in,
// what was left after each stage, and what the year generated in cash.
var incomeRows = []struct {
	label string
	key   string
}{
	{"Revenue", "revenue"},
	{"Gross profit", "grossProfit"},
	{"Operating income", "operatingIncome"},
	{"Net income", "netIncome"},
	{"R&D spend", "researchDevelopment"},
	{"Operating cash flow", "operatingCashFlow"},
	{"Capital spending", "capitalExpenditure"},
}

var balanceRows = []struct {
	label string
	key   string
}{
	{"Total assets", "assets"},
	{"Total liabilities", "liabilities"},
	{"Shareholders' equity", "equity"},
	{"Cash and equivalents", "cash"},
	{"Short-term investments", "marketableSecurities"},
	{"Current assets", "currentAssets"},
	{"Current liabilities", "currentLiabilities"},
	{"Long-term debt", "longTermDebt"},
	{"Inventory", "inventory"},
}

// Table renders the snapshot as text for the model and for a terminal. Figures
// are in millions of US dollars unless the row says otherwise, and a line the
// filer does not report reads "not reported" rather than zero.
func (s Snapshot) Table() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s), SEC CIK %d\n", s.Company, s.Ticker, s.CIK)
	fmt.Fprintf(&b, "Figures as filed with the SEC, in %s. Amounts carry their scale: m is million, bn is billion, tn is trillion.\n", s.currency())

	columns := s.columns()
	if len(columns) > 0 {
		b.WriteString("\nReporting periods, most recent first.")
		if s.YTD != nil {
			b.WriteString(" The first column is the current financial year so far, from the latest interim filing")
			if s.PriorYTD != nil {
				b.WriteString(", and the second is the same stretch of the year before it")
			}
			b.WriteString(". The rest are full fiscal years, so do not add them together")
		}
		b.WriteString("\n")

		b.WriteString(pad("", 32))
		for _, y := range columns {
			b.WriteString(padLeft(y.Label, 26))
		}
		b.WriteString("\n")

		for _, row := range incomeRows {
			b.WriteString(pad(row.label, 32))
			for _, y := range columns {
				b.WriteString(padLeft(amount(y.Figure(row.key)), 26))
			}
			b.WriteString("\n")
		}

		b.WriteString(pad("Free cash flow", 32))
		for _, y := range columns {
			b.WriteString(padLeft(amount(y.FreeCashFlow()), 26))
		}
		b.WriteString("\n")

		b.WriteString(pad("Diluted EPS ("+s.currency()+")", 32))
		for _, y := range columns {
			b.WriteString(padLeft(plain(y.Figure("epsDiluted")), 26))
		}
		b.WriteString("\n")

		b.WriteString("\nDerived from the rows above:\n")
		derived := []struct {
			label string
			of    func(Year) Value
		}{
			{"Gross margin", func(y Year) Value { return Ratio(y.Figure("grossProfit"), y.Figure("revenue")) }},
			{"Operating margin", func(y Year) Value { return Ratio(y.Figure("operatingIncome"), y.Figure("revenue")) }},
			{"Net margin", func(y Year) Value { return Ratio(y.Figure("netIncome"), y.Figure("revenue")) }},
			{"Free cash flow margin", func(y Year) Value { return Ratio(y.FreeCashFlow(), y.Figure("revenue")) }},
			{"R&D as % of revenue", func(y Year) Value { return Ratio(y.Figure("researchDevelopment"), y.Figure("revenue")) }},
		}
		for _, d := range derived {
			b.WriteString(pad(d.label, 32))
			for _, y := range columns {
				b.WriteString(padLeft(percent(d.of(y)), 26))
			}
			b.WriteString("\n")
		}

		// Growth compares like with like: the year so far against the same
		// stretch of last year, and a full year against the full year before
		// it. Comparing a half year to a full one would halve the business.
		b.WriteString(pad("Revenue growth", 32))
		for i := range columns {
			b.WriteString(padLeft(percent(s.revenueGrowth(columns, i)), 26))
		}
		b.WriteString("\n")
	}

	if len(s.Balance.Figures) > 0 {
		fmt.Fprintf(&b, "\nBalance sheet as at %s, from a %s filing:\n", s.Balance.AsOf.Format("2 January 2006"), s.Balance.Form)
		for _, row := range balanceRows {
			fmt.Fprintf(&b, "%s%s\n", pad(row.label, 32), amount(s.Balance.Figure(row.key)))
		}
		fmt.Fprintf(&b, "%s%s\n", pad("Shares outstanding", 32), amount(s.Balance.Figure("sharesOutstanding")))

		b.WriteString("\nDerived from the balance sheet:\n")
		cur := Ratio(s.Balance.Figure("currentAssets"), s.Balance.Figure("currentLiabilities"))
		fmt.Fprintf(&b, "%s%s\n", pad("Current ratio", 32), times(cur))
		fmt.Fprintf(&b, "%s%s\n", pad("Debt to equity", 32), times(Ratio(s.Balance.Figure("longTermDebt"), s.Balance.Figure("equity"))))
		fmt.Fprintf(&b, "%s%s\n", pad("Cash and investments less debt", 32), amount(Less(Add(s.Balance.Figure("cash"), s.Balance.Figure("marketableSecurities")), s.Balance.Figure("longTermDebt"))))
		fmt.Fprintf(&b, "%s%s\n", pad("Equity per share ("+s.currency()+")", 32),
			plain(Ratio(s.Balance.Figure("equity"), s.Balance.Figure("sharesOutstanding"))))
		if len(s.Years) > 0 {
			fmt.Fprintf(&b, "%s%s\n", pad("Return on equity", 32),
				percent(Ratio(s.Years[0].Figure("netIncome"), s.Balance.Figure("equity"))))
		}
	}

	b.WriteString(`
Per-share figures and share counts are as filed and are not restated for later stock splits, so comparing them across years can mislead.
`)

	if len(s.Missing) > 0 {
		fmt.Fprintf(&b, "\nNot reported by this filer, so unavailable rather than zero: %s\n",
			strings.Join(s.Missing, ", "))
	}
	return b.String()
}

// Age says how stale the newest filing is, which decides how much of today's
// news the figures can possibly reflect.
func (s Snapshot) Age(now time.Time) time.Duration {
	if s.Balance.AsOf.IsZero() {
		return 0
	}
	return now.Sub(s.Balance.AsOf)
}

// amount writes a figure at the scale a reader thinks in. A column of
// nine-digit numbers under a "millions" heading makes every row look alike; 215.9bn
// and 6.0m do not, and the unit travels with the number into whatever the model
// writes.
func amount(v Value) string {
	if !v.Known {
		return "not reported"
	}
	size := v.Amount
	if size < 0 {
		size = -size
	}
	switch {
	case size >= 1e12:
		return fmt.Sprintf("%.2ftn", v.Amount/1e12)
	case size >= 1e9:
		return fmt.Sprintf("%.2fbn", v.Amount/1e9)
	case size >= 1e6:
		return fmt.Sprintf("%.1fm", v.Amount/1e6)
	case size >= 1e3:
		return fmt.Sprintf("%.0fk", v.Amount/1e3)
	default:
		return fmt.Sprintf("%.0f", v.Amount)
	}
}

func plain(v Value) string {
	if !v.Known {
		return "not reported"
	}
	return fmt.Sprintf("%.2f", v.Amount)
}

func percent(v Value) string {
	if !v.Known {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", v.Amount*100)
}

func times(v Value) string {
	if !v.Known {
		return "n/a"
	}
	return fmt.Sprintf("%.2fx", v.Amount)
}

func pad(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}

func padLeft(s string, n int) string {
	for len([]rune(s)) < n {
		s = " " + s
	}
	return s
}

// commas groups a rounded amount in threes. Go's formatter has no separator
// verb, and a nine-figure number without one is unreadable at a glance.
func commas(f float64) string {
	s := fmt.Sprintf("%.0f", f)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return sign + strings.Join(append([]string{s}, parts...), ",")
}

// currency is what the figures are denominated in, defaulting to dollars only
// when nothing was read at all.
func (s Snapshot) currency() string {
	if s.Currency == "" {
		return "USD"
	}
	return s.Currency
}

// columns is what the table shows across: the current year so far and the same
// stretch of the year before where the company has filed them, then the full
// fiscal years.
func (s Snapshot) columns() []Year {
	var out []Year
	if s.YTD != nil {
		out = append(out, *s.YTD)
	}
	if s.PriorYTD != nil {
		out = append(out, *s.PriorYTD)
	}
	return append(out, s.Years...)
}

// interimColumns is how many of the leading columns are part-year figures.
func (s Snapshot) interimColumns() int {
	n := 0
	if s.YTD != nil {
		n++
	}
	if s.PriorYTD != nil {
		n++
	}
	return n
}

// revenueGrowth compares a column with the equivalent period a year earlier:
// the year so far against the same stretch of last year, and a full year
// against the year before it. A part-year column has no comparison of its own
// beyond that, so it is left blank rather than measured against a full year.
func (s Snapshot) revenueGrowth(columns []Year, i int) Value {
	interim := s.interimColumns()

	var prior Value
	switch {
	case i == 0 && interim == 2:
		prior = columns[1].Figure("revenue")
	case i < interim:
		return Value{} // the prior-year interim has nothing to compare against
	case i+1 < len(columns):
		prior = columns[i+1].Figure("revenue")
	default:
		return Value{}
	}
	return Ratio(Less(columns[i].Figure("revenue"), prior), prior)
}
