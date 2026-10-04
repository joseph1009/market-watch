package fundamentals

import (
	"fmt"
	"strings"
)

// statutoryTax is the US federal rate, assumed where the company's own rate
// cannot be read or means nothing: a loss before tax, or a one-off that takes
// the rate below zero or past half.
const statutoryTax = 0.21

// Sensitivity is how far a company's earnings move when one thing about the
// business changes and nothing else does. A verdict that says "margins
// expand" has to say by how much that is worth, and whether a point of margin
// is 1% of the year's earnings or 10% depends entirely on the shape of the
// accounts -- which is arithmetic, and done here rather than left to a model.
type Sensitivity struct {
	// Basis is the period the figures cover: the last twelve months where
	// the interim filings allow it, the last full year otherwise.
	Basis string

	GrossMargin, OperatingMargin Value

	// Leverage is gross profit over operating income: with operating costs
	// fixed, the percentage a 1% change in revenue moves operating income.
	// Unknown where there is no gross profit or no operating profit.
	Leverage Value

	// TaxRate is what a change in profit is taxed at, TaxFiled whether it is
	// the company's own effective rate or statutoryTax.
	TaxRate  float64
	TaxFiled bool

	// Shares are the weighted diluted shares of the latest period, or the
	// cover page's count where those are not filed, and SharesFrom which.
	Shares     Value
	SharesFrom string

	// Per diluted share, after tax: a 100 basis point change in gross margin,
	// and a 1% change in revenue with operating costs held fixed (Volume) or
	// moving in step with revenue (VolumeScaled). The two volume figures are
	// the ends of the range; the truth for a given company lies between them.
	// All unknown where the accounts are not in US dollars.
	Margin, Volume, VolumeScaled Value
}

// Sensitivity works the figures out, and reports false where there is no
// revenue to work from.
func (s Snapshot) Sensitivity() (Sensitivity, bool) {
	if len(s.Years) == 0 {
		return Sensitivity{}, false
	}
	figure, basis := s.lastTwelveMonths("revenue", "operatingIncome")
	revenue, gross, operating := figure("revenue"), figure("grossProfit"), figure("operatingIncome")
	if !revenue.Known || revenue.Amount <= 0 {
		return Sensitivity{}, false
	}

	out := Sensitivity{
		Basis:           basis,
		GrossMargin:     Ratio(gross, revenue),
		OperatingMargin: Ratio(operating, revenue),
		TaxRate:         statutoryTax,
	}
	// A gross profit smaller than the operating income is a filer tagging
	// something other than what the name says, and the ratio would be below
	// one: less than proportional, which fixed costs cannot produce.
	if operating.Known && operating.Amount > 0 && gross.Known && gross.Amount >= operating.Amount {
		out.Leverage = Ratio(gross, operating)
	}
	if rate := Ratio(figure("incomeTax"), figure("pretaxIncome")); rate.Known && figure("pretaxIncome").Amount > 0 && rate.Amount >= 0 && rate.Amount <= 0.5 {
		out.TaxRate, out.TaxFiled = rate.Amount, true
	}
	out.Shares, out.SharesFrom = s.dilutedShares()

	// Per-share figures only where the price and the accounts share a
	// currency, for the reason valuation gives: a US listing of a foreign
	// filer may stand for several ordinary shares, at a ratio not known here.
	if s.currency() != "USD" || !out.Shares.Known || out.Shares.Amount <= 0 {
		return out, true
	}
	perShare := func(v Value) Value {
		if !v.Known {
			return Value{}
		}
		return known(v.Amount * 0.01 * (1 - out.TaxRate) / out.Shares.Amount)
	}
	out.Margin = perShare(revenue)
	out.Volume = perShare(gross)
	if operating.Known && operating.Amount > 0 {
		out.VolumeScaled = perShare(operating)
	}
	return out, true
}

// lastTwelveMonths reads figures for the last twelve months where every one of
// needed can be rolled forward -- the last full year, less the part of it the
// prior-year interim covered, plus the current interim -- and for the last full
// year otherwise. All the figures come from the one basis: a margin with its
// revenue from one period and its profit from another is not a margin.
func (s Snapshot) lastTwelveMonths(needed ...string) (func(string) Value, string) {
	year := s.Years[0]
	annual := func(key string) Value { return year.Figure(key) }
	annualBasis := year.Label
	if s.YTD == nil || s.PriorYTD == nil {
		return annual, annualBasis
	}
	rolled := func(key string) Value {
		return Add(Less(year.Figure(key), s.PriorYTD.Figure(key)), s.YTD.Figure(key))
	}
	for _, key := range needed {
		if !rolled(key).Known {
			return annual, annualBasis
		}
	}
	return rolled, MonthSpan(s.YTD.End, 12)
}

// dilutedShares is the latest period's weighted diluted share count, which is
// what earnings per share are divided by, or the cover page's count of shares
// outstanding where no diluted count is filed.
func (s Snapshot) dilutedShares() (Value, string) {
	if s.YTD != nil {
		if v := s.YTD.Figure("dilutedShares"); v.Known {
			return v, "the weighted average diluted count for " + s.YTD.Label
		}
	}
	if v := s.Years[0].Figure("dilutedShares"); v.Known {
		return v, "the weighted average diluted count for " + s.Years[0].Label
	}
	if v := s.Balance.Figure("sharesOutstanding"); v.Known {
		return v, "shares outstanding on the latest filing's cover, as no diluted count is filed"
	}
	return Value{}, ""
}

// SensitivityFacts writes the sensitivity for the model, or nothing where it
// cannot be worked out.
func (s Snapshot) SensitivityFacts() string {
	sens, ok := s.Sensitivity()
	// A bank files neither margin: its earnings turn on lending spreads and
	// credit losses, which none of this measures, so it is left out whole.
	if !ok || (!sens.GrossMargin.Known && !sens.OperatingMargin.Known) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nHow sensitive the earnings are, worked out here from the filed figures for %s. This is arithmetic, not a forecast: each line changes one thing and holds everything else still.\n", sens.Basis)
	var margins []string
	if sens.GrossMargin.Known {
		margins = append(margins, "gross margin "+percent(sens.GrossMargin))
	}
	if sens.OperatingMargin.Known {
		margins = append(margins, "operating margin "+percent(sens.OperatingMargin))
	}
	fmt.Fprintf(&b, "- %s.\n", capitalise(strings.Join(margins, ", ")))

	switch {
	case sens.Leverage.Known:
		fmt.Fprintf(&b, "- Operating leverage %s: gross profit is %s operating income, so with operating costs fixed a 1%% change in revenue moves operating income about %.1f%%.\n",
			times(sens.Leverage), times(sens.Leverage), sens.Leverage.Amount)
	case !sens.GrossMargin.Known:
		b.WriteString("- Operating leverage cannot be measured: the company files no gross profit, as banks, insurers and many service businesses do not.\n")
	default:
		b.WriteString("- Operating leverage is not meaningful: there was no operating profit over the period, so small changes in revenue swing the result between loss and profit.\n")
	}

	if s.currency() != "USD" {
		fmt.Fprintf(&b, "Per-share effects are not worked out: the accounts are in %s while the share is priced in US dollars and may stand for several ordinary shares.\n", s.currency())
		return b.String()
	}
	if !sens.Margin.Known {
		b.WriteString("Per-share effects are not worked out: no share count is filed.\n")
		return b.String()
	}

	// A point of either margin is 1% of revenue kept as profit, so the figure
	// is the same; it is named for the margin the company reports.
	margin := "gross margin"
	if !sens.GrossMargin.Known {
		margin = "operating margin"
	}
	fmt.Fprintf(&b, "- 100 basis points of %s: ±US$%s a diluted share after tax%s.\n", margin, perShare(sens.Margin), s.ofExpected(sens.Margin))
	switch {
	case sens.Volume.Known && sens.VolumeScaled.Known:
		fmt.Fprintf(&b, "- 1%% of revenue: ±US$%s a share with operating costs fixed, ±US$%s if every cost moves with revenue.\n",
			perShare(sens.Volume), perShare(sens.VolumeScaled))
	case sens.VolumeScaled.Known:
		fmt.Fprintf(&b, "- 1%% of revenue: ±US$%s a share if every cost moves with revenue; more where costs are fixed.\n", perShare(sens.VolumeScaled))
	case sens.Volume.Known:
		fmt.Fprintf(&b, "- 1%% of revenue: ±US$%s a share with operating costs fixed.\n", perShare(sens.Volume))
	}

	tax := fmt.Sprintf("the company's effective rate over the same period, %.1f%%", sens.TaxRate*100)
	if !sens.TaxFiled {
		tax = fmt.Sprintf("the US federal rate of %.0f%%, as the company's own rate could not be read or was distorted by a loss or a one-off", sens.TaxRate*100)
	}
	fmt.Fprintf(&b, "Taxed at %s, and spread over %s shares, %s.\n", tax, amount(sens.Shares), sens.SharesFrom)
	return b.String()
}

// ofExpected sets an effect per share against the year's consensus, which is
// what makes it large or small.
func (s Snapshot) ofExpected(v Value) string {
	if s.ExpectedEPS <= 0 || !v.Known {
		return ""
	}
	return fmt.Sprintf(", %.1f%% of the US$%.2f analysts expect for %s (their figure is usually on the company's adjusted basis)",
		v.Amount/s.ExpectedEPS*100, s.ExpectedEPS, YearEnding(s.ExpectedFor))
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// perShare writes an amount per share to the cent, or to a tenth of a cent
// where it is smaller than a cent and would otherwise read as nothing.
func perShare(v Value) string {
	a := v.Amount
	if a < 0 {
		a = -a
	}
	if a < 0.01 {
		return fmt.Sprintf("%.3f", a)
	}
	return fmt.Sprintf("%.2f", a)
}
