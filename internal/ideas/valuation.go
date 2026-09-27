package ideas

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/joseph1009/market-watch/internal/fundamentals"
)

// Multiples are a company's cost against its earnings and sales, and its
// growth: see fundamentals.Multiples.
type Multiples = fundamentals.Multiples

// Valuation is a company's multiples beside its theme's and its own
// history's.
type Valuation struct {
	Now Multiples

	// Theme is the median of the other companies in its theme, and Peers
	// how many of them had figures; Own the median of its own last five
	// fiscal years.
	Theme Multiples
	Peers int
	Own   Multiples

	// Flags are the warning signs that the price may already assume more
	// than the business can deliver.
	Flags []string
}

// minPeers is how many companies a theme's median needs.
const minPeers = 3

// Medians is the median of each multiple across companies, each over those
// that have it, and zero where fewer than three do. GrowthPS's median is kept
// in Growth's place, with HasGrowth set, so a comparison can be made against
// it.
func Medians(all []Multiples) Multiples {
	pick := func(of func(Multiples) float64) float64 {
		var vs []float64
		for _, m := range all {
			if v := of(m); v > 0 && !math.IsInf(v, 0) {
				vs = append(vs, v)
			}
		}
		if len(vs) < minPeers {
			return 0
		}
		sort.Float64s(vs)
		if len(vs)%2 == 1 {
			return vs[len(vs)/2]
		}
		return (vs[len(vs)/2-1] + vs[len(vs)/2]) / 2
	}
	out := Multiples{
		PE:     pick(func(m Multiples) float64 { return m.PE }),
		PS:     pick(func(m Multiples) float64 { return m.PS }),
		EVEBIT: pick(func(m Multiples) float64 { return m.EVEBIT }),
	}
	if g := pick(func(m Multiples) float64 { return m.GrowthPS() }); g > 0 {
		out.Growth, out.HasGrowth = g, true
	}
	return out
}

// measures are the multiples a company is compared on, each against its
// theme's median where there is one and its own history's otherwise.
func (v Valuation) measures() []struct {
	name     string
	now, ref float64
	against  string
} {
	type m = struct {
		name     string
		now, ref float64
		against  string
	}
	var out []m
	for _, x := range []struct {
		name            string
		now, theme, own float64
	}{
		{"price to earnings", v.Now.PE, v.Theme.PE, v.Own.PE},
		{"price to sales", v.Now.PS, v.Theme.PS, v.Own.PS},
		{"company value to operating profit", v.Now.EVEBIT, v.Theme.EVEBIT, v.Own.EVEBIT},
	} {
		switch {
		case x.now <= 0:
		case x.theme > 0:
			out = append(out, m{x.name, x.now, x.theme, "its theme"})
		case x.own > 0:
			out = append(out, m{x.name, x.now, x.own, "its own five years"})
		}
	}
	return out
}

// BuyClosed says why a BUY is not open to the company, or "" where it is.
//
// Two rules, both the investor's. A BUY must be cheaper than its theme on at
// least one measure, or growing fast enough to pay for being dearer: its
// price to sales for each point of growth no higher than the theme's. And a
// company dearer than its theme on every measure, with two or more warning
// signs, is no BUY whatever its growth. A company with nothing to compare --
// no accounts, or no theme and no history -- is held to neither; its
// confidence is held down instead.
func (v Valuation) BuyClosed() string {
	measures := v.measures()
	if len(measures) == 0 {
		return ""
	}
	cheaper := false
	var dearer []string
	for _, m := range measures {
		if m.now < m.ref {
			cheaper = true
		} else {
			dearer = append(dearer, fmt.Sprintf("%s %.1f against %.1f for %s", m.name, m.now, m.ref, m.against))
		}
	}
	growthPaid := v.Now.GrowthPS() > 0 && v.Theme.HasGrowth && v.Now.GrowthPS() <= v.Theme.Growth
	allDearer := len(dearer) == len(measures)
	switch {
	case allDearer && len(measures) >= 2 && len(v.Flags) >= 2:
		return fmt.Sprintf("it is dearer than its comparison on every measure (%s) and shows %d warning signs", strings.Join(dearer, "; "), len(v.Flags))
	case !cheaper && !growthPaid:
		return fmt.Sprintf("it is dearer than its comparison on every measure (%s) and its growth does not pay for the difference", strings.Join(dearer, "; "))
	}
	return ""
}

// Facts writes the valuation out for the verdict.
func (v Valuation) Facts() string {
	var b strings.Builder
	b.WriteString("Valuation against its theme and its own history. Multiples use the market value from Nasdaq and the latest twelve months of filed figures, converted to US dollars; its own history scales today's market value by the share price at each year end, so a company that has bought back or issued many shares reads approximately.\n")
	row := func(name string, now, theme, own float64, format string) {
		if now <= 0 && theme <= 0 && own <= 0 {
			return
		}
		fmt.Fprintf(&b, "- %s: %s", name, orNA(now, format))
		if theme > 0 {
			fmt.Fprintf(&b, "; its theme's median %s", fmt.Sprintf(format, theme))
		}
		if own > 0 {
			fmt.Fprintf(&b, "; its own five-year median %s", fmt.Sprintf(format, own))
		}
		b.WriteString("\n")
	}
	row("Price to earnings", v.Now.PE, v.Theme.PE, v.Own.PE, "%.1f")
	row("Price to sales", v.Now.PS, v.Theme.PS, v.Own.PS, "%.1f")
	row("Company value (market value plus debt less cash) to operating profit", v.Now.EVEBIT, v.Theme.EVEBIT, v.Own.EVEBIT, "%.1f")
	if v.Now.HasGrowth {
		fmt.Fprintf(&b, "- Revenue growth, latest year: %s", pct(v.Now.Growth))
		if g := v.Now.GrowthPS(); g > 0 {
			fmt.Fprintf(&b, "; price to sales for each point of it %.2f", g)
			if v.Theme.HasGrowth {
				fmt.Fprintf(&b, ", against its theme's median %.2f", v.Theme.Growth)
			}
		}
		b.WriteString("\n")
	}
	if v.Peers > 0 {
		fmt.Fprintf(&b, "The theme's medians are over %d companies with figures.\n", v.Peers)
	}
	if len(v.Flags) > 0 {
		b.WriteString("Warning signs that the price may assume more than the business can deliver: " + strings.Join(v.Flags, "; ") + ".\n")
	}
	if why := v.BuyClosed(); why != "" {
		b.WriteString("BUY is not open to this company: " + why + ". A BUY given to it will be turned into a HOLD.\n")
	}
	return b.String()
}

func orNA(v float64, format string) string {
	if v <= 0 {
		return "not meaningful (a loss, or no figure)"
	}
	return fmt.Sprintf(format, v)
}

// WarningSigns are the signs that a share's price may already assume more
// than the business can deliver: far above its long average, above what
// analysts think it is worth, insiders selling a meaningful part of it, or
// many investors betting against it. Each is left out where its figures are
// missing.
func WarningSigns(price, ma200, target, insiderNet3, shortShares, sharesOut float64) []string {
	var out []string
	if price > 0 && ma200 > 0 && price > 1.4*ma200 {
		out = append(out, fmt.Sprintf("%s above its 200-day average", pct(price/ma200-1)))
	}
	if price > 0 && target > 0 && price > target {
		out = append(out, fmt.Sprintf("above the average analyst target of %.2f", target))
	}
	if sharesOut > 0 && insiderNet3 < 0 && -insiderNet3 >= 0.0025*sharesOut {
		out = append(out, fmt.Sprintf("insiders sold a net %.2f%% of the shares in three months", -100*insiderNet3/sharesOut))
	}
	if sharesOut > 0 && shortShares >= 0.10*sharesOut {
		out = append(out, fmt.Sprintf("%.0f%% of the shares sold short", 100*shortShares/sharesOut))
	}
	return out
}
