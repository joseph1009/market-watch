package app

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"

	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/runcache"
)

// The day's reactions: shares that moved several times their usual on the
// last session, on heavy trading, where the brief's articles say why. The
// market repriced them; the verdict asks whether it repriced them by the
// right amount. Results come first, since a share's drift after its results
// is where the market's misjudgment has been measured most often.

const (
	// moveTimes is how far beyond its usual daily move a share must go, and
	// moveBusy how far beyond its usual trading, to count as repriced.
	moveTimes = 3
	moveBusy  = 2

	// movesRead is how many of the largest moves are matched to articles, to
	// find the few the news explains.
	movesRead = 40

	// reactionHistory is how much of the market's history the reactions
	// read: the sixty sessions a share's usual move is measured over, and
	// room for holidays.
	reactionHistory = 120 * 24 * time.Hour
)

// resultsWords mark an article about a company's results or its outlook.
var resultsWords = regexp.MustCompile(`(?i)\b(results?|earnings|quarter(ly)?|q[1-4]|guidance|outlook|forecast|revenue|profit|sales|beats?|miss(es)?)\b`)

// runReactions finds, judges and chooses the day's reactions, and returns
// those shown with the chart each is priced from.
func (a *App) runReactions(ctx context.Context, lk look, listings []market.Listing, f following, backdrop []string) ([]model.Idea, map[string]string) {
	cached := runcache.From(ctx)
	panel, err := a.MarketStore.Load(a.now().Add(-reactionHistory), nil)
	if err != nil {
		a.Log.Warn("no reactions: the market's recent history could not be read", "error", err)
		return nil, nil
	}
	if len(panel.Dates) < 62 {
		a.Log.Warn("no reactions: the market's recent history is not there yet", "sessions", len(panel.Dates))
		return nil, nil
	}
	moves := market.Moves(panel, listings, market.DefaultRules, moveTimes, moveBusy)

	type found struct {
		idea    model.Idea
		results bool
		times   float64
	}
	var cands []found
	for _, m := range moves[:min(movesRead, len(moves))] {
		name := market.PlainName(m.Name)
		idea := model.Idea{Name: name, Listed: m.Name, Ticker: m.Symbol, Exchange: "US", Kind: model.IdeaReaction}
		if f.follows(idea) {
			continue
		}
		articles := fundamentals.Relevant(lk.Cited, m.Symbol, name, 3)
		if len(articles) == 0 {
			continue // a move no article explains cannot be judged against its news
		}
		results := false
		for _, art := range articles {
			if resultsWords.MatchString(art.Title) {
				results = true
			}
		}
		idea.Articles = articles
		idea.Link = fmt.Sprintf("%s %.1f%% on %s, %.1f times its usual daily move, on %.1f times its usual trading.",
			direction(m.Percent), math.Abs(100*m.Percent), m.Date.Format("Mon 2 Jan"), m.Times, m.Busy)
		cands = append(cands, found{idea, results, m.Times})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].results != cands[j].results {
			return cands[i].results
		}
		return cands[i].times > cands[j].times
	})
	var list []model.Idea
	for _, c := range cands[:min(ideas.ReactionsJudged, len(cands))] {
		list = append(list, c.idea)
	}
	cached.Save("moves", moves[:min(movesRead, len(moves))])
	cached.Save("reactions", list)
	a.Log.Info("reactions found", "moves", len(moves), "explained", len(cands), "judged", len(list))
	if len(list) == 0 {
		return nil, nil
	}

	sheets := a.factsFor(ctx, list)
	charts := map[string]string{}
	for i, s := range sheets {
		charts[list[i].Symbol()] = s.chart
		if s.snap == nil {
			continue
		}
		// The warning signs, without a theme to set the price against.
		var target, insiders, short float64
		if e := s.expect; e != nil {
			target, insiders, short = e.Target, e.InsiderNet3, e.ShortShares
		}
		var price, ma200 float64
		if t := list[i].Trading; t != nil {
			price, ma200 = t.Last, t.MA200
		}
		list[i].Flags = ideas.WarningSigns(price, ma200, target, insiders, short, s.snap.Balance.Figure("sharesOutstanding").Amount)
	}
	judged := a.judge(ctx, list, sheets, lk.Cited, backdrop)

	var shown []model.Idea
	for _, j := range judged {
		if j.Shown() {
			shown = append(shown, j)
		}
	}
	return best(shown, ideas.ReactionsShown), charts
}

func direction(move float64) string {
	if move < 0 {
		return "Down"
	}
	return "Up"
}
