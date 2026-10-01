package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/consensus"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/runcache"
	"github.com/joseph1009/market-watch/internal/search"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// "Worth a closer look" follows the brief. Once a week, on the scheduled
// run's first look of the week, the themes: up to ten companies found from the
// market's numbers -- what it has been paying for, and what is growing before
// its shares have followed -- each with a verdict (themes.go). Every day, the
// reactions: up to three shares that moved several times their usual on the
// day's news, where the move and the news do not fit (reactions.go). A day
// with neither sends nothing. Only companies the watchlists do not follow are
// shown; the brief covers those.
//
// The daily run's closer look starts twenty minutes after the brief (look.go),
// so the brief is never held up by it and the plan's allowance is not asked
// for both at once. A brief asked for with /now, or sent with -once, is
// followed at once, with the reactions alone: the week's themes belong to the
// scheduled run, which the channel reads.
//
// It follows the brief to the same places: the owner always, and the channel
// on the days the brief goes there, which is the scheduled run and -once
// -share. A brief asked for with /now still keeps both to the owner.
//
// This used to be owner-only, and the change was made deliberately and with
// the trade understood: verdicts published to other people are advice given to
// others, not notes kept for yourself. What stands in for the review that no
// longer happens is the note at the head of the channel's copy, which says a
// model wrote it, that nobody checked it, and that it is not a recommendation
// to act. If that note ever goes, this should go back to owner-only. The
// reasoning is written out in docs/RUNBOOK.md.
//
// It is still never what /share posts: /share passes on the last brief or
// analysis, and a verdict reaching the channel is the daily run's doing or
// nothing.

const (
	// ideasBudget bounds a day's closer look: the market's latest sessions,
	// the facts for six companies, and their verdicts, which takes five to
	// ten minutes, and since the verdicts search the web to check their case
	// (2026-10-01) a few more. The run lock is held throughout, so a /now
	// sent meanwhile waits; this is what keeps that wait finite.
	ideasBudget = 35 * time.Minute

	// themesBudget bounds the week's: the sorting, the scout, five themes
	// researched two at a time, the facts for their companies and their
	// peers, and up to sixteen verdicts that search the web, on top of the
	// day's. Under an hour, done before the open.
	themesBudget = 90 * time.Minute

	// factWorkers is how many companies' facts are read at once. Each is SEC
	// reads, paced across all of them by the accounts client, and six Nasdaq
	// reads of a second or two each.
	factWorkers = 4
)

// look is what the closer look starts from: the brief's articles, whether
// the channel is reading today, and whether this is the scheduled run, the
// one the week's themes go with. It is written to the data volume while it
// waits, so it carries the articles rather than the report, whose citations
// are not kept on disk.
type look struct {
	Due       time.Time       `json:"due"`
	Share     bool            `json:"share"`
	Scheduled bool            `json:"scheduled,omitempty"`
	Cited     []model.Article `json:"cited"`
}

func lookFrom(rep model.Report, share, scheduled bool) look {
	return look{Share: share, Scheduled: scheduled, Cited: rep.Cited}
}

// sendIdeas finds, judges and delivers. Every failure costs this section only:
// the brief has already arrived. lk.Share says whether the channel gets it
// too, and carries the value the brief was sent with, so the two never
// disagree about who is reading today.
func (a *App) sendIdeas(ctx context.Context, lk look) {
	if a.Judge == nil || a.MarketStore == nil {
		return
	}
	prefs := a.Prefs()
	if prefs.ChatID == 0 {
		return
	}

	weekly := lk.Scheduled && a.Themes != nil && a.Sorter != nil && a.Researcher != nil &&
		!a.Themes.DoneThisWeek(a.now(), a.Cfg.ScheduleLocation)
	budget := ideasBudget
	if weekly {
		budget = themesBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	started := time.Now()

	ctx, cached := a.Cache.Start(ctx, runcache.Recommendations, "")
	defer cached.Finish(nil)
	cached.Save("look", lk)

	a.topUpMarket(ctx)
	listings, err := a.listings(ctx)
	if err != nil {
		a.Log.Warn("no closer look: Nasdaq's listings could not be read", "error", err)
		cached.Fail(err)
		return
	}
	backdrop := a.backdrop(ctx)
	cached.Save("backdrop", backdrop)
	following := newFollowing(prefs.Groups)

	var (
		picks  telegram.Picks
		shown  []model.Idea
		charts = map[string]string{}
		week   *ideas.ThemeRun
	)
	if weekly {
		w := a.runThemes(ctx, listings, following, backdrop)
		if w.ran {
			picks.Themes, picks.Earlier = w.views, w.earlier
			shown = append(shown, w.shown...)
			for k, v := range w.charts {
				charts[k] = v
			}
			week = &w.run
		}
	}
	reactions, reactionCharts := a.runReactions(ctx, lk, listings, following, backdrop)
	picks.Reactions = reactions
	shown = append(shown, reactions...)
	for k, v := range reactionCharts {
		charts[k] = v
	}

	// The week is written down however it went, so a week whose research
	// found nothing does not search again every day until it does.
	if week != nil {
		if err := a.Themes.Add(*week); err != nil {
			a.Log.Warn("could not record the week's themes", "error", err)
		}
	}
	if picks.Empty() {
		a.Log.Info("closer look: nothing to show today", "weekly", weekly, "took", time.Since(started).Round(time.Second))
		return
	}

	title := "Closer look · " + a.now().In(a.Cfg.DisplayLocation).Format("Mon 2 Jan")
	picksFor := func(opts telegram.IdeasOptions) outgoing {
		doc := telegram.PicksDoc(picks, lk.Cited, opts, a.now(), a.Cfg.DisplayLocation)
		return outgoing{
			title:    title,
			messages: telegram.RenderPicks(picks, lk.Cited, opts, a.Cfg.DisplayLocation),
			summary:  telegram.PicksSummary(picks, opts),
			doc:      &doc,
		}
	}
	owner := picksFor(telegram.IdeasOptions{})
	messages := owner.messages
	cached.Save("shown", shown)
	cached.Text("messages.html", joinMessages(messages))
	cached.Text("summary.html", owner.summary.Text)
	ids, err := a.send(ctx, prefs.ChatID, owner, false)
	if len(ids) > 0 {
		// Cleared with the brief it follows, when REPLACE_PREVIOUS is on.
		if saveErr := a.UpdatePrefs(func(p *config.Prefs) error {
			p.LastBrief = append(p.LastBrief, ids...)
			return nil
		}); saveErr != nil {
			a.Log.Warn("could not record the closer look's message ids", "error", saveErr)
		}
	}
	if err != nil {
		a.Log.Warn("could not deliver the closer look", "error", err)
		cached.Fail(err)
		return
	}

	// The channel is posted before the verdicts are scored, for the reason the
	// brief is: the reader's copy should not wait on bookkeeping.
	if lk.Share {
		a.shareIdeas(ctx, picksFor(telegram.IdeasOptions{ForChannel: true}))
	}

	a.recordVerdicts(ctx, shown, charts)
	a.Log.Info("ideas delivered",
		"weekly", weekly,
		"themes", len(picks.Themes),
		"reactions", len(picks.Reactions),
		"messages", len(messages),
		"took", time.Since(started).Round(time.Second))
}

// following is what the watchlists follow, which the closer look leaves to
// the brief: US tickers, and every name and ticker they carry.
type following struct {
	tickers map[string]bool
	names   []string
}

func newFollowing(groups []model.Group) following {
	f := following{tickers: map[string]bool{}, names: trackedNames(groups)}
	for _, t := range watchedTickers(groups) {
		f.tickers[t] = true
	}
	return f
}

// follows reports whether an idea is a company the watchlists follow.
func (f following) follows(idea model.Idea) bool {
	if (idea.Exchange == "US" || idea.Exchange == "") && f.tickers[idea.Ticker] {
		return true
	}
	for _, n := range f.names {
		if strings.EqualFold(n, idea.Name) || strings.EqualFold(n, market.PlainName(idea.Listed)) {
			return true
		}
	}
	return false
}

// judge reads the facts for some companies and judges them. It returns the
// verdicts, in the order given, what was read for each company given, by
// position, and what the verdicts cost.
func (a *App) judge(ctx context.Context, list []model.Idea, sheets []sheet, cited []model.Article, backdrop []string) []model.Idea {
	facts := make([]string, len(sheets))
	for i, s := range sheets {
		facts[i] = s.facts
	}
	cached := runcache.From(ctx)
	cached.Save("facts", factsView(list, sheets))
	judged, usage, err := a.Judge.Judge(ctx, list, facts, cited, backdrop)
	if err != nil {
		a.Log.Warn("some verdicts are missing", "error", err)
	}
	cached.Save("verdicts", judged)
	a.Log.Info("ideas judged",
		"judged", len(judged),
		"of", len(list),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens)
	return judged
}

// factsView pairs each company judged with the facts it was judged on and the
// chart it was priced from, for the run cache.
func factsView(list []model.Idea, sheets []sheet) any {
	type row struct {
		Ticker    string `json:"ticker"`
		Name      string `json:"name"`
		Chart     string `json:"chart,omitempty"`
		Valuation string `json:"valuation,omitempty"`
		Facts     string `json:"facts"`
	}
	out := make([]row, len(list))
	for i, idea := range list {
		out[i] = row{Ticker: idea.Ticker, Name: idea.Name, Chart: sheets[i].chart, Valuation: idea.Valuation, Facts: sheets[i].facts}
	}
	return out
}

// sheet is what was read for one company: the fact sheet its verdict rests
// on, the chart its price comes from, and the accounts and the analysts'
// figures behind the sheet, where they were read.
type sheet struct {
	chart  string
	facts  string
	snap   *fundamentals.Snapshot
	expect *consensus.Report
}

// factsFor reads each idea's facts, a few companies at a time. The ideas
// themselves are updated in place with their prices and whether their
// accounts were read.
func (a *App) factsFor(ctx context.Context, all []model.Idea) []sheet {
	sheets := make([]sheet, len(all))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range factWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				all[i], sheets[i] = a.ideaFacts(ctx, all[i])
			}
		}()
	}
queue:
	for i := range all {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	wg.Wait()
	return sheets
}

// ideaFacts reads what a verdict should rest on, and returns the idea with its
// prices attached, the symbol the chart source knows it by, and the fact sheet
// written for the model.
//
// For a US listing that files with the SEC, the sheet is what /analyse reads:
// five years of accounts, what the company says it does and has told the SEC
// lately, valuation and trading, what analysts expect, the latest results
// release at the same length, and what has been reported about it. The one
// thing left out is the analysis's two web searches for news, which would
// spend the search allowance on every company every day; the news feed's
// stories stand in for them. For anything else it is the price and the
// trading alone, and it says so, so the verdict cannot quietly pretend to a
// knowledge of the accounts it does not have.
func (a *App) ideaFacts(ctx context.Context, idea model.Idea) (model.Idea, sheet) {
	chart := prices.ChartSymbol(idea.Ticker, idea.Exchange)
	if chart != "" {
		idea.Trading, idea.Quote = a.marketFor(ctx, chart)
	}

	if idea.Exchange == "US" || idea.Exchange == "" {
		// A US listing prefers the quote feed, which is live where the chart's
		// last bar is a close. Everywhere else the close is what there is, and
		// it carries the currency it was struck in.
		if live := a.quoteFor(ctx, idea.Ticker); live != nil {
			idea.Quote = live
		}
		if a.Accounts != nil {
			snap, err := a.Accounts.Fetch(ctx, idea.Ticker, accountYears)
			if err == nil {
				snap.Price, snap.Trading = idea.Quote, idea.Trading
				for _, problem := range fundamentals.AddBusiness(ctx, a.Filings, &snap, a.now()) {
					a.Log.Info("idea context", "ticker", idea.Ticker, "error", problem)
				}
				a.addIdeaNews(ctx, &snap, idea.Name, true)
				expect := a.addExpectations(ctx, &snap)
				a.addRelease(ctx, &snap, analysisReleaseRunes)
				idea.Accounts = true
				return idea, sheet{chart: chart, facts: snap.Table() + snap.SensitivityFacts(), snap: &snap, expect: expect}
			}
			a.Log.Info("no accounts for an idea", "ticker", idea.Ticker, "error", err)
		}
	}

	var b strings.Builder
	if idea.Trading != nil {
		b.WriteString(fundamentals.TradingFacts(idea.Trading))
	} else {
		b.WriteString("No price history could be read for it.\n")
	}
	b.WriteString("\nNo SEC accounts were read for it: it does not file with the SEC, or its filings could not be read. Say that the accounts are missing, and weigh your confidence accordingly.\n")
	// What has been written about it, so even a company without accounts is
	// judged on more than its chart and one article.
	news := fundamentals.Snapshot{Ticker: idea.Ticker, Company: idea.Name}
	a.addIdeaNews(ctx, &news, idea.Name, idea.Exchange == "US" || idea.Exchange == "")
	b.WriteString(news.NewsFacts())
	return idea, sheet{chart: chart, facts: b.String()}
}

// ideaSearchWindow is how far back a company judged by the closer look is
// searched for: the last fortnight, which is the news a verdict should not
// miss.
const ideaSearchWindow = 14 * 24 * time.Hour

// addIdeaNews attaches what has been written about a company the closer
// look judges: the news feed's stories where it is a US listing, and one
// news search, so a verdict can check its case in more than one source
// rather than lean on the single article that brought the company in. One
// search rather than /analyse's two: the closer look judges up to sixteen
// companies a week and six a day, and the search allowance is shared with
// the brief.
func (a *App) addIdeaNews(ctx context.Context, snap *fundamentals.Snapshot, name string, us bool) {
	var (
		fromFeed []model.Article
		wg       sync.WaitGroup
	)
	if us && a.Press.Enabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, newsBudget)
			defer cancel()
			found, err := a.Press.Company(ctx, snap.Ticker, a.now())
			if err != nil {
				a.Log.Info("company news", "ticker", snap.Ticker, "error", err)
			}
			fromFeed = found
		}()
	}
	var fromSearch []model.Article
	if a.Search.Enabled() {
		if name == "" {
			name = snap.Ticker
		}
		query := search.Query{Label: "idea:" + snap.Ticker, Text: fmt.Sprintf("%s (%s) news, results and outlook", search.PlainName(name), snap.Ticker)}
		sctx, cancel := context.WithTimeout(ctx, newsBudget)
		found := a.Search.Collect(sctx, []search.Query{query}, a.now().Add(-ideaSearchWindow))
		cancel()
		for _, err := range found.Errors {
			a.Log.Warn("idea search", "ticker", snap.Ticker, "error", err)
		}
		fromSearch = found.Articles
	}
	wg.Wait()
	fundamentals.SetNews(snap, fromSearch, fromFeed)
	a.Log.Info("idea news", "ticker", snap.Ticker, "from_feed", len(fromFeed), "from_search", len(fromSearch), "kept", len(snap.News))
}

// recordVerdicts writes each verdict shown to the scorecard, with the chart
// each is priced from, by symbol. The price each is measured from is not
// known yet: it is the next session's open, which /scorecard reads once it
// has happened.
func (a *App) recordVerdicts(ctx context.Context, shown []model.Idea, charts map[string]string) {
	if a.Scorecard == nil {
		return
	}
	var records []ideas.Record
	var currencies []string
	for _, idea := range shown {
		if r, ok := ideas.NewRecord(idea, charts[idea.Symbol()], a.now()); ok {
			records = append(records, r)
			currencies = append(currencies, r.Currency)
		}
	}
	// A share priced abroad is scored in dollars, from the rate on the day
	// of its entry. The rate now is kept beside it, for a history that does
	// not reach that day.
	rates := a.dollarRates(ctx, currencies)
	for i := range records {
		records[i].FX = rates.Latest(records[i].Currency)
	}
	if err := a.Scorecard.Add(records...); err != nil {
		a.Log.Warn("could not record the verdicts", "error", err)
		return
	}
	a.Log.Info("verdicts recorded", "count", len(records), "of", len(shown))
}

// pathFor is a chart's sessions as the scorecard reads them, or nil.
func (a *App) pathFor(ctx context.Context, chart string) ideas.Path {
	if a.Market == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, historyBudget)
	defer cancel()
	series, err := a.Market.Fetch(ctx, chart)
	if err != nil {
		return nil
	}
	path := make(ideas.Path, len(series.Bars))
	for i, bar := range series.Bars {
		path[i] = ideas.Session{Opened: bar.Opened, Open: bar.Open, Close: bar.Close}
	}
	return path
}

// scorecardSymbols bounds how many shares /scorecard prices in one go. The
// chart source is read one symbol at a time, and a reply that takes minutes is
// not one anybody waits for.
const scorecardSymbols = 60

// handleScorecard says how past verdicts have done against the index.
func (a *App) handleScorecard(ctx context.Context, msg telegram.Message) error {
	if a.Scorecard == nil {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, "The scorecard is not kept on this install.")
	}

	due := a.Scorecard.Due(a.now(), scorecardSymbols)
	if len(due) > 0 {
		if err := a.Bot.SendMessage(ctx, msg.Chat.ID, "Pricing past verdicts — a moment."); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	paths := map[string]ideas.Path{}
	for _, chart := range due {
		if p := a.pathFor(ctx, chart); len(p) > 0 {
			paths[chart] = p
		}
	}
	var bench ideas.Path
	var rates ideas.Rates
	if len(due) > 0 {
		bench = a.pathFor(ctx, ideas.Benchmark)
		rates = a.dollarRates(ctx, a.Scorecard.Currencies(a.now()))
		// Each verdict's entry is written down once its session has been,
		// so it is still there when the history no longer reaches it.
		if err := a.Scorecard.Settle(paths, bench, rates); err != nil {
			a.Log.Warn("could not save the verdicts' entries", "error", err)
		}
	}

	text := a.Scorecard.Summary(a.now(), paths, bench, rates, a.Cfg.DisplayLocation)
	return a.Bot.SendMessage(ctx, msg.Chat.ID, text)
}

// dollarRates reads what each currency is worth in US dollars, day by day over
// the chart source's two years. A currency that cannot be read is left out,
// and the verdicts priced in it go unscored rather than scored in the wrong
// money.
func (a *App) dollarRates(ctx context.Context, currencies []string) ideas.Rates {
	rates := ideas.Rates{}
	if a.Market == nil {
		return rates
	}
	for _, cur := range currencies {
		chart := prices.DollarRateChart(cur)
		if _, done := rates[cur]; done || chart == "" {
			continue
		}
		fetch, cancel := context.WithTimeout(ctx, historyBudget)
		series, err := a.Market.Fetch(fetch, chart)
		cancel()
		if err != nil {
			a.Log.Warn("exchange rate not read", "currency", cur, "error", err)
			continue
		}
		var hist []ideas.Rate
		for _, bar := range series.Bars {
			hist = append(hist, ideas.Rate{Date: bar.Date, USD: bar.Close})
		}
		rates[cur] = hist
	}
	return rates
}

// confidenceRank orders verdicts for the cut to a limit: high before medium
// before low.
func confidenceRank(c string) int {
	switch c {
	case "high":
		return 0
	case "medium":
		return 1
	}
	return 2
}

// best keeps up to n of the ideas, the most confident first where there are
// more, and otherwise in the order given.
func best(list []model.Idea, n int) []model.Idea {
	if len(list) <= n {
		return list
	}
	order := make([]int, len(list))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return confidenceRank(list[order[i]].Confidence) < confidenceRank(list[order[j]].Confidence)
	})
	keep := map[int]bool{}
	for _, i := range order[:n] {
		keep[i] = true
	}
	var out []model.Idea
	for i, idea := range list {
		if keep[i] {
			out = append(out, idea)
		}
	}
	return out
}

// trackedNames is every company the watchlists name, by name where they have
// one and by ticker otherwise, once each.
func trackedNames(groups []model.Group) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if k := strings.ToLower(s); s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	for _, g := range groups {
		for _, c := range g.Companies {
			for _, n := range c.Names() {
				add(n)
			}
			add(c.Symbol)
		}
	}
	return out
}
