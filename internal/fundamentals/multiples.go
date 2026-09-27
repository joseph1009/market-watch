package fundamentals

import (
	"math"
	"time"
)

// Multiples are what a company costs against what it earns and sells, and
// how fast it is growing. A multiple is zero where it cannot be worked out:
// no figure, or a loss, which makes a price-to-earnings mean nothing.
//
// They are reckoned from the market value of the whole company in US dollars
// -- Nasdaq's, for a US listing -- rather than from a share price, so a
// foreign filer's depositary shares, each standing for some number of its
// ordinary shares, cost nothing to get right: the accounts are converted to
// dollars at the day's rate and set against the market value as a whole.
type Multiples struct {
	PE, PS, EVEBIT float64

	// Growth is the latest year's revenue growth, as a fraction, where
	// HasGrowth.
	Growth    float64
	HasGrowth bool
}

// GrowthPS is price to sales for each point of growth: what a share costs
// against how fast its sales are growing. Zero where it is not growing.
func (m Multiples) GrowthPS() float64 {
	if !m.HasGrowth || m.Growth <= 0 || m.PS <= 0 {
		return 0
	}
	return m.PS / (100 * m.Growth)
}

// Trailing is a line's last twelve months where the interim figures allow --
// the last full year, less the part of it the interim a year earlier covered,
// plus the interim since -- and the last full year otherwise.
func (s Snapshot) Trailing(key string) Value {
	if len(s.Years) == 0 {
		return Value{}
	}
	annual := s.Years[0].Figure(key)
	if s.YTD != nil && s.PriorYTD != nil {
		if rolled := Add(Less(annual, s.PriorYTD.Figure(key)), s.YTD.Figure(key)); rolled.Known {
			return rolled
		}
	}
	return annual
}

// Growth is the latest revenue growth: the year so far against the same
// stretch of the year before where there is an interim, and the last full
// year against the one before otherwise.
func (s Snapshot) Growth() (float64, bool) {
	columns := s.columns()
	if len(columns) == 0 {
		return 0, false
	}
	g := s.revenueGrowth(columns, 0)
	if !g.Known || math.IsNaN(g.Amount) || math.IsInf(g.Amount, 0) {
		return 0, false
	}
	return g.Amount, true
}

// Multiples are the company's at a market value in US dollars, with usd
// what one unit of its reporting currency is worth in dollars.
func (s Snapshot) Multiples(marketCap, usd float64) Multiples {
	if marketCap <= 0 || usd <= 0 {
		return Multiples{}
	}
	m := Multiples{}
	m.Growth, m.HasGrowth = s.Growth()
	earn := s.Trailing("netIncome")
	sales := s.Trailing("revenue")
	operating := s.Trailing("operatingIncome")
	if earn.Known && earn.Amount > 0 {
		m.PE = marketCap / (earn.Amount * usd)
	}
	if sales.Known && sales.Amount > 0 {
		m.PS = marketCap / (sales.Amount * usd)
	}
	if operating.Known && operating.Amount > 0 {
		// Debt the balance sheet does not report is taken as none, and so is
		// cash: a missing line moves the multiple less than dropping it would.
		debt := s.Balance.Figure("longTermDebt").Amount
		cash := s.Balance.Figure("cash").Amount + s.Balance.Figure("marketableSecurities").Amount
		if ev := marketCap + (debt-cash)*usd; ev > 0 {
			m.EVEBIT = ev / (operating.Amount * usd)
		}
	}
	return m
}

// PastMultiples are the company's price to earnings and price to sales at
// the end of each full year it reported, from today's market value scaled by
// the share's price then against its price now, priceAt reading the price on
// a day. A buyback or an issue of shares since makes these approximate; they
// say whether a multiple is high for this company, not what it was to the
// decimal.
func (s Snapshot) PastMultiples(marketCap, priceNow float64, priceAt func(time.Time) float64, usd float64) []Multiples {
	if marketCap <= 0 || priceNow <= 0 || usd <= 0 || priceAt == nil {
		return nil
	}
	var out []Multiples
	for _, y := range s.Years {
		then := priceAt(y.End)
		if then <= 0 {
			continue
		}
		value := marketCap * then / priceNow
		m := Multiples{}
		if e := y.Figure("netIncome"); e.Known && e.Amount > 0 {
			m.PE = value / (e.Amount * usd)
		}
		if r := y.Figure("revenue"); r.Known && r.Amount > 0 {
			m.PS = value / (r.Amount * usd)
		}
		if m.PE > 0 || m.PS > 0 {
			out = append(out, m)
		}
	}
	return out
}
