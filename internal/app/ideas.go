package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/runcache"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// "Worth a closer look" follows the brief: twenty companies a day, each with a
// buy, hold or sell verdict. Up to six are companies the watchlists follow,
// where a screen of all of them found the move and the news not fitting each
// other, and new names -- found by research on the web from the day's news and
// the market's largest moves -- fill the rest. See the ideas package for how,
// and docs/RUNBOOK.md for the stages.
//
// The daily run's closer look arrives an hour after the brief (look.go), so
// the brief is never held up by it and the plan's allowance is not asked for
// both at once. A brief asked for with /now, or sent with -once, is followed
// at once.
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
	// ideasBudget bounds the whole pass: research with the web and the screen
	// side by side, the facts for each company, and the verdicts. It usually
	// takes ten to fifteen minutes. The run lock is held throughout, so a /now
	// sent meanwhile waits; this is what keeps that wait finite.
	ideasBudget = 30 * time.Minute

	// ideaYears is how much of each company's accounts the verdict sees. Three
	// years shows a direction without a batch of five outgrowing one request.
	ideaYears = 3

	// ideaReleaseRunes is how much of each results release a verdict reads:
	// the headline figures and the quarter's table, which is where the
	// numbers a verdict turns on are. /analyse reads twice as much.
	ideaReleaseRunes = 3500

	// factWorkers is how many companies' facts are read at once. Each is SEC
	// reads, paced across all of them by the accounts client, and six Nasdaq
	// reads of a second or two each.
	factWorkers = 4

	// screenWorkers is how many followed companies' histories are read at once
	// for the screen.
	screenWorkers = 4

	// moversBudget bounds the market's movers: two to five requests, twelve
	// and a half seconds apart on the free plan.
	moversBudget = 2 * time.Minute

	// The movers the research is shown: fifteen of them, biggest move first,
	// each a share of at least US$5 on which at least US$25m changed hands, in
	// a company worth at least US$2bn. On the first day it was read, without
	// the last floor, the list was a real-estate trust up 191% and three
	// biotechs a tenth of that size -- spikes, not news. The sixty largest
	// moves are sized, to find fifteen that clear it.
	moverMinPrice  = 5
	moverMinVolume = 25e6
	moverMinValue  = 2e9
	moverCount     = 15
	moverSized     = 60
)

// look is what the closer look starts from: the brief it follows, and whether
// the channel is reading today. It is written to the data volume while it
// waits its hour, so it carries the brief's text and articles rather than the
// report, whose citations are not kept on disk.
type look struct {
	Due        time.Time         `json:"due"`
	Share      bool              `json:"share"`
	Brief      string            `json:"brief"`
	Cited      []model.Article   `json:"cited"`
	Candidates []model.Candidate `json:"candidates,omitempty"`
}

func lookFrom(rep model.Report, share bool) look {
	return look{Share: share, Brief: briefText(rep), Cited: rep.Cited, Candidates: rep.Candidates}
}

// sendIdeas researches, screens, judges and delivers. Every failure costs this
// section only: the brief has already arrived. lk.Share says whether the
// channel gets it too, and carries the value the brief was sent with, so the
// two never disagree about who is reading today.
func (a *App) sendIdeas(ctx context.Context, lk look) {
	if a.Researcher == nil || a.Judge == nil {
		return
	}
	prefs := a.Prefs()
	if prefs.ChatID == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, ideasBudget)
	defer cancel()
	started := time.Now()

	ctx, cached := a.Cache.Start(ctx, runcache.Recommendations, "")
	defer cached.Finish(nil)
	cached.Save("look", lk)

	// The new names and the followed companies are found side by side: the
	// research is minutes of web searches, the screen a minute of prices and
	// one short call, and neither needs the other.
	var found, picked []model.Idea
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); found = a.researchNewNames(ctx, lk, prefs.Groups) }()
	go func() { defer wg.Done(); picked = a.screenFollowed(ctx, lk, prefs.Groups) }()
	wg.Wait()

	// LookSize are shown: the followed companies, and new names for the
	// rest, best first. The rest of the research stands by.
	want := min(ideas.LookSize-len(picked), len(found))
	all, spare := append(picked, found[:want]...), found[want:]
	if len(all) == 0 {
		cached.Fail(errors.New("nothing to judge: the research and the screen found no companies"))
		return
	}

	backdrop := a.backdrop(ctx)
	cached.Save("backdrop", backdrop)
	shown, charts, judged, usage := a.judgeIdeas(ctx, all, lk.Cited, backdrop)

	// A followed HOLD is not shown, and a verdict can fail. Each place left
	// goes to the next new name, whose verdict is always shown, in one more
	// round: judging the spares up front would spend a verdict on each of
	// them every day, for the one or two a day that are needed.
	var standIns []model.Idea
	if short := min(ideas.LookSize-len(shown), len(spare)); short > 0 {
		standIns = spare[:short]
		more, moreCharts, moreJudged, moreUsage := a.judgeIdeas(ctx, standIns, lk.Cited, backdrop)
		shown = append(shown, more...)
		all, charts = append(all, standIns...), append(charts, moreCharts...)
		judged += moreJudged
		usage.InputTokens += moreUsage.InputTokens
		usage.OutputTokens += moreUsage.OutputTokens
	}
	a.Log.Info("ideas judged",
		"judged", judged,
		"of", len(all),
		"shown", len(shown),
		"stand_ins", len(standIns),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens)
	if len(shown) == 0 {
		cached.Fail(errors.New("nothing to show: every verdict was a hidden HOLD or failed"))
		return
	}

	messages := telegram.RenderIdeas(shown, lk.Cited, telegram.IdeasOptions{})
	cached.Save("shown", shown)
	cached.Text("messages.html", joinMessages(messages))
	ids, err := a.Bot.SendReport(ctx, prefs.ChatID, messages)
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
		a.shareIdeas(ctx, telegram.RenderIdeas(shown, lk.Cited, telegram.IdeasOptions{ForChannel: true}))
	}

	a.recordVerdicts(ctx, shown, all, charts)
	a.Log.Info("ideas delivered",
		"messages", len(messages),
		"took", time.Since(started).Round(time.Second))
}

// judgeIdeas reads the facts for some companies and judges them. It returns
// the verdicts to show, the chart symbol of each company it was given, in
// order, how many verdicts came back, and what they cost.
func (a *App) judgeIdeas(ctx context.Context, list []model.Idea, cited []model.Article, backdrop []string) ([]model.Idea, []string, int, model.Usage) {
	facts, charts := a.factsFor(ctx, list)
	cached := runcache.From(ctx)
	cached.Save("facts", factsView(list, facts, charts))
	judged, usage, err := a.Judge.Judge(ctx, list, facts, cited, backdrop)
	if err != nil {
		a.Log.Warn("some verdicts are missing", "error", err)
	}
	cached.Save("verdicts", judged)
	var shown []model.Idea
	for _, idea := range judged {
		if idea.Shown() {
			shown = append(shown, idea)
		}
	}
	return shown, charts, len(judged), usage
}

// factsView pairs each company judged with the facts it was judged on and the
// chart it was priced from, for the run cache.
func factsView(list []model.Idea, facts, charts []string) any {
	type row struct {
		Ticker string `json:"ticker"`
		Name   string `json:"name"`
		Chart  string `json:"chart,omitempty"`
		Facts  string `json:"facts"`
	}
	out := make([]row, len(list))
	for i, idea := range list {
		out[i] = row{Ticker: idea.Ticker, Name: idea.Name, Chart: charts[i], Facts: facts[i]}
	}
	return out
}

// researchNewNames finds the companies nobody follows that today's news, or
// today's largest moves, bear on.
func (a *App) researchNewNames(ctx context.Context, lk look, groups []model.Group) []model.Idea {
	started := time.Now()
	followed := map[string]bool{}
	for _, t := range watchedTickers(groups) {
		followed[t] = true
	}
	movers := a.marketMovers(ctx, followed)
	cached := runcache.From(ctx)
	cached.Save("market-movers", movers)
	found, usage, err := a.Researcher.Propose(ctx, ideas.Input{
		Brief:      lk.Brief,
		Cited:      lk.Cited,
		Candidates: lk.Candidates,
		Tracked:    trackedNames(groups),
		Followed:   followed,
		Movers:     movers,
	})
	if err != nil {
		a.Log.Warn("could not research new names for a closer look", "error", err)
		return nil
	}
	cached.Save("research", found)
	a.Log.Info("ideas researched",
		"verified", len(found),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"took", time.Since(started).Round(time.Second))
	return found
}

// marketMovers are the day's largest moves among the US companies nobody
// follows, named as the SEC knows them. Funds, and the notes a bank issues,
// are left out -- a leveraged fund moving three times its index is arithmetic,
// not news -- and so are companies worth less than moverMinValue, whose moves
// are mostly noise.
func (a *App) marketMovers(ctx context.Context, followed map[string]bool) []ideas.Mover {
	if !a.Movers.Enabled() || a.Filings == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, moversBudget)
	defer cancel()

	type filer struct {
		cik  int
		name string
	}
	filers := map[string]filer{}
	moves, err := a.Movers.Movers(ctx, a.now(), prices.MoverRules{
		MinPrice:        moverMinPrice,
		MinDollarVolume: moverMinVolume,
		Limit:           moverSized,
		Keep: func(symbol string) bool {
			if followed[symbol] {
				return false
			}
			// The SEC writes a share class with a dash: BRK-B, not BRK.B.
			sec := strings.ReplaceAll(symbol, ".", "-")
			cik, name, err := a.Filings.LookupCIK(ctx, sec)
			if err != nil || isFund(name) || !a.Filings.MainTicker(ctx, sec) {
				return false
			}
			filers[symbol] = filer{cik, name}
			return true
		},
	})
	if err != nil {
		a.Log.Warn("could not read the market's movers", "error", err)
		return nil
	}

	var out []ideas.Mover
	for _, m := range moves {
		if len(out) == moverCount {
			break
		}
		f := filers[m.Symbol]
		if a.Accounts != nil {
			shares, err := a.Accounts.SharesOutstanding(ctx, f.cik)
			if err != nil || m.Close*shares < moverMinValue {
				continue // too small, or no share count to tell
			}
		}
		out = append(out, ideas.Mover{Symbol: m.Symbol, Name: f.name, Percent: m.Percent, DollarVolume: m.DollarVolume})
	}
	a.Log.Info("market movers", "sized", len(moves), "kept", len(out))
	return out
}

// isFund reports whether a registered name is a fund's rather than a
// company's.
func isFund(name string) bool {
	upper := " " + strings.ToUpper(name) + " "
	for _, word := range []string{" ETF", " FUND", " ETN", "PROSHARES", "DIREXION", "ISHARES", "SPDR", "SHARES TRUST"} {
		if strings.Contains(upper, word) {
			return true
		}
	}
	return false
}

// screenFollowed chooses which of the followed companies get a verdict today.
func (a *App) screenFollowed(ctx context.Context, lk look, groups []model.Group) []model.Idea {
	if a.Screener == nil {
		return nil
	}
	started := time.Now()
	rows := a.screenRows(ctx, groups, lk.Cited)
	cached := runcache.From(ctx)
	cached.Save("screen-rows", rows)
	picked, usage, err := a.Screener.Pick(ctx, lk.Brief, lk.Cited, rows)
	if err != nil {
		a.Log.Warn("could not screen the followed companies", "error", err)
		return nil
	}
	cached.Save("screen", picked)
	a.Log.Info("followed companies screened",
		"rows", len(rows),
		"picked", len(picked),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"took", time.Since(started).Round(time.Second))
	return picked
}

// factsFor reads each idea's facts, a few companies at a time, and returns
// the ideas' fact sheets and chart symbols by position. The ideas themselves
// are updated in place with their prices.
func (a *App) factsFor(ctx context.Context, all []model.Idea) (facts, charts []string) {
	facts = make([]string, len(all))
	charts = make([]string, len(all))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range factWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				all[i], charts[i], facts[i] = a.ideaFacts(ctx, all[i])
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
	return facts, charts
}

// ideaFacts reads what a verdict should rest on, and returns the idea with its
// prices attached, the symbol the chart source knows it by, and the fact sheet
// written for the model.
//
// For a US listing that files with the SEC, the sheet is the same table the
// analysis reads: accounts, valuation and trading, what analysts expect, and
// the latest results release. For anything else it is the price and the
// trading alone, and it says so, so the verdict cannot quietly pretend to a
// knowledge of the accounts it does not have.
func (a *App) ideaFacts(ctx context.Context, idea model.Idea) (model.Idea, string, string) {
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
			snap, err := a.Accounts.Fetch(ctx, idea.Ticker, ideaYears)
			if err == nil {
				snap.Price, snap.Trading = idea.Quote, idea.Trading
				a.addExpectations(ctx, &snap)
				a.addRelease(ctx, &snap, ideaReleaseRunes)
				idea.Accounts = true
				return idea, chart, snap.Table()
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
	return idea, chart, b.String()
}

// recordVerdicts writes each verdict to the scorecard with the price it was
// given at and the index beside it. judged is a subset of found, and charts is
// found's, by position.
func (a *App) recordVerdicts(ctx context.Context, judged, found []model.Idea, charts []string) {
	if a.Scorecard == nil || a.Market == nil {
		return
	}
	bench := a.lastClose(ctx, ideas.Benchmark)
	if bench <= 0 {
		a.Log.Warn("verdicts not scored: the index could not be priced")
		return
	}

	chartOf := map[string]string{}
	for i, f := range found {
		chartOf[f.Symbol()] = charts[i]
	}

	var records []ideas.Record
	for _, idea := range judged {
		if r, ok := ideas.NewRecord(idea, chartOf[idea.Symbol()], bench, a.now()); ok {
			records = append(records, r)
		}
	}
	if err := a.Scorecard.Add(records...); err != nil {
		a.Log.Warn("could not record the verdicts", "error", err)
		return
	}
	a.Log.Info("verdicts recorded", "count", len(records), "of", len(judged))
}

// lastClose is a symbol's latest price from the chart source, or zero.
func (a *App) lastClose(ctx context.Context, chart string) float64 {
	if a.Market == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, historyBudget)
	defer cancel()
	series, err := a.Market.Fetch(ctx, chart)
	if err != nil || len(series.Bars) == 0 {
		return 0
	}
	return series.Bars[len(series.Bars)-1].Close
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

	now := map[string]float64{}
	for _, chart := range due {
		if p := a.lastClose(ctx, chart); p > 0 {
			now[chart] = p
		}
	}
	bench := 0.0
	if len(due) > 0 {
		bench = a.lastClose(ctx, ideas.Benchmark)
	}

	text := a.Scorecard.Summary(a.now(), now, bench, a.Cfg.DisplayLocation)
	return a.Bot.SendMessage(ctx, msg.Chat.ID, text)
}

// briefText is the brief's prose as the research reads it: the overview, then
// each section under its name, with the citations as written.
func briefText(rep model.Report) string {
	var b strings.Builder
	b.WriteString("OVERVIEW\n")
	b.WriteString(strings.TrimSpace(rep.Overview))
	for _, s := range rep.Sections {
		if strings.TrimSpace(s.Body) == "" {
			continue
		}
		b.WriteString("\n\n" + strings.ToUpper(s.GroupName) + "\n")
		b.WriteString(strings.TrimSpace(s.Body))
	}
	return b.String()
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
