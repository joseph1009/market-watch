// Package ideas finds companies worth a closer look and judges them.
//
// Two ways in, both starting from numbers rather than from the day's news.
//
// Once a week, from two years of the whole US market's prices
// (internal/market), and Singapore's thirty largest beside it, the themes: the
// shares that have risen furthest and most steadily, sorted by what drives
// them (Sorter, Opus); and apart from those, the industries whose business
// is growing before their shares have followed (Scout, Opus with the web).
// Each theme is then researched (Researcher, Opus with the web): what drives
// it, which part of it the market has already paid for, which part it has
// not, and which companies sit in that part -- the ones it has not paid for as
// BUY candidates, and one it has paid too much for as a SELL. The market's
// leaders are where the search starts, not what it recommends: a pick is as
// likely to be a company that has not risen yet as one that has.
//
// Every day, the reactions: shares that moved several times their usual on
// heavy trading, where the day's articles say why. Markets misjudge news
// often, and a share that fell a fifth on news that barely touches its
// earnings is the case the verdict looks for.
//
// Every candidate's ticker is checked against the exchange, and one that does
// not check out is dropped: a verdict on the wrong symbol is the worst thing
// this package could produce. The facts are then read -- the accounts, the
// price, the valuation against the theme and the company's own history, what
// analysts expect -- and the verdicts are written from those, with no tools,
// so the figures they cite are ones this service fetched rather than ones a
// web page asserted (Judge). The code holds each verdict to rules the model is
// told but cannot bend: no BUY where the price is dearer than the theme on
// every measure and not paid for by growth, and no more than low confidence
// without accounts.
//
// The verdicts go to the owner, and to the channel with the daily brief, where
// they are published under a note saying a model wrote them and that they are
// not advice. See internal/app/ideas.go.
package ideas

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/model"
)

const (
	// PicksShown is how many theme picks a week shows at most: fewer where
	// fewer hold up, never more to fill a number.
	PicksShown = 10

	// ReactionsShown is how many reactions a day shows at most, and
	// ReactionsJudged how many are given a verdict to find them: a reaction
	// that matched its news is a HOLD, and not shown.
	ReactionsShown  = 3
	ReactionsJudged = 6

	// PopularThemes and EarlyThemes are how many of each the week reads.
	PopularThemes = 3
	EarlyThemes   = 2

	// PerTheme is how many companies the research proposes in one theme, and
	// PicksJudged how many are given a verdict across them all.
	PerTheme    = 4
	PicksJudged = 16

	// RepeatWindow is how long a pick is not written up again unless its
	// verdict changes: it is on the earlier picks list meanwhile.
	RepeatWindow = 8 * 7 * 24 * time.Hour
)

// PickExchanges are where a pick may be listed: the US, where the accounts
// can be read, and Singapore, where the reader lives.
var PickExchanges = map[string]bool{"US": true, "SP": true}

// Completer is the model call every stage makes.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

// verify keeps the ideas whose ticker belongs to the company named, and drops
// the rest. Unlike a new name, an idea without a trustworthy ticker has no
// value: its whole point is a verdict on a share someone could buy.
func verify(ctx context.Context, v discover.Verifier, ideas []model.Idea) ([]model.Idea, error) {
	if v == nil {
		return nil, fmt.Errorf("no verifier")
	}

	var queries []discover.Query
	var asked []int
	for i, idea := range ideas {
		if _, ok := discover.Exchanges[idea.Exchange]; !ok {
			continue
		}
		queries = append(queries, discover.Query{Ticker: idea.Ticker, Exchange: idea.Exchange})
		asked = append(asked, i)
	}
	if len(queries) == 0 {
		return nil, nil
	}

	names, err := v.Verify(ctx, queries)
	if err != nil {
		return nil, err
	}

	var out []model.Idea
	for n, i := range asked {
		if n >= len(names) || names[n] == "" || !discover.SameCompany(names[n], ideas[i].Name) {
			continue
		}
		idea := ideas[i]
		idea.Listed = names[n]
		out = append(out, idea)
	}
	return out, nil
}

// field reads "LABEL: value" at the head of a line, in any case, with any
// bold the model added dropped.
func field(line, label string) (string, bool) {
	line = strings.TrimSpace(strings.ReplaceAll(line, "*", ""))
	if len(line) < len(label) || !strings.EqualFold(line[:len(label)], label) {
		return "", false
	}
	return strings.TrimSpace(line[len(label):]), true
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// pct writes a fraction as a signed percentage: +12.3%.
func pct(f float64) string { return fmt.Sprintf("%+.0f%%", 100*f) }
