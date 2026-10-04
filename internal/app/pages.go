package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/pages"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// Since 2026-10-01, at the owner's request, everything long the service
// sends -- the brief, the closer look, an analysis, an industry -- can go out
// as a summary with a button to the whole thing as a web page, served by the
// service itself (internal/pages). PAGES_URL turns it on. Without it, or when
// a page cannot be written, the messages are sent in full as before, so a
// fault in the pages costs the reader the layout and never the content.

// Publisher keeps a page and says where it can be read.
type Publisher interface {
	Publish(p pages.Page) (string, error)
}

// outgoing is one thing to send: its messages in full, and the summary sent
// in their place when they are a page, laid out as doc.
type outgoing struct {
	title    string // the page's
	messages []string
	summary  telegram.Summary // no text: always sent in full
	doc      *pages.Doc       // none: the page is laid out from the messages
}

// send delivers to a chat or the channel. A broadcast is quiet after its
// first message, as a channel's posts are.
func (a *App) send(ctx context.Context, chatID int64, out outgoing, broadcast bool) ([]int64, error) {
	if a.Pages != nil && out.summary.Text != "" && telegram.Fits(out.summary.Text) {
		id, err := a.sendLinked(ctx, chatID, out.summary.Text, out)
		if err == nil {
			return []int64{id}, nil
		}
		a.Log.Warn("could not send a summary and a page; sending in full", "page", out.title, "error", err)
	}
	a.sending.Lock()
	defer a.sending.Unlock()
	if broadcast {
		return a.Bot.Broadcast(ctx, chatID, out.messages)
	}
	return a.Bot.SendReport(ctx, chatID, out.messages)
}

// sendTogether posts several things to the channel as one message: their
// summaries one after another, and under them a button to each one's page.
// The owner asked for the daily brief and its closer look to arrive that way
// (2026-10-01), as one post rather than a summary each. When they cannot --
// pages off, one with no summary, or summaries too long for one message
// together -- each goes as send would post it on its own.
func (a *App) sendTogether(ctx context.Context, chatID int64, outs ...outgoing) ([]int64, error) {
	if len(outs) > 1 && a.Pages != nil {
		var texts []string
		for _, out := range outs {
			texts = append(texts, out.summary.Text)
		}
		text := strings.Join(texts, "\n\n")
		if !slices.Contains(texts, "") && telegram.Fits(text) {
			id, err := a.sendLinked(ctx, chatID, text, outs...)
			if err == nil {
				return []int64{id}, nil
			}
			a.Log.Warn("could not send as one message; sending each on its own", "error", err)
		} else {
			a.Log.Info("sending each on its own: a summary is missing or they are too long together", "runes", len([]rune(text)))
		}
	}
	var ids []int64
	for _, out := range outs {
		sent, err := a.send(ctx, chatID, out, true)
		ids = append(ids, sent...)
		if err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// sendLinked publishes each page and sends text with a button to each.
func (a *App) sendLinked(ctx context.Context, chatID int64, text string, outs ...outgoing) (int64, error) {
	var links []telegram.Link
	for _, out := range outs {
		url, err := a.Pages.Publish(pages.Page{
			Title:       out.title,
			Description: pages.Plain(out.summary.Text),
			Doc:         out.doc,
			Messages:    out.messages,
		})
		if err != nil {
			return 0, err
		}
		links = append(links, telegram.Link{Label: out.summary.Button, URL: url})
	}
	return a.Bot.SendLinked(ctx, chatID, text, links, false)
}

// gaugeNames are the brief's readings as a table row names them: the
// readings' own labels are written for a model, at a sentence's length.
var gaugeNames = map[string]string{
	"SP500":    "S&P 500",
	"VIXCLS":   "VIX, expected swings",
	"DGS10":    "10-year Treasury yield",
	"DGS2":     "2-year Treasury yield",
	"T10Y2Y":   "10-year less 2-year",
	"DFF":      "Fed funds rate",
	"CPIAUCSL": "Inflation, a year (CPI)",
	"CPILFESL": "Core inflation, a year",
}

// gaugeOrder puts the market first, then the rates, then inflation.
var gaugeOrder = []string{"SP500", "VIXCLS", "DGS10", "DGS2", "T10Y2Y", "DFF", "CPIAUCSL", "CPILFESL"}

// pageMarket is what the brief's page shows of the markets: the readings
// the brief was written against, and the benchmark funds' last session.
func pageMarket(levels []prices.Reading, quotes []model.Quote) telegram.Market {
	var m telegram.Market
	byID := map[string]prices.Reading{}
	for _, r := range levels {
		byID[r.ID] = r
	}
	for _, id := range gaugeOrder {
		if r, ok := byID[id]; ok {
			m.Gauges = append(m.Gauges, gauge(r))
		}
	}
	bySymbol := map[string]model.Quote{}
	for _, q := range quotes {
		bySymbol[q.Symbol] = q
	}
	for _, b := range prices.Benchmarks {
		if q, ok := bySymbol[b.Symbol]; ok && q.Price > 0 {
			name, _, _ := strings.Cut(b.Label, " (")
			m.Funds = append(m.Funds, telegram.Fund{Name: name, Quote: q})
		}
	}
	return m
}

// gauge writes a reading out: a rate's moves in percentage points, an
// index's in percent.
func gauge(r prices.Reading) telegram.Gauge {
	g := telegram.Gauge{Name: gaugeNames[r.ID], Day: "–", Week: "–", AsOf: r.AsOf.Format("2 Jan")}
	if g.Name == "" {
		g.Name = r.Label
	}
	if r.Monthly {
		g.AsOf = r.AsOf.Format("Jan 2006")
	}
	move := func(from float64) string {
		if r.Unit == "%" {
			return fmt.Sprintf("%+.2f", r.Latest-from)
		}
		if from == 0 {
			return "–"
		}
		return fmt.Sprintf("%+.1f%%", 100*(r.Latest-from)/from)
	}
	switch {
	case r.Unit == "%":
		g.Level = fmt.Sprintf("%.2f%%", r.Latest)
	case r.Latest >= 1000:
		g.Level = humanize(r.Latest)
	default:
		g.Level = fmt.Sprintf("%.2f", r.Latest)
	}
	if r.HasPrevious {
		g.Day = move(r.Previous)
	}
	if r.HasWeekAgo && !r.Monthly {
		g.Week = move(r.WeekAgo)
	}
	return g
}

// humanize writes 7651.54 as "7,651.54".
func humanize(v float64) string {
	whole, frac, _ := strings.Cut(fmt.Sprintf("%.2f", v), ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String() + "." + frac
}

// pageAccounts is what an analysis's page shows of the accounts: each
// period's reported figures, and the share's price.
func pageAccounts(s fundamentals.Snapshot) telegram.Accounts {
	// months is the period's length, to name the one a year before it.
	period := func(y fundamentals.Year, months int) telegram.Period {
		p := telegram.Period{Label: y.Label, End: y.End, Figures: map[string]float64{}, Release: y.FromRelease}
		if y.YearAgoRevenue.Known {
			p.YearAgo, p.YearAgoLabel = y.YearAgoRevenue.Amount, fundamentals.MonthSpan(y.End.AddDate(-1, 0, 0), months)
		}
		for k, v := range y.Figures {
			if v.Known {
				p.Figures[k] = v.Amount
			}
		}
		return p
	}
	acc := telegram.Accounts{Currency: s.Currency, AsOf: s.Balance.AsOf, Price: s.Price, Trading: s.Trading}
	if s.ReleaseAdded {
		acc.Release = s.ReleaseFrom
	}
	g := s.Glance()
	acc.Glance = &telegram.Glance{MarketCap: g.MarketCap.Amount, PE: g.PE.Amount, PS: g.PS.Amount, On: g.On,
		ForwardPE: g.ForwardPE.Amount, ForwardFor: g.ForwardFor,
		Price: g.Price.Amount, Shares: g.Shares.Amount, EPS: g.EPS.Amount, Sales: g.Sales.Amount, ExpectedEPS: g.ExpectedEPS.Amount,
		Cash: g.Cash.Amount, Debt: g.Debt.Amount, HasCash: g.Cash.Known, WithInvestments: g.WithInvestments, HasDebt: g.Debt.Known, CashAt: g.CashAt, DebtAt: g.DebtAt}
	if len(s.Balance.Figures) > 0 {
		acc.Balance = &telegram.BalanceSheet{AsOf: s.Balance.AsOf, Release: s.Balance.FromRelease, Figures: map[string]float64{}, Older: s.Balance.Older}
		for k, v := range s.Balance.Figures {
			if v.Known {
				acc.Balance.Figures[k] = v.Amount
			}
		}
	}
	for _, y := range s.Years {
		acc.Years = append(acc.Years, period(y, 12))
	}
	for _, q := range s.Quarters {
		acc.Quarters = append(acc.Quarters, period(q, 3))
	}
	if s.TTM != nil {
		ttm := period(*s.TTM, 12)
		acc.TTM = &ttm
	}
	if g := s.Peers; g != nil {
		own := "This company's are its own, as above"
		if g.Own != "" {
			own = "This company's are " + g.Own + ", as above"
		}
		acc.Peers = &telegram.Peers{About: fmt.Sprintf("<i>Nasdaq's %s group: %d other US-listed companies worth US$500m or more. Its largest are %s. Each is on its latest 12 months filed with the SEC, and its P/E and price to sales on today's market value. %s. The free cash flow margin is on each company's year nearest %d, as cash flows are filed only year to date.</i>",
			escape(g.Industry), g.Size, escape(strings.Join(g.Largest, ", ")), escape(own), g.Year)}
		for _, l := range g.Lines {
			row := telegram.PeerRow{Label: l.Label, Company: "not reported", Median: l.Show(l.Median),
				Range: l.Show(l.Low) + " to " + l.Show(l.High), Above: "—"}
			if l.Reported {
				row.Company, row.Above = l.Show(l.Company), fmt.Sprintf("%d of %d", l.Below, l.Count)
			}
			acc.Peers.Rows = append(acc.Peers.Rows, row)
		}
	}
	return acc
}
