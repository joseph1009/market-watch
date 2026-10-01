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

	if s.Business != "" {
		fmt.Fprintf(&b, "\nWhat the company says it does, from its %s:\n%s\n",
			s.BusinessFrom, s.Business)
	}
	if len(s.Events) > 0 {
		b.WriteString("\nWhat it has told the SEC recently, by filing date:\n")
		for _, e := range s.Events {
			fmt.Fprintf(&b, "- %s\n", e)
		}
		b.WriteString("These are filing headings, not the filings themselves: they say what kind of event was announced, never the terms.\n")
	}

	b.WriteString(s.news())
	if s.Expectations != "" {
		b.WriteString("\n" + strings.TrimSpace(s.Expectations) + "\n")
	}

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
			b.WriteString(padLeft(y.Label, cellWidth))
		}
		b.WriteString("\n")

		for _, row := range incomeRows {
			b.WriteString(pad(row.label, 32))
			for _, y := range columns {
				b.WriteString(padLeft(amount(y.Figure(row.key)), cellWidth))
			}
			b.WriteString("\n")
		}

		b.WriteString(pad("Free cash flow", 32))
		for _, y := range columns {
			b.WriteString(padLeft(amount(y.FreeCashFlow()), cellWidth))
		}
		b.WriteString("\n")

		b.WriteString(pad("Diluted EPS ("+s.currency()+")", 32))
		for _, y := range columns {
			b.WriteString(padLeft(plain(y.Figure("epsDiluted")), cellWidth))
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
				b.WriteString(padLeft(percent(d.of(y)), cellWidth))
			}
			b.WriteString("\n")
		}

		// Growth compares like with like: the year so far against the same
		// stretch of last year, and a full year against the full year before
		// it. Comparing a half year to a full one would halve the business.
		b.WriteString(pad("Revenue growth", 32))
		for i := range columns {
			b.WriteString(padLeft(percent(s.revenueGrowth(columns, i)), cellWidth))
		}
		b.WriteString("\n")
	}

	b.WriteString(s.quarterTable())

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
			income, basis := s.trailingIncome()
			fmt.Fprintf(&b, "%s%s  (profit for %s over equity at the balance sheet date)\n", pad("Return on equity", 32),
				percent(Ratio(income, s.Balance.Figure("equity"))), basis)
		}
	}

	b.WriteString(s.valuation())
	b.WriteString(s.trading())
	if s.Release != "" {
		fmt.Fprintf(&b, "\nThe company's latest results release, %s, as it wrote it. These are the company's own words and its adjusted figures, not the filed accounts: say so when you use them, and prefer the filed figure where the two differ.\n%s\n",
			s.ReleaseFrom, strings.TrimSpace(s.Release))
	}
	if len(s.Backdrop) > 0 {
		b.WriteString("\nThe market backdrop, as measured by FRED, for a company whose fortunes follow one of these:\n")
		for _, line := range s.Backdrop {
			b.WriteString("- " + line + "\n")
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

// cellWidth is one column of figures. It fits "Three months to 30 Sep 2026",
// the longest period label, with a space to spare.
const cellWidth = 28

// padLeft right-aligns a table cell, always leaving at least one space in front
// of it. At twenty-six columns "Nine months to 28 May 2026" filled its cell
// exactly and ran into the next heading, which is the row that says which
// figures belong to which period.
func padLeft(s string, n int) string {
	for len([]rune(s)) < n-1 {
		s = " " + s
	}
	return " " + s
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

// renderValuation sets today's price against the filed figures.
//
// Multiples are computed only where the company reports in US dollars, because
// the price is a US listing and the accounts may not be: TSMC's shares quote in
// dollars while its earnings are filed in Taiwan dollars, and dividing one by
// the other produces a confident number that means nothing. American depositary
// shares make it worse, each standing for several ordinary shares at a ratio
// this has no way to know. So a foreign filer gets its price and no arithmetic.
func (s Snapshot) valuation() string {
	if s.Price == nil {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\nMarket price, as traded rather than as filed: %.2f USD, %s on its last session",
		s.Price.Price, s.Price.Move())
	if !s.Price.AsOf.IsZero() {
		fmt.Fprintf(&b, ", as at %s", s.Price.AsOf.Format("2 Jan 2006"))
	}
	b.WriteString("\n")

	if s.currency() != "USD" {
		fmt.Fprintf(&b, "The accounts are in %s while the price is in US dollars, and a US listing may stand for several ordinary shares. No multiples are computed: they would be meaningless without the exchange rate and the share ratio, neither of which is available here.\n", s.currency())
		return b.String()
	}

	shares := s.Balance.Figure("sharesOutstanding")
	price := known(s.Price.Price)

	if value := multiply(price, shares); value.Known {
		fmt.Fprintf(&b, "%s%s\n", pad("Market value of the equity", 32), amount(value))
	}
	if len(s.Years) > 0 {
		earnings, freeCash, basis := s.trailing()
		fmt.Fprintf(&b, "%s%s  (on %s)\n", pad("Price to earnings", 32),
			times(Ratio(price, earnings)), basis)

		if value := multiply(price, shares); value.Known {
			fmt.Fprintf(&b, "%s%s  (on %s)\n", pad("Free cash flow yield", 32),
				percent(Ratio(freeCash, value)), basis)
		}
	}
	fmt.Fprintf(&b, "%s%s\n", pad("Price to book value", 32),
		times(Ratio(price, Ratio(s.Balance.Figure("equity"), shares))))
	b.WriteString("These multiples compare today's price with figures already filed, so they are historical. There is no peer group here and no history of the multiple itself, which is what would be needed to call one high or low.\n")
	return b.String()
}

// multiply is a product that stays unknown when either side is.
func multiply(a, b Value) Value {
	if !a.Known || !b.Known {
		return Value{}
	}
	return known(a.Amount * b.Amount)
}

// trailing is the last twelve months where the figures allow it, and the last
// full year otherwise.
//
// The full year alone can be badly out of date. Micron's latest annual earnings
// were US$7.59 a share while it had earned US$41.40 in the nine months since --
// a price-to-earnings of 122 times against about 21. Both are arithmetically
// correct and only one describes the company. The twelve months are assembled
// as the last full year, less the part of it the prior-year interim covered,
// plus the current interim: the same stretch of the calendar, one year apart.
func (s Snapshot) trailing() (earnings, freeCash Value, basis string) {
	year := s.Years[0]
	annualEPS, annualFCF := year.Figure("epsDiluted"), year.FreeCashFlow()
	annual := year.Label + " earnings"

	if s.YTD == nil || s.PriorYTD == nil {
		return annualEPS, annualFCF, annual
	}

	eps := Add(Less(annualEPS, s.PriorYTD.Figure("epsDiluted")), s.YTD.Figure("epsDiluted"))
	cash := Add(Less(annualFCF, s.PriorYTD.FreeCashFlow()), s.YTD.FreeCashFlow())
	if !eps.Known && !cash.Known {
		return annualEPS, annualFCF, annual
	}

	// Either half may be missing, so each falls back on its own.
	if !eps.Known {
		eps = annualEPS
	}
	if !cash.Known {
		cash = annualFCF
	}
	return eps, cash, "the twelve months to " + s.YTD.End.Format("2 Jan 2006")
}

// trailingIncome is net income on the same footing as the multiples.
//
// A return on equity is a year's profit over the equity that earned it. The
// last full year's profit over a balance sheet three quarters later is neither:
// for Micron it read 8.5%, against 50.1% on the twelve months to the same
// balance sheet.
func (s Snapshot) trailingIncome() (Value, string) {
	year := s.Years[0]
	annual := year.Figure("netIncome")
	if s.YTD != nil && s.PriorYTD != nil {
		rolled := Add(Less(annual, s.PriorYTD.Figure("netIncome")), s.YTD.Figure("netIncome"))
		if rolled.Known {
			return rolled, "the twelve months to " + s.YTD.End.Format("2 Jan 2006")
		}
	}
	return annual, "the " + strings.Replace(year.Label, "FY to", "year to", 1)
}
