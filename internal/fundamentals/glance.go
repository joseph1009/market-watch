package fundamentals

import "time"

// Glance is the handful of figures a reader looks for first: what the
// company is worth, what its share costs against its earnings, sales and the
// next year's expected earnings, and its cash against its debt. The owner
// listed them on 4 October 2026, and the analysis's page shows them in a box
// of their own. A figure that cannot be worked out is unknown.
type Glance struct {
	MarketCap Value

	PE, PS Value
	On     string // the months PE and PS are on: "Sep 2025–Aug 2026"

	ForwardPE  Value
	ForwardFor string // the months its expected earnings are for: "Sep 2026–Aug 2027"

	// What the figures are worked out from, so the page can show the sum:
	// the price, the shares, the earnings a share and the sales on On, and
	// the earnings a share analysts expect for ForwardFor.
	Price, Shares, EPS, Sales, ExpectedEPS Value

	// Cash is cash, with short-term investments where WithInvestments, and
	// Debt long-term debt, each at its own date.
	Cash, Debt      Value
	CashAt, DebtAt  time.Time
	WithInvestments bool
}

// Glance works out the figures a reader looks for first. The multiples need
// a price in the currency the accounts are in.
func (s Snapshot) Glance() Glance {
	g := Glance{Debt: s.Balance.Figure("longTermDebt"), DebtAt: s.Balance.At("longTermDebt")}
	g.Cash, g.CashAt, g.WithInvestments = s.Balance.CashPot()

	if s.Price == nil || s.currency() != "USD" {
		return g
	}
	price := known(s.Price.Price)
	g.Price, g.Shares = price, s.Balance.Figure("sharesOutstanding")
	g.MarketCap = multiply(price, g.Shares)
	if len(s.Years) > 0 {
		earnings, _, basis := s.trailing()
		if earnings.Known && earnings.Amount > 0 {
			g.PE, g.EPS = Ratio(price, earnings), earnings
		}
		if sales := s.Trailing("revenue"); sales.Known && sales.Amount > 0 {
			g.PS, g.Sales = Ratio(g.MarketCap, sales), sales
		}
		g.On = basis
	}
	if s.ExpectedEPS > 0 {
		g.ForwardPE, g.ForwardFor = known(s.Price.Price/s.ExpectedEPS), YearEnding(s.ExpectedFor)
		g.ExpectedEPS = known(s.ExpectedEPS)
	}
	return g
}
