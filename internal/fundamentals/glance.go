package fundamentals

import (
	"strings"
	"time"
)

// Glance is the handful of figures a reader looks for first: what the
// company is worth, what its share costs against its earnings, sales and the
// next year's expected earnings, and its cash against its debt. The owner
// listed them on 4 October 2026, and the analysis's page shows them in a box
// of their own. A figure that cannot be worked out is unknown.
type Glance struct {
	MarketCap Value

	PE, PS Value
	On     string // the period PE and PS are on: "the year to 3 Sep 2026"

	ForwardPE  Value
	ForwardFor string // the year its expected earnings are for: "Aug 2027"

	// Cash is cash and short-term investments, and Debt long-term debt, at
	// the balance sheet's date, or each at its own where it is older.
	Cash, Debt     Value
	CashAt, DebtAt time.Time
}

// Glance works out the figures a reader looks for first. The multiples need
// a price in the currency the accounts are in.
func (s Snapshot) Glance() Glance {
	g := Glance{
		Cash:   Add(s.Balance.Figure("cash"), s.Balance.Figure("marketableSecurities")),
		Debt:   s.Balance.Figure("longTermDebt"),
		CashAt: s.Balance.AsOf,
		DebtAt: s.Balance.AsOf,
	}
	if !s.Balance.Figure("marketableSecurities").Known {
		g.Cash = s.Balance.Figure("cash")
	}
	for _, key := range []string{"cash", "marketableSecurities"} {
		if at, ok := s.Balance.Older[key]; ok && at.Before(g.CashAt) {
			g.CashAt = at
		}
	}
	if at, ok := s.Balance.Older["longTermDebt"]; ok {
		g.DebtAt = at
	}

	if s.Price == nil || s.currency() != "USD" {
		return g
	}
	price := known(s.Price.Price)
	g.MarketCap = multiply(price, s.Balance.Figure("sharesOutstanding"))
	if len(s.Years) > 0 {
		earnings, _, basis := s.trailing()
		if earnings.Known && earnings.Amount > 0 {
			g.PE = Ratio(price, earnings)
		}
		if sales := s.Trailing("revenue"); sales.Known && sales.Amount > 0 {
			g.PS = Ratio(g.MarketCap, sales)
		}
		g.On = strings.TrimSuffix(strings.Replace(basis, "FY to", "the year to", 1), " earnings")
	}
	if s.ExpectedEPS > 0 {
		g.ForwardPE, g.ForwardFor = known(s.Price.Price/s.ExpectedEPS), s.ExpectedFor
	}
	return g
}
