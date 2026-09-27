package consensus

import (
	"fmt"
	"math"
	"strings"
)

// How much of each list the fact sheet shows. Two quarters and two years
// ahead is as far as most estimates are worth reading; four quarters of
// results against forecast is a year of the company's record of meeting them.
const (
	shownQuarters  = 2
	shownYears     = 2
	shownSurprises = 4
)

// Facts writes the report for a model to read, with price the share's last
// price in US dollars (zero where there is none), which turns the forecasts
// into multiples and the target into a distance.
func (r Report) Facts(price float64) string {
	if !r.Covered() {
		return ""
	}
	var b strings.Builder
	b.WriteString("What analysts expect, and what insiders, short sellers and funds have done, from Nasdaq's published consensus. Forecasts are other people's estimates, not filed figures: attribute them as such.\n")

	if len(r.Quarters) > 0 {
		b.WriteString("- Earnings per share expected, next quarters: ")
		b.WriteString(estimates(r.Quarters[:min(shownQuarters, len(r.Quarters))], 0))
		b.WriteString("\n")
	}
	if len(r.Years) > 0 {
		b.WriteString("- Earnings per share expected, fiscal years: ")
		b.WriteString(estimates(r.Years[:min(shownYears, len(r.Years))], price))
		b.WriteString("\n")
	}
	if r.Target > 0 {
		fmt.Fprintf(&b, "- Price target: average US$%s, range US$%s to US$%s", money(r.Target), money(r.TargetLow), money(r.TargetHigh))
		if price > 0 {
			fmt.Fprintf(&b, ", %s today's US$%s", distance(r.Target, price), money(price))
		}
		fmt.Fprintf(&b, ". %d analysts rate it buy, %d hold, %d sell.\n", r.Buy, r.Hold, r.Sell)
	}
	if len(r.Surprises) > 0 {
		b.WriteString("- Results against the forecast, most recent first: ")
		var parts []string
		for _, s := range r.Surprises[:min(shownSurprises, len(r.Surprises))] {
			when := ""
			if !s.Reported.IsZero() {
				when = ", reported " + s.Reported.Format("2 Jan 2006")
			}
			parts = append(parts, fmt.Sprintf("quarter to %s%s: %.2f a share against %.2f expected (%+.1f%%)",
				s.Period, when, s.EPS, s.Expected, s.Percent))
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString("\n")
	}
	if !r.NextResults.IsZero() {
		fmt.Fprintf(&b, "- Next results: %s", r.NextResults.Format("Monday 2 Jan 2006"))
		if r.ResultsWhen != "" {
			b.WriteString(", " + r.ResultsWhen)
		}
		if r.ResultsEstimated {
			b.WriteString(". Not yet announced: Zacks' estimate from the company's past reporting days, which can be out by a week or more.\n")
		} else {
			b.WriteString(", as the company has announced it.\n")
		}
	}
	if r.InsiderBuys12+r.InsiderSells12 > 0 {
		fmt.Fprintf(&b, "- Insiders, last 3 months: open-market buys %d, sales %d, %s. Last 12 months: buys %d, sales %d, %s.\n",
			r.InsiderBuys3, r.InsiderSells3, net(r.InsiderNet3),
			r.InsiderBuys12, r.InsiderSells12, net(r.InsiderNet12))
	}
	if r.ShortShares > 0 {
		fmt.Fprintf(&b, "- Short interest: %s shares on %s", shares(r.ShortShares), r.ShortAsOf.Format("2 Jan 2006"))
		if r.ShortPrior > 0 {
			fmt.Fprintf(&b, " (%s at the settlement before)", shares(r.ShortPrior))
		}
		fmt.Fprintf(&b, ", %.1f days of normal trading to buy back.\n", r.DaysToCover)
	}
	if r.Institutional > 0 || r.FundsAdded+r.FundsCut > 0 {
		fmt.Fprintf(&b, "- Funds: institutions hold %.1f%% of the shares. In their latest quarterly filings %s added to their holdings (%s shares) and %s cut them (%s shares); %s opened a position and %s sold out.\n",
			r.Institutional, count(r.FundsAdded), shares(r.SharesAddedBy), count(r.FundsCut), shares(r.SharesCutBy), count(r.FundsOpened), count(r.FundsSoldOut))
	}
	if len(r.Missing) > 0 {
		fmt.Fprintf(&b, "Not available: %s.\n", strings.Join(r.Missing, ", "))
	}
	return b.String()
}

func estimates(list []Estimate, price float64) string {
	var parts []string
	for _, e := range list {
		s := fmt.Sprintf("period to %s %.2f (%d analysts, range %.2f to %.2f; in the last 4 weeks %d raised, %d cut)",
			e.Period, e.EPS, e.Analysts, e.Low, e.High, e.Up, e.Down)
		if price > 0 && e.EPS > 0 {
			s += fmt.Sprintf(", which today's price is %.1f times", price/e.EPS)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func distance(target, price float64) string {
	pct := (target/price - 1) * 100
	if pct >= 0 {
		return fmt.Sprintf("%.0f%% above", pct)
	}
	return fmt.Sprintf("%.0f%% below", -pct)
}

func net(n float64) string {
	switch {
	case n > 0:
		return "net " + shares(n) + " shares bought"
	case n < 0:
		return "net " + shares(-n) + " shares sold"
	}
	return "no net change"
}

func count(n int) string { return commas(fmt.Sprint(n)) }

func money(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	whole, frac, _ := strings.Cut(s, ".")
	return commas(whole) + "." + frac
}

func shares(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return fmt.Sprintf("%.2fbn", v/1e9)
	case a >= 1e6:
		return fmt.Sprintf("%.1fm", v/1e6)
	}
	return commas(fmt.Sprintf("%.0f", v))
}

func commas(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
