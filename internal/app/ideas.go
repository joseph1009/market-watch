package app

import (
	"context"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// "Worth a closer look" follows the brief: companies today's news bears on,
// found by research on the web, each with a buy, hold or sell verdict. See the
// ideas package for how, and RUNBOOK.md for the stages.
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
// reasoning is written out in RUNBOOK.md.
//
// It is still never what /share posts: /share passes on the last brief or
// analysis, and a verdict reaching the channel is the daily run's doing or
// nothing.

// ideasBudget bounds the whole pass: research with the web, the facts for each
// company, and the verdicts. The run lock is held throughout, so a /now sent
// meanwhile waits; this is what keeps that wait finite.
const ideasBudget = 25 * time.Minute

// ideaYears is how much of each company's accounts the verdict sees. Three
// years shows a direction without the table growing past what six companies
// can share in one request.
const ideaYears = 3

// sendIdeas researches, judges and delivers. Every failure costs this section
// only: the brief has already arrived. share says whether the channel gets it
// too, and carries the same value the brief was sent with, so the two never
// disagree about who is reading today.
func (a *App) sendIdeas(ctx context.Context, done *briefDone, share bool) {
	if a.Researcher == nil || a.Judge == nil || done == nil {
		return
	}
	prefs := a.Prefs()
	if prefs.ChatID == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, ideasBudget)
	defer cancel()
	started := time.Now()

	found, usage, err := a.Researcher.Propose(ctx, ideas.Input{
		Brief:      briefText(done.rep),
		Cited:      done.rep.Cited,
		Candidates: done.rep.Candidates,
		Tracked:    trackedNames(prefs.Groups),
	})
	if err != nil {
		a.Log.Warn("could not research companies worth a closer look", "error", err)
		return
	}
	a.Log.Info("ideas researched",
		"verified", len(found),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"took", time.Since(started).Round(time.Second))
	if len(found) == 0 {
		return
	}

	facts := make([]string, len(found))
	charts := make([]string, len(found))
	for i := range found {
		found[i], charts[i], facts[i] = a.ideaFacts(ctx, found[i])
	}

	judged, vusage, err := a.Judge.Judge(ctx, found, facts, done.rep.Cited)
	if err != nil {
		a.Log.Warn("could not judge the companies worth a closer look", "error", err)
		return
	}
	a.Log.Info("ideas judged",
		"judged", len(judged),
		"of", len(found),
		"input_tokens", vusage.InputTokens,
		"output_tokens", vusage.OutputTokens)
	if len(judged) == 0 {
		return
	}

	messages := telegram.RenderIdeas(judged, done.rep.Cited, telegram.IdeasOptions{})
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
		return
	}

	// The channel is posted before the verdicts are scored, for the reason the
	// brief is: the reader's copy should not wait on bookkeeping.
	if share {
		a.shareIdeas(ctx, telegram.RenderIdeas(judged, done.rep.Cited, telegram.IdeasOptions{ForChannel: true}))
	}

	a.recordVerdicts(ctx, judged, found, charts)
	a.Log.Info("ideas delivered",
		"messages", len(messages),
		"took", time.Since(started).Round(time.Second))
}

// ideaFacts reads what a verdict should rest on, and returns the idea with its
// prices attached, the symbol the chart source knows it by, and the fact sheet
// written for the model.
//
// For a US listing that files with the SEC, the sheet is the same table the
// analysis reads: accounts, valuation and trading. For anything else it is the
// price and the trading alone, and it says so, so the verdict cannot quietly
// pretend to a knowledge of the accounts it does not have.
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
		for _, n := range g.Names {
			add(n)
		}
		for _, t := range g.Tickers {
			add(t)
		}
	}
	return out
}
