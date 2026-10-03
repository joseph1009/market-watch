package telegram

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/pages"
)

// The pages: each thing the chat is sent a summary of, laid out as a
// document from what it was written from (see internal/pages). The prose is
// the messages' prose; what a page adds is what a chat cannot draw: tables,
// charts, a card a company, the sources beside what they back.

// Gauge is one market reading for the brief's page, written out: "5.26%",
// "+0.02".
type Gauge struct {
	Name, Level, Day, Week, AsOf string
}

// Fund is one market's fund and its last session.
type Fund struct {
	Name  string // "S&P 500"
	Quote model.Quote
}

// Market is the brief's markets: the readings it was written against, and
// the funds that track each corner of the market.
type Market struct {
	Gauges []Gauge
	Funds  []Fund
}

// BriefDoc is the brief as a page: the day's line and the few bullets, the
// markets in a table and a chart, the overview, what is coming up, and each
// sector with its biggest moves and its sources.
func BriefDoc(rep model.Report, market Market, opts Options) pages.Doc {
	display := opts.Display
	if display == nil {
		display = time.UTC
	}
	doc := pages.Doc{
		Kicker: "Market Watch",
		Title:  pages.Plain(headline(rep.Overview)),
		Dek: []string{
			escape(rep.GeneratedAt.In(display).Format("Monday 2 January 2006") + " @ " +
				rep.GeneratedAt.In(display).Format("15:04") + " " + placeName(display) + " time"),
		},
	}
	if doc.Title == "" {
		doc.Title = "The day in the markets"
	}

	var short []string
	for _, b := range summaryBullets(rep.Summary) {
		short = append(short, strings.TrimPrefix(b, "• "))
	}
	doc.Parts = append(doc.Parts, pages.Box{Title: "In short", Items: short})

	if markets := marketsSection(market); len(markets.Parts) > 0 {
		doc.Parts = append(doc.Parts, markets)
	}

	if body := withoutHeadline(rep.Overview); body != "" {
		var parts []pages.Part
		if len(rep.MarketMoves) > 0 {
			parts = append(parts, moveChips("Across the market", model.MarketQuotes(rep.MarketMoves)))
		}
		parts = append(parts, pages.Prose(linkTerms(cite(paragraphs(body), rep.Cited), opts.Terms)))
		doc.Parts = append(doc.Parts, pages.Section{ID: "overview", Title: "Overview", Parts: parts})
	}

	if coming := comingUp(rep.Calendar, rep.GeneratedAt, display); len(coming) > 0 {
		doc.Parts = append(doc.Parts, pages.Section{ID: "coming-up", Title: "Coming up",
			Lead:  "Times in " + escape(placeName(display)) + ", with New York's beside them. Forecasts are the calendars' consensus.",
			Parts: coming})
	}

	for _, s := range rep.Sections {
		title := s.GroupName
		if s.Emoji != "" {
			title = s.Emoji + " " + title
		}
		sec := pages.Section{ID: anchorID(s.GroupID), Title: title}
		if len(s.Movers) > 0 {
			sec.Parts = append(sec.Parts, moveChips("Biggest moves", s.Movers))
		}
		if s.Body != "" {
			sec.Parts = append(sec.Parts, pages.Prose(linkTerms(cite(paragraphs(s.Body), rep.Cited), opts.Terms)))
		}
		if links := sourceLinks(s.Articles, rep.Cited, opts.Sources); len(links) > 0 {
			sec.Parts = append(sec.Parts, pages.Sources{Title: "Sources for " + s.GroupName, Links: links})
		}
		doc.Parts = append(doc.Parts, sec)
	}

	if names := newNames(rep.Candidates, rep.Cited); names != nil {
		doc.Parts = append(doc.Parts, *names)
	}
	if len(rep.QuietGroups) > 0 {
		doc.Parts = append(doc.Parts, pages.Section{ID: "quiet", Title: "Quiet today", Parts: []pages.Part{
			pages.Para(escape(strings.Join(rep.QuietGroups, ", ")) + " — nothing that warranted a section."),
		}})
	}
	if opts.Sources == SourcesFull {
		if links := sourceLinks(rep.General, rep.Cited, SourcesFull); len(links) > 0 {
			doc.Parts = append(doc.Parts, pages.Sources{Title: generalCategory, Links: links})
		}
	}
	doc.Footer = strings.Split(renderFooter(rep), "\n")
	return doc
}

// marketsSection is the markets in figures: the readings in a table, the
// funds in a chart.
func marketsSection(m Market) pages.Section {
	sec := pages.Section{ID: "markets", Title: "The markets"}
	if len(m.Gauges) > 0 {
		t := pages.Table{Head: []string{"", "Level", "Day", "Week", "As of"}, Align: "lrrrr"}
		for _, g := range m.Gauges {
			t.Rows = append(t.Rows, []string{escape(g.Name), "<b>" + escape(g.Level) + "</b>",
				escape(g.Day), escape(g.Week), escape(g.AsOf)})
		}
		t.Note = "<i>From FRED, the Federal Reserve Bank of St. Louis. A day's change in a rate is in percentage points; a monthly figure's \"day\" is the month before.</i>"
		sec.Parts = append(sec.Parts, t)
	}
	if len(m.Funds) > 0 {
		bars := pages.Bars{Title: "How each corner of the market moved on the last session"}
		for _, f := range m.Funds {
			bars.Items = append(bars.Items, pages.Bar{Label: f.Name, Value: f.Quote.Percent, Text: f.Quote.Move()})
		}
		bars.Note = "<i>Funds that track each market or sector, so close to it but not the index itself.</i>"
		sec.Parts = append(sec.Parts, bars)
	}
	return sec
}

// withoutHeadline is the overview after its opening line, which heads the
// page.
func withoutHeadline(overview string) string {
	overview = strings.TrimSpace(overview)
	if headline(overview) == "" {
		return overview
	}
	_, rest, _ := strings.Cut(overview, "\n")
	return strings.TrimSpace(rest)
}

// comingUp is the calendar as tables, a day each: the releases with their
// figures, high impact marked, then the results due with what analysts
// expect, a followed company marked.
func comingUp(cal model.Calendar, now time.Time, display *time.Location) []pages.Part {
	if cal.Empty() {
		return nil
	}
	type day struct {
		label    string
		releases pages.Table
		results  pages.Table
	}
	var order []string
	days := map[string]*day{}
	get := func(t time.Time) *day {
		key := t.Format(time.DateOnly)
		if d, ok := days[key]; ok {
			return d
		}
		label := t.Format("Monday 2 January")
		if key == now.In(t.Location()).Format(time.DateOnly) {
			label = "Today, " + label
		}
		days[key] = &day{label: label,
			releases: pages.Table{Head: []string{placeName(display), "New York", "Release", "Impact", "Forecast", "Previous"}, Align: "llllrr"},
			results:  pages.Table{Head: []string{"Company", "", "When", "Expected EPS", "A year ago"}, Align: "lllrr"},
		}
		order = append(order, key)
		return days[key]
	}
	for _, e := range cal.Events {
		d := get(e.At)
		title := escape(e.Title)
		if e.Country != "USD" {
			title = escape(e.Country) + " · " + title
		}
		d.releases.Rows = append(d.releases.Rows, []string{"<b>" + e.At.In(display).Format("15:04") + "</b>", e.At.Format("15:04"),
			title, escape(e.Impact), "<b>" + escape(e.Forecast) + "</b>", escape(e.Previous)})
		d.releases.Marked = append(d.releases.Marked, e.Impact == "High")
	}
	for _, r := range cal.Earnings {
		d := get(r.Day)
		d.results.Rows = append(d.results.Rows, []string{escape(shortName(r.Name)), "<code>" + escape(r.Symbol) + "</code>",
			escape(r.When), "<b>" + escape(r.Forecast) + "</b>", escape(r.LastYear)})
		d.results.Marked = append(d.results.Marked, r.Followed)
	}
	sort.Strings(order)
	var parts []pages.Part
	for i, key := range order {
		d := days[key]
		if len(d.releases.Rows) > 0 {
			d.releases.Caption = d.label
			if i == 0 {
				d.releases.Note = "<i>Highlighted: high impact, the releases most likely to move markets.</i>"
			}
			parts = append(parts, d.releases)
		}
		if len(d.results.Rows) > 0 {
			d.results.Caption = "Results · " + d.label
			d.results.Note = "<i>Highlighted: a company on your watchlists.</i>"
			parts = append(parts, d.results)
		}
	}
	return parts
}

// newNames is the companies the news kept mentioning that no watchlist
// tracks, as a table.
func newNames(candidates []model.Candidate, cited []model.Article) *pages.Section {
	if len(candidates) == 0 {
		return nil
	}
	t := pages.Table{Head: []string{"Company", "", "Last session", "Why it was in the news"}, Align: "llrl"}
	for _, c := range candidates {
		symbol := "<i>ticker unverified</i>"
		switch {
		case c.Symbol() != "":
			symbol = flagged(c.Symbol(), c.Exchange)
		case c.Private:
			symbol = "<i>private</i>"
		}
		move := ""
		if c.Quote != nil {
			move = escape(c.Quote.Move())
		}
		why := escape(c.Why)
		for _, a := range c.Articles {
			if n := citationNumber(a, cited); n > 0 {
				why += fmt.Sprintf(" <a href=\"%s\">[%d]</a>", escape(a.URL), n)
			}
		}
		t.Rows = append(t.Rows, []string{"<b>" + escape(c.Name) + "</b>", symbol, move, why})
	}
	return &pages.Section{ID: "new-names", Title: "New names in the news",
		Lead:  "Companies today's stories were about that none of your watchlists track. Not recommendations.",
		Parts: []pages.Part{t}}
}

// sourceLinks are a part's articles as the page lists them, numbered as the
// prose cites them.
func sourceLinks(articles []model.Article, cited []model.Article, mode SourceMode) []pages.Link {
	if mode == SourcesOff {
		return nil
	}
	var links []pages.Link
	for _, a := range articles {
		links = append(links, pages.Link{N: citationNumber(a, cited), Title: a.Title, URL: a.URL, Source: a.SourceName})
	}
	return links
}

// PicksDoc is the closer look as a page: every company at a glance, then
// each theme with what the research found, then a card a company with its
// price, its moves and its case.
func PicksDoc(p Picks, cited []model.Article, opts IdeasOptions, now time.Time, where *time.Location) pages.Doc {
	weekly := false
	for _, t := range p.Themes {
		weekly = weekly || len(t.Ideas) > 0
	}
	doc := pages.Doc{Kicker: "Closer look", Title: "Reacting to the news", Note: picksNote(opts),
		Dek: []string{escape(now.In(where).Format("Monday 2 January 2006"))}}
	if weekly {
		doc.Title = "This week's picks"
	}

	var all []model.Idea
	for _, t := range p.Themes {
		all = append(all, t.Ideas...)
	}
	all = append(all, p.Reactions...)
	if len(all) > 1 {
		doc.Parts = append(doc.Parts, pages.Section{ID: "at-a-glance", Title: "At a glance", Parts: []pages.Part{glance(all)}})
	}

	for _, t := range p.Themes {
		if len(t.Ideas) == 0 {
			continue
		}
		kind := "Popular: a theme the market has been paying for"
		if t.Kind == "early" {
			kind = "Early: a business growing before its shares have"
		}
		sec := pages.Section{ID: anchorID(t.Name), Title: t.Name, Lead: "<i>" + escape(kind) + "</i>"}
		for _, f := range []pages.Field{
			{Label: "The numbers", Body: escape(t.Figures)},
			{Label: "What's driving it", Body: escape(t.Driving)},
			{Label: "Priced in", Body: escape(t.PricedIn)},
			{Label: "Where the value is", Body: escape(t.Value)},
		} {
			sec.Parts = append(sec.Parts, f)
		}
		for _, idea := range t.Ideas {
			sec.Parts = append(sec.Parts, ideaCard(idea, cited))
		}
		doc.Parts = append(doc.Parts, sec)
	}

	if len(p.Reactions) > 0 {
		const lead = "Shares that moved several times their usual on the last session, where the move and the news do not fit."
		var cards []pages.Part
		for _, idea := range p.Reactions {
			cards = append(cards, ideaCard(idea, cited))
		}
		doc.Parts = append(doc.Parts, pages.Section{ID: "reactions", Title: "Reacting to the news", Lead: lead, Parts: cards})
	}

	if len(p.Earlier) > 0 {
		t := pages.Table{Head: []string{"Company", "", "Verdict", "On", "Since"}, Align: "lllrr"}
		for _, e := range p.Earlier {
			since := "<i>not yet traded</i>"
			if e.Priced {
				since = fmt.Sprintf("<b>%+.1f</b> points", e.Ahead)
			}
			t.Rows = append(t.Rows, []string{escape(e.Name), flaggedSymbol(e.Symbol), "<b>" + escape(e.Verdict) + "</b>",
				e.At.In(where).Format("2 Jan"), since})
		}
		t.Note = "<i>Points the way each verdict called it, against the S&amp;P 500, since the first open after it. A pick is not written up again for eight weeks unless its verdict changes.</i>"
		doc.Parts = append(doc.Parts, pages.Section{ID: "earlier", Title: "Earlier picks", Parts: []pages.Part{t}})
	}
	return doc
}

// glance is every company in a table: verdict, confidence, why it is here,
// and its last session.
func glance(ideas []model.Idea) pages.Table {
	t := pages.Table{Head: []string{"Company", "", "Verdict", "Confidence", "Why it is here", "Last session"}, Align: "lllllr"}
	for _, idea := range ideas {
		why := idea.Theme
		if idea.Kind == model.IdeaReaction {
			why = "reacting to news"
			if w := reactionWord(idea.Reaction); w != "" {
				why += ", " + w
			}
		}
		move := ""
		if idea.Quote != nil {
			move = escape(idea.Quote.Move())
		}
		t.Rows = append(t.Rows, []string{"<b>" + escape(idea.Name) + "</b>", flagged(idea.Symbol(), idea.Exchange),
			"<b>" + escape(idea.Verdict) + "</b>", escape(idea.Confidence), escape(why), move})
	}
	return t
}

// ideaCard is one company: its verdict, its price and moves, and its case a
// labelled part at a time.
func ideaCard(idea model.Idea, cited []model.Article) pages.Card {
	card := pages.Card{ID: anchorID(idea.Symbol()), Tone: strings.ToLower(idea.Verdict), Badge: idea.Verdict,
		Title: idea.Name, Code: idea.Symbol()}
	if idea.Confidence != "" {
		card.Meta = append(card.Meta, idea.Confidence+" confidence")
	}
	if w := reactionWord(idea.Reaction); w != "" {
		card.Meta = append(card.Meta, w)
	}
	if !idea.Accounts {
		card.Meta = append(card.Meta, "no SEC accounts behind it")
	}
	if idea.Before != "" {
		card.Meta = append(card.Meta, "was "+idea.Before)
	}
	card.Parts = append(card.Parts, priceParts(idea.Quote, idea.Trading)...)

	label, why := "What changed", idea.Changed
	if idea.Kind == model.IdeaTheme {
		label, why = "Where it fits", idea.Link
	}
	if why == "" {
		label, why = "Why it is here", idea.Link
	}
	card.Parts = append(card.Parts,
		pages.Field{Label: label, Body: linkCitations(escape(why), cited)},
		pages.Field{Label: "The move", Body: escape(idea.Moved)},
		pages.Field{Label: "Justified?", Body: linkCitations(escape(idea.Reaction), cited)},
		pages.Field{Label: "The price", Body: escape(idea.Value)},
		pages.Field{Label: "Warning signs", Body: escape(strings.Join(idea.Flags, "; "))},
		pages.Field{Label: "The case", Body: linkCitations(escape(idea.Case), cited)},
		pages.Field{Label: "Catalyst", Body: linkCitations(escape(idea.Catalyst), cited)},
		pages.Field{Label: "Sensitivity", Body: escape(idea.Sensitivity)},
	)
	if items := numberItems(idea.Numbers); len(items) > 0 {
		card.Parts = append(card.Parts, pages.Field{Label: "Numbers", Body: "• " + strings.Join(items, "\n• ")})
	}
	card.Parts = append(card.Parts,
		pages.Field{Label: "Checked", Body: linkCitations(escape(idea.Checked), cited)},
		pages.Field{Label: "Risk", Body: linkCitations(escape(idea.Risk), cited)},
	)
	var links []pages.Link
	for _, a := range idea.Articles {
		links = append(links, pages.Link{N: citationNumber(a, cited), Title: a.Title, URL: a.URL, Source: a.SourceName})
	}
	if len(links) > 0 {
		card.Parts = append(card.Parts, pages.Sources{Title: "The stories behind it", Links: links})
	}
	return card
}

// numberItems are the figures a verdict rests on, which the prompt asks for
// separated by semicolons.
func numberItems(numbers string) []string {
	var items []string
	for _, item := range strings.Split(numbers, ";") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, escape(item))
		}
	}
	return items
}

// priceParts are a share's price in figures and on its year's range: the
// last session, its moves over the stretches a reader thinks in, and where
// it sits between its low and its high with its averages marked.
func priceParts(q *model.Quote, tr *model.Trading) []pages.Part {
	var facts pages.Facts
	unit := "USD"
	if q != nil {
		unit = q.Unit()
		facts = append(facts, [2]string{"Last price", fmt.Sprintf("%s %s", formatPrice(q.Price), escape(unit))},
			[2]string{"Last session", escape(q.Move())})
	}
	if tr == nil {
		if len(facts) == 0 {
			return nil
		}
		return []pages.Part{facts}
	}
	if tr.Currency != "" {
		unit = tr.Currency
	}
	if q == nil && tr.Last > 0 {
		facts = append(facts, [2]string{"Last price", fmt.Sprintf("%s %s", formatPrice(tr.Last), escape(unit))})
	}
	var moves []pages.Bar
	for _, r := range tr.Returns {
		facts = append(facts, [2]string{capitalise(r.Over), fmt.Sprintf("%+.1f%%", r.Percent)})
		moves = append(moves, pages.Bar{Label: r.Over, Value: r.Percent, Text: fmt.Sprintf("%+.1f%%", r.Percent)})
	}
	if tr.Volatility > 0 {
		facts = append(facts, [2]string{"Usual swing, a year", fmt.Sprintf("%.0f%%", tr.Volatility)})
	}
	parts := []pages.Part{facts}
	if tr.High52 > tr.Low52 && tr.Last > 0 {
		r := pages.Range{Title: "Where the price sits in its year", Low: tr.Low52, High: tr.High52, Last: tr.Last,
			LowText: formatPrice(tr.Low52), HighText: formatPrice(tr.High52), LastText: formatPrice(tr.Last)}
		if tr.MA50 > 0 {
			r.Marks = append(r.Marks, pages.Mark{Label: "50-day", Value: tr.MA50})
		}
		if tr.MA200 > 0 {
			r.Marks = append(r.Marks, pages.Mark{Label: "200-day", Value: tr.MA200})
		}
		r.Note = fmt.Sprintf("<i>In %s. The marks are the average closing price over the last 50 and 200 sessions.</i>", escape(unit))
		parts = append(parts, r)
	}
	if line, ok := priceLine(tr, unit); ok {
		parts = append(parts, line)
	}
	if len(moves) > 1 {
		parts = append(parts, pages.Bars{Title: "Its moves", Items: moves})
	}
	return parts
}

// priceLine is the share's closing price over its last year, against its 50-
// and 200-day averages as they stood each day. Whether the price is above or
// below them, and since when, is most of what a chart reader looks for.
func priceLine(tr *model.Trading, unit string) (pages.Lines, bool) {
	if len(tr.Path) < 20 {
		return pages.Lines{}, false
	}
	price := pages.Line{Label: "Closing price", Tone: 0}
	ma50 := pages.Line{Label: "50-day average", Tone: 1}
	ma200 := pages.Line{Label: "200-day average", Tone: 2}
	low, high := tr.Path[0].Close, tr.Path[0].Close
	for _, p := range tr.Path {
		price.Values = append(price.Values, p.Close)
		ma50.Values = append(ma50.Values, p.MA50)
		ma200.Values = append(ma200.Values, p.MA200)
		low, high = min(low, p.Close), max(high, p.Close)
	}
	first, last := tr.Path[0].Date, tr.Path[len(tr.Path)-1].Date
	return pages.Lines{
		Title:    "The price over the year",
		Lines:    []pages.Line{price, ma50, ma200},
		LowText:  formatPrice(low),
		HighText: formatPrice(high),
		From:     first.Format("Jan 2006"),
		To:       last.Format("2 Jan 2006"),
		Note:     fmt.Sprintf("<i>Daily closes in %s, with the year's highest and lowest close at the side. The averages are of the last 50 and 200 closes, as they stood each day.</i>", escape(unit)),
	}, true
}

// moveChips is a line of share moves, each coloured by its direction.
func moveChips(label string, quotes []model.Quote) pages.Chips {
	chips := pages.Chips{Label: label}
	for _, q := range quotes {
		chips.Items = append(chips.Items, pages.Chip{Text: q.Symbol + " " + q.Move(), Tone: direction(q.Percent)})
	}
	return chips
}

func direction(v float64) string {
	switch {
	case v >= 0.05:
		return "up"
	case v <= -0.05:
		return "down"
	}
	return ""
}

func formatPrice(v float64) string {
	if v >= 1000 {
		return thousandsFloat(v, 0)
	}
	return fmt.Sprintf("%.2f", v)
}

// thousandsFloat writes 7651.5 as "7,652" (or with decimals).
func thousandsFloat(v float64, decimals int) string {
	s := fmt.Sprintf("%.*f", decimals, math.Abs(v))
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if v < 0 {
		out = "-" + out
	}
	return out
}

// Period is a stretch of a company's accounts: a year, a quarter, the
// last twelve months. Figures holds only what was reported.
type Period struct {
	Label   string
	Figures map[string]float64
}

// Accounts is what an analysis's page shows of the company's figures.
type Accounts struct {
	Currency string
	Years    []Period // newest first
	Quarters []Period // newest first
	TTM      *Period
	AsOf     time.Time
	Price    *model.Quote
	Trading  *model.Trading
	Peers    *Peers
}

// Peers is the company beside its industry group, written out already.
type Peers struct {
	About string // Telegram HTML: which group, which year
	Rows  []PeerRow
}

// PeerRow is one measure: the company's figure, the group's middle value and
// middle half, and how many of the group the company is above.
type PeerRow struct {
	Label, Company, Median, Range, Above string
}

// AnalysisDoc is an analysis as a page: the share's price on its year, the
// accounts as tables and charts, the reading of them, the verdict, and the
// companies to read beside it. The verdict is last, as in the chat
// (RenderAnalysis says why).
func AnalysisDoc(ticker, company string, v AnalysisVerdict, prose string, acc Accounts, related []Related, opts IdeasOptions, terms []model.Term, now time.Time, where *time.Location) pages.Doc {
	doc := pages.Doc{Kicker: "Analysis · " + ticker, Title: company,
		Dek: []string{escape(now.In(where).Format("Monday 2 January 2006"))}}
	if price := priceParts(acc.Price, acc.Trading); len(price) > 0 {
		doc.Parts = append(doc.Parts, pages.Section{ID: "price", Title: "The share price", Parts: price})
	}
	if figures := accountsParts(acc); len(figures) > 0 {
		doc.Parts = append(doc.Parts, pages.Section{ID: "figures", Title: "The figures", Parts: figures})
	}
	if p := acc.Peers; p != nil && len(p.Rows) > 0 {
		t := pages.Table{Head: []string{"", "This company", "Group middle", "Middle half", "Higher than"}, Align: "lrrrr", Labels: true}
		for _, r := range p.Rows {
			t.Rows = append(t.Rows, []string{escape(r.Label), "<b>" + escape(r.Company) + "</b>", escape(r.Median), escape(r.Range), escape(r.Above)})
		}
		t.Note = p.About
		doc.Parts = append(doc.Parts, pages.Section{ID: "peers", Title: "Beside its industry", Parts: []pages.Part{t}})
	}
	if prose != "" {
		var blocks []string
		for _, seg := range plainSegments(prose) {
			blocks = append(blocks, seg.blocks...)
		}
		doc.Parts = append(doc.Parts, pages.Prose(linkTerms(blocks, terms)))
	}
	if v.Verdict != "" {
		doc.Note = analysisNote(opts)
		card := pages.Card{ID: "verdict", Tone: strings.ToLower(v.Verdict), Badge: v.Verdict, Title: "The verdict"}
		if v.Confidence != "" {
			card.Meta = append(card.Meta, v.Confidence+" confidence")
		}
		card.Parts = append(card.Parts, pages.Prose(linkTerms(paragraphs(v.Body), terms)))
		doc.Parts = append(doc.Parts, card)
	}
	if len(related) > 0 {
		t := pages.Table{Head: []string{"Company", "", "Why read it"}, Align: "lll"}
		for _, r := range related {
			t.Rows = append(t.Rows, []string{"<b>" + escape(r.Name) + "</b>", flagged(r.Symbol, r.Exchange), escape(r.Why)})
		}
		t.Note = "<i>Every ticker checked against its exchange. Not recommendations.</i>"
		doc.Parts = append(doc.Parts, pages.Section{ID: "related", Title: "Companies to read next to it", Parts: []pages.Part{t}})
	}
	if !acc.AsOf.IsZero() {
		doc.Footer = []string{"<i>" + escape(company) + ", from filings up to " + acc.AsOf.Format("2 Jan 2006") + "</i>"}
	}
	return doc
}

// lines of the accounts table, in the order a reader reads a company: what
// it sells, what it keeps, what it turns into cash.
var accountLines = []struct {
	label string
	of    func(map[string]float64) (float64, bool)
	kind  string // "money", "percent", "eps"
}{
	{"Revenue", figure("revenue"), "money"},
	{"Gross margin", ratio("grossProfit", "revenue"), "percent"},
	{"Operating profit", figure("operatingIncome"), "money"},
	{"Operating margin", ratio("operatingIncome", "revenue"), "percent"},
	{"Net profit", figure("netIncome"), "money"},
	{"Earnings a share", figure("epsDiluted"), "eps"},
	{"Cash from operations", figure("operatingCashFlow"), "money"},
	{"Spent on investment", figure("capitalExpenditure"), "money"},
	{"Free cash flow", freeCash, "money"},
	{"Research", figure("researchDevelopment"), "money"},
}

func figure(key string) func(map[string]float64) (float64, bool) {
	return func(f map[string]float64) (float64, bool) {
		v, ok := f[key]
		return v, ok
	}
}

func ratio(top, bottom string) func(map[string]float64) (float64, bool) {
	return func(f map[string]float64) (float64, bool) {
		t, ok1 := f[top]
		b, ok2 := f[bottom]
		if !ok1 || !ok2 || b == 0 {
			return 0, false
		}
		return 100 * t / b, true
	}
}

func freeCash(f map[string]float64) (float64, bool) {
	ocf, ok1 := f["operatingCashFlow"]
	capex, ok2 := f["capitalExpenditure"]
	if !ok1 || !ok2 {
		return 0, false
	}
	return ocf - math.Abs(capex), true
}

// accountsParts are the accounts as tables, a column a period, and the
// revenue and operating margin as charts.
func accountsParts(acc Accounts) []pages.Part {
	var parts []pages.Part
	if len(acc.Quarters) > 0 {
		periods := append([]Period(nil), acc.Quarters...)
		reverse(periods)
		if acc.TTM != nil {
			periods = append(periods, Period{Label: "Last 12 months", Figures: acc.TTM.Figures})
		}
		parts = append(parts, accountsTable("Quarter by quarter", periods, acc.Currency))
	}
	if len(acc.Years) > 0 {
		years := append([]Period(nil), acc.Years...)
		reverse(years)
		parts = append(parts, accountsTable("Year by year", years, acc.Currency))

		revenue := pages.Columns{Title: "Revenue, year by year"}
		margin := pages.Columns{Title: "Operating margin, year by year"}
		for _, y := range years {
			if v, ok := y.Figures["revenue"]; ok {
				revenue.Items = append(revenue.Items, pages.Bar{Label: shortPeriod(y.Label), Value: v, Text: money(v, acc.Currency)})
			}
			if v, ok := ratio("operatingIncome", "revenue")(y.Figures); ok {
				margin.Items = append(margin.Items, pages.Bar{Label: shortPeriod(y.Label), Value: v, Text: fmt.Sprintf("%.1f%%", v)})
			}
		}
		if len(revenue.Items) > 1 {
			parts = append(parts, revenue)
		}
		if len(margin.Items) > 1 {
			parts = append(parts, margin)
		}
		cash := pages.Pairs{Title: "Cash from the business against what it invests, year by year",
			A: "Cash from operations", B: "Capital spending"}
		for _, y := range years {
			in, ok1 := y.Figures["operatingCashFlow"]
			out, ok2 := y.Figures["capitalExpenditure"]
			if ok1 && ok2 {
				cash.Items = append(cash.Items, pages.Pair{Label: shortPeriod(y.Label), A: in, B: out, Text: money(in-out, acc.Currency)})
			}
		}
		if len(cash.Items) > 1 {
			cash.Note = "<i>The bold figure under each year is its free cash flow: the cash from operations left after capital spending.</i>"
			parts = append(parts, cash)
		}
	}
	if len(parts) > 0 {
		parts = append(parts, pages.Small("<i>From the company's filings with the SEC. Margins are the share of revenue left at each line; free cash flow is cash from operations less what was spent on investment.</i>"))
	}
	return parts
}

func accountsTable(caption string, periods []Period, currency string) pages.Table {
	t := pages.Table{Caption: caption, Head: []string{""}, Align: "l", Labels: true}
	for _, p := range periods {
		t.Head = append(t.Head, shortPeriod(p.Label))
		t.Align += "r"
	}
	for _, line := range accountLines {
		row := []string{escape(line.label)}
		known := false
		for _, p := range periods {
			v, ok := line.of(p.Figures)
			if !ok {
				row = append(row, "–")
				continue
			}
			known = true
			switch line.kind {
			case "percent":
				row = append(row, fmt.Sprintf("%.1f%%", v))
			case "eps":
				row = append(row, fmt.Sprintf("%.2f", v))
			default:
				row = append(row, escape(money(v, currency)))
			}
		}
		if known {
			t.Rows = append(t.Rows, row)
		}
	}
	return t
}

// periodEnd finds the end in "FY to 31 Dec 2025" or "3 months to Jun 2026".
var periodEnd = regexp.MustCompile(`to (\d{1,2} )?(\w{3} \d{4})$`)

// shortPeriod is a period's label as a column head: "FY to 31 Dec 2025" is
// "FY Dec 2025", and a quarter, "3 months to 30 Jun 2026", is its months,
// "Apr–Jun 2026". The owner read "3m to Jun 2026" on 1 October 2026 and
// asked what it meant.
//
// A company whose year is weeks rather than months ends it on a weekday:
// Micron's quarter of June, July and August ended on 3 September 2026. A
// period ending in a month's first week is named for the month before, as
// the company names it.
func shortPeriod(label string) string {
	m := periodEnd.FindStringSubmatch(label)
	if m == nil {
		return label
	}
	end, err := time.Parse("Jan 2006", m[2])
	if err != nil {
		return label
	}
	if day, _ := strconv.Atoi(strings.TrimSpace(m[1])); day > 0 && day <= 7 {
		end = end.AddDate(0, -1, 0)
	}
	switch {
	case strings.HasPrefix(label, "FY"):
		return "FY " + end.Format("Jan 2006")
	case strings.HasPrefix(label, "3m to") || strings.HasPrefix(label, "3 months to"):
		return end.AddDate(0, -2, 0).Format("Jan") + "–" + end.Format("Jan 2006")
	}
	return label
}

// money writes an amount as a reader says it: "$3.37bn", "$97m".
func money(v float64, currency string) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	unit := "$"
	if currency != "" && currency != "USD" {
		unit = currency + " "
	}
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%s%s%.2fbn", sign, unit, v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%s%s%.0fm", sign, unit, v/1e6)
	}
	return fmt.Sprintf("%s%s%s", sign, unit, thousandsFloat(v, 0))
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// IndustryDoc is /industry as a page: the industry drawn as a chain of its
// parts, the explanation, and the companies in each part.
func IndustryDoc(topic, prose string, companies []IndustryCompany, opts IdeasOptions, terms []model.Term) pages.Doc {
	doc := pages.Doc{Kicker: "How an industry fits together", Title: capitalise(topic), Note: industryNoteFor(opts)}
	parts := partsOf(companies)
	if len(parts) > 1 {
		chain := pages.Chain{Title: "The parts, from what goes in to who buys what comes out"}
		for _, p := range parts {
			step := pages.Step{Name: p.Name}
			for _, c := range p.Companies {
				step.Items = append(step.Items, c.Symbol)
			}
			chain.Steps = append(chain.Steps, step)
		}
		chain.Note = "<i>The tickers are the companies to look into in each part, listed below.</i>"
		doc.Parts = append(doc.Parts, chain)
	}
	if prose != "" {
		doc.Parts = append(doc.Parts, pages.Section{ID: "explained", Title: "How it works",
			Parts: []pages.Part{pages.Prose(linkTerms(paragraphs(prose), terms))}})
	}
	if len(parts) > 0 {
		sec := pages.Section{ID: "companies", Title: "Companies to look into", Lead: "<i>Every ticker checked against its exchange.</i>"}
		for _, p := range parts {
			t := pages.Table{Caption: p.Name, Head: []string{"Company", "", "Why look into it"}, Align: "lll"}
			for _, c := range p.Companies {
				name := "<b>" + escape(c.Name) + "</b>"
				if c.Market != "" {
					name += " <i>" + escape(c.Market) + "</i>"
				}
				t.Rows = append(t.Rows, []string{name, "<code>" + escape(c.Symbol) + "</code>", escape(c.Why)})
			}
			sec.Parts = append(sec.Parts, t)
		}
		doc.Parts = append(doc.Parts, sec)
	}
	return doc
}

// anchorID is a name as a page anchor: "Big Tech" is "big-tech".
func anchorID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
