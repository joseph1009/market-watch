package app

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/runcache"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// The week's themes, in order: the market's leaders measured from two years
// of prices; the themes driving them, sorted by Sonnet; the industries whose
// business is growing before their shares, found by Opus on the web; each
// theme researched for the part of it the market has not paid for; the
// companies proposed, with their accounts and their valuation against the
// rest of the theme and their own history; and their verdicts. See the ideas
// package for the stages, and docs/RUNBOOK.md for why.

const (
	// leaderCount is how many of the market's leaders the sorting sees.
	leaderCount = 150

	// popularIndustries and earlyIndustries are how many of each the sorting
	// and the scout are shown.
	popularIndustries = 8
	earlyIndustries   = 6

	// headlinesShown is how many of the recent briefs' headlines the sorting
	// and the scout read, newest first.
	headlinesShown = 80

	// researchWorkers is how many themes are researched at once: each is an
	// Opus call with the web, and the plan is not asked for more than two.
	researchWorkers = 2

	// peersPerTheme is how many of a theme's members, besides the companies
	// proposed, have their accounts read for the theme's medians.
	peersPerTheme = 8

	// membersShown is how many of a theme's members the research sees.
	membersShown = 12

	// panelFloor is the least a company must be worth for its history to be
	// loaded: under the US$2bn a company must be worth to be considered, so
	// a smaller one proposed by the research still has its price.
	panelFloor = 1e9
)

// week is what the week's themes produced.
type week struct {
	// ran says the themes were sorted, so the week is done whatever the
	// research then found.
	ran bool

	views   []telegram.ThemeView
	earlier []telegram.EarlierPick
	shown   []model.Idea
	charts  map[string]string
	run     ideas.ThemeRun
}

// runThemes runs the week's themes.
func (a *App) runThemes(ctx context.Context, listings []market.Listing, f following, backdrop []string) week {
	w := week{charts: map[string]string{}}
	started := time.Now()
	cached := runcache.From(ctx)

	panel, err := a.loadPanel(listings)
	if err != nil {
		a.Log.Warn("no themes: the market's history could not be read", "error", err)
		return w
	}
	if len(panel.Dates) < market.DefaultRules.MinHistory {
		a.Log.Warn("no themes yet: the market's history is still filling",
			"sessions", len(panel.Dates), "needed", market.DefaultRules.MinHistory, "missing", a.MarketStore.Missing(a.now()))
		return w
	}

	stocks := market.Measure(panel, listings)
	bench := market.MeasureSeries(panel.Get(market.Benchmark), market.Listing{Symbol: market.Benchmark})
	pool := append(append([]market.Stock{}, stocks...), a.singaporeStocks(ctx)...)
	leaders, _ := market.Leaders(pool, market.DefaultRules, leaderCount)
	headlines := a.recentHeadlines()
	inds := market.Industries(stocks, bench, market.DefaultRules, market.Mentions(headlines, listings))
	popular, early := market.Popular(inds, popularIndustries), market.Early(inds, earlyIndustries)
	shown := headlines[:min(headlinesShown, len(headlines))]
	cached.Save("leaders", leaders)
	cached.Save("industries", map[string][]market.Industry{"popular": popular, "early": early})

	themes, usage, err := a.Sorter.Sort(ctx, ideas.SortInput{
		Leaders: leaders, Bench: bench, Industries: popular, Headlines: shown,
		LastWeek: a.Themes.Names(ideas.ThemePopular),
	})
	if err != nil {
		a.Log.Warn("no themes: the sorting failed", "error", err)
		return w
	}
	w.ran = true
	a.Log.Info("themes sorted", "themes", len(themes), "leaders", len(leaders), "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)

	if a.Scout != nil {
		names := map[string]string{}
		for _, l := range listings {
			names[l.Symbol] = l.Name
		}
		found, usage, err := a.Scout.Find(ctx, ideas.ScoutInput{
			Early: early, Names: names, Popular: themes, Headlines: shown,
			LastWeek: a.Themes.Names(ideas.ThemeEarly), Today: a.now(),
		})
		if err != nil {
			a.Log.Warn("no early themes: the scout failed", "error", err)
		}
		a.Log.Info("early themes found", "themes", len(found), "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)
		themes = append(themes, found...)
	}

	byStock := map[string]market.Stock{}
	for _, s := range pool {
		byStock[s.Symbol] = s
	}
	byIndustry := map[string]market.Industry{}
	for _, d := range inds {
		byIndustry[d.Name] = d
	}
	summaries := make([]string, len(themes))
	for i := range themes {
		themes[i].Figures, summaries[i] = themeFigures(themes[i], byStock, byIndustry, bench)
	}
	cached.Save("themes", themes)

	recent := a.recentPicks()
	researched := a.researchThemes(ctx, themes, recent, f)
	cached.Save("research", researched)

	// The companies proposed, a theme at a time in turn, so the verdicts go
	// round every theme before any gets a second.
	last := map[string]ideas.Record{}
	for _, r := range recent {
		last[r.Symbol] = r
	}
	var cands []model.Idea
	for round := 0; len(cands) < ideas.PicksJudged; round++ {
		more := false
		for _, r := range researched {
			if round >= len(r.Ideas) || len(cands) == ideas.PicksJudged {
				continue
			}
			more = true
			idea := r.Ideas[round]
			if f.follows(idea) {
				continue
			}
			if prev, ok := last[idea.Symbol()]; ok {
				idea.Before = prev.Verdict + " on " + prev.At.In(a.Cfg.DisplayLocation).Format("2 Jan")
			}
			cands = append(cands, idea)
		}
		if !more {
			break
		}
	}

	var judged []model.Idea
	if len(cands) > 0 {
		sheets := a.factsFor(ctx, cands)
		for i, s := range sheets {
			w.charts[cands[i].Symbol()] = s.chart
		}
		a.value(ctx, cands, sheets, themes, byStock, listings)
		judged = a.judge(ctx, cands, sheets, nil, backdrop)
	}

	// A pick is shown as a BUY or a SELL; one given the same verdict in the
	// last weeks is on the earlier picks list instead.
	var keep []model.Idea
	for _, j := range judged {
		if !j.Shown() {
			continue
		}
		if prev, ok := last[j.Symbol()]; ok && prev.Verdict == j.Verdict {
			continue
		}
		keep = append(keep, j)
	}
	keep = best(keep, ideas.PicksShown)
	w.shown = keep

	for i, t := range themes {
		r := researched[i]
		view := telegram.ThemeView{Kind: t.Kind, Name: t.Name, Figures: summaries[i], Driving: r.Driving, PricedIn: r.PricedIn, Value: r.Value}
		if view.Driving == "" {
			view.Driving = t.Driver
		}
		entry := ideas.ThemeEntry{Kind: t.Kind, Name: t.Name, Driver: t.Driver, Research: r.Text}
		for _, idea := range keep {
			if idea.Theme == t.Name {
				view.Ideas = append(view.Ideas, idea)
				entry.Picks = append(entry.Picks, idea.Symbol())
			}
		}
		w.views = append(w.views, view)
		w.run.Themes = append(w.run.Themes, entry)
	}
	w.run.At = a.now()
	w.earlier = a.earlierPicks(ctx, panel, recent)
	a.Log.Info("themes done", "themes", len(themes), "proposed", len(cands), "judged", len(judged), "shown", len(keep),
		"took", time.Since(started).Round(time.Second))
	return w
}

// loadPanel reads two years of the market's history for the companies worth
// enough to matter, and the index fund.
func (a *App) loadPanel(listings []market.Listing) (*market.Panel, error) {
	keep := map[string]bool{market.Benchmark: true}
	for _, l := range listings {
		if l.MarketCap >= panelFloor {
			keep[l.Symbol] = true
		}
	}
	return a.MarketStore.Load(a.now().Add(-market.Reach), func(sym string) bool { return keep[sym] })
}

// recentHeadlines are the headlines the briefs have carried in the last three
// weeks, newest first.
func (a *App) recentHeadlines() []string {
	if a.Covered == nil {
		return nil
	}
	return a.Covered.Titles()
}

// recentPicks are the theme picks recorded within the repeat window.
func (a *App) recentPicks() []ideas.Record {
	if a.Scorecard == nil {
		return nil
	}
	return a.Scorecard.Since(ideas.SourceTheme, a.now().Add(-ideas.RepeatWindow))
}

// researchThemes researches each theme, two at a time. A theme whose research
// fails keeps its place with nothing found.
func (a *App) researchThemes(ctx context.Context, themes []ideas.Theme, recent []ideas.Record, f following) []ideas.Research {
	var lines []string
	for _, r := range recent {
		line := fmt.Sprintf("%s (%s): %s on %s", r.Name, r.Symbol, r.Verdict, r.At.In(a.Cfg.DisplayLocation).Format("2 Jan"))
		if r.Theme != "" {
			line += ", under " + r.Theme
		}
		lines = append(lines, line)
	}
	out := make([]ideas.Research, len(themes))
	sem := make(chan struct{}, researchWorkers)
	var wg sync.WaitGroup
	for i, t := range themes {
		out[i] = ideas.Research{Theme: t}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			r, usage, err := a.Researcher.Research(ctx, ideas.ResearchInput{
				Theme: t, Previous: a.Themes.Previous(t.Name), Recent: lines,
				Tracked: f.names, Followed: f.tickers, Today: a.now(),
			})
			if err != nil {
				a.Log.Warn("a theme's research failed", "theme", t.Name, "error", err)
				return
			}
			a.Log.Info("theme researched", "theme", t.Name, "proposed", len(r.Ideas), "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)
			out[i] = r
		}()
	}
	wg.Wait()
	return out
}

// themeFigures writes a theme's members' numbers for the research, and a
// line of them for the message.
func themeFigures(t ideas.Theme, byStock map[string]market.Stock, byIndustry map[string]market.Industry, bench market.Stock) (string, string) {
	var members []market.Stock
	for _, m := range t.Members {
		if s, ok := byStock[memberSymbol(m)]; ok {
			members = append(members, s)
		}
	}
	if len(members) == 0 {
		return "", ""
	}
	var r12, r3 []float64
	industries := map[string]bool{}
	var b strings.Builder
	b.WriteString("Members, against the S&P 500:\n")
	for i, s := range members {
		r12 = append(r12, s.R12-bench.R12)
		r3 = append(r3, s.R3-bench.R3)
		if s.Industry != "" {
			industries[s.Industry] = true
		}
		if i < membersShown {
			fmt.Fprintf(&b, "- %s %s (%s): 2 years %s, 12 months %s, 6 months %s, 3 months %s\n",
				s.Symbol, market.PlainName(s.Name), s.Industry, pp(s.R24-bench.R24), pp(s.R12-bench.R12), pp(s.R6-bench.R6), pp(s.R3-bench.R3))
		}
	}
	var names []string
	for name := range industries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if d, ok := byIndustry[name]; ok {
			b.WriteString("Industry: " + ideas.IndustryLine(d) + "\n")
		}
	}
	summary := fmt.Sprintf("%d companies; the median one %s against the S&P 500 over 12 months, %s over 3",
		len(members), pp(medianOf(r12)), pp(medianOf(r3)))
	return b.String(), summary
}

// memberSymbol is a member as the market's history names it: the scout
// writes "FLNC:US" and "5E2:SP", the sorting the symbol alone.
func memberSymbol(m string) string {
	sym, ex, found := strings.Cut(strings.ToUpper(strings.TrimSpace(m)), ":")
	if !found || ex == "US" {
		return sym
	}
	return sym + "." + ex
}

// pp writes a difference of fractions in percentage points.
func pp(f float64) string {
	if math.IsNaN(f) {
		return "n/a"
	}
	return fmt.Sprintf("%+.0f pts", 100*f)
}

func medianOf(vs []float64) float64 {
	var clean []float64
	for _, v := range vs {
		if !math.IsNaN(v) {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return math.NaN()
	}
	sort.Float64s(clean)
	if n := len(clean); n%2 == 1 {
		return clean[n/2]
	}
	return (clean[len(clean)/2-1] + clean[len(clean)/2]) / 2
}

// value sets each proposed company's multiples against its theme's and its
// own history's, with its warning signs, and says where a BUY is closed to
// it. A theme's medians are over the companies proposed in it and its best
// members, whose accounts are read for the purpose.
func (a *App) value(ctx context.Context, cands []model.Idea, sheets []sheet, themes []ideas.Theme, byStock map[string]market.Stock, listings []market.Listing) {
	caps := map[string]float64{}
	for _, l := range listings {
		caps[l.Symbol] = l.MarketCap
	}

	// The accounts of the companies to compare with: those proposed, read
	// already, and each theme's best US members.
	snaps := map[string]*fundamentals.Snapshot{}
	for i, c := range cands {
		if sheets[i].snap != nil {
			snaps[c.Ticker] = sheets[i].snap
		}
	}
	peers := map[string][]string{}
	var toRead []string
	queued := map[string]bool{}
	for _, t := range themes {
		var members []market.Stock
		for _, m := range t.Members {
			if s, ok := byStock[memberSymbol(m)]; ok && !s.NotListedInAmerica {
				members = append(members, s)
			}
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].Score > members[j].Score })
		for _, s := range members[:min(peersPerTheme, len(members))] {
			peers[t.Name] = append(peers[t.Name], s.Symbol)
			if snaps[s.Symbol] == nil && !queued[s.Symbol] {
				queued[s.Symbol] = true
				toRead = append(toRead, s.Symbol)
			}
		}
	}
	for i, c := range cands {
		if sheets[i].snap != nil {
			peers[c.Theme] = append(peers[c.Theme], c.Ticker)
		}
	}
	for sym, snap := range a.readAccounts(ctx, toRead) {
		snaps[sym] = snap
	}

	var currencies []string
	for _, s := range snaps {
		currencies = append(currencies, s.Currency)
	}
	rates := a.dollarRates(ctx, currencies)
	usd := func(cur string) float64 {
		if cur == "" || cur == "USD" {
			return 1
		}
		return rates.Latest(cur)
	}
	multiples := map[string]ideas.Multiples{}
	for sym, s := range snaps {
		multiples[sym] = s.Multiples(caps[sym], usd(s.Currency))
	}

	for i := range cands {
		c := &cands[i]
		v := ideas.Valuation{}
		var price, ma200 float64
		if c.Trading != nil {
			price, ma200 = c.Trading.Last, c.Trading.MA200
		}
		snap := sheets[i].snap
		if snap == nil {
			v.Flags = ideas.WarningSigns(price, ma200, 0, 0, 0, 0)
		} else {
			v.Now = multiples[c.Ticker]
			var others []ideas.Multiples
			for _, p := range peers[c.Theme] {
				if p != c.Ticker {
					if m, ok := multiples[p]; ok {
						others = append(others, m)
					}
				}
			}
			v.Theme, v.Peers = ideas.Medians(others), len(others)
			v.Own = ideas.Medians(a.pastMultiples(ctx, sheets[i].chart, snap, caps[c.Ticker], usd(snap.Currency)))
			var target, insiders, short float64
			if e := sheets[i].expect; e != nil {
				target, insiders, short = e.Target, e.InsiderNet3, e.ShortShares
			}
			v.Flags = ideas.WarningSigns(price, ma200, target, insiders, short, snap.Balance.Figure("sharesOutstanding").Amount)
		}
		c.Flags, c.BuyClosed = v.Flags, v.BuyClosed()
		if snap != nil || len(v.Flags) > 0 {
			c.Valuation = v.Facts()
		}
	}
}

// readAccounts reads the accounts of companies compared against, a few at a
// time. Those that cannot be read are left out.
func (a *App) readAccounts(ctx context.Context, tickers []string) map[string]*fundamentals.Snapshot {
	out := map[string]*fundamentals.Snapshot{}
	if a.Accounts == nil {
		return out
	}
	var mu sync.Mutex
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range factWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				snap, err := a.Accounts.Fetch(ctx, t, accountYears)
				if err != nil {
					continue
				}
				mu.Lock()
				out[t] = &snap
				mu.Unlock()
			}
		}()
	}
queue:
	for _, t := range tickers {
		select {
		case jobs <- t:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

// pastMultiples are a company's multiples at its last five year ends, from
// five years of its month-end prices.
func (a *App) pastMultiples(ctx context.Context, chart string, snap *fundamentals.Snapshot, marketCap, usd float64) []ideas.Multiples {
	if a.Market == nil || chart == "" || marketCap <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, historyBudget)
	defer cancel()
	series, err := a.Market.Monthly(ctx, chart)
	if err != nil || len(series.Bars) == 0 {
		return nil
	}
	now := series.Bars[len(series.Bars)-1].Close
	priceAt := func(t time.Time) float64 {
		for i := len(series.Bars) - 1; i >= 0; i-- {
			if !series.Bars[i].Date.After(t) {
				return series.Bars[i].Close
			}
		}
		return 0
	}
	return snap.PastMultiples(marketCap, now, priceAt, usd)
}

// earlierPicks are the theme picks of the last weeks, and how each has done
// the way it was called since its first open, against the index.
func (a *App) earlierPicks(ctx context.Context, panel *market.Panel, recent []ideas.Record) []telegram.EarlierPick {
	if len(recent) == 0 {
		return nil
	}
	bench := panelPath(panel, market.Benchmark)
	var currencies []string
	for _, r := range recent {
		currencies = append(currencies, r.Currency)
	}
	rates := a.dollarRates(ctx, currencies)
	var out []telegram.EarlierPick
	for i := len(recent) - 1; i >= 0; i-- {
		r := recent[i]
		var path ideas.Path
		if strings.Contains(r.Chart, ".") {
			path = a.pathFor(ctx, r.Chart) // listed abroad: the chart source
		} else {
			path = panelPath(panel, strings.ReplaceAll(r.Chart, "-", "."))
		}
		called, ok := ideas.Called(r, path, bench, rates)
		out = append(out, telegram.EarlierPick{
			Name: r.Name, Symbol: r.Symbol, Verdict: r.Verdict, Theme: r.Theme, At: r.At,
			Ahead: 100 * called, Priced: ok,
		})
	}
	return out
}
