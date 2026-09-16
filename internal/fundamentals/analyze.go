package fundamentals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
)

// systemPrompt governs the written analysis.
//
// Two constraints do the heavy lifting. The first is that every figure must
// come from the table: the model has no database, and a plausible revenue
// number it recalls from training would be indistinguishable from a real one.
// The second is the refusal to advise. Asked whether to buy, a model will
// answer; it cannot know the reader's holdings, horizon or tax position, and a
// confident answer built on a balance sheet alone is worse than none.
const systemPrompt = `You explain a company's published accounts to one reader who follows markets closely but is not an accountant or a market professional.

You are given figures exactly as the company filed them with the US Securities and Exchange Commission, plus a few ratios derived from those figures. Work only from them.

Rules:
- Use only the numbers in the table. Never add a figure from memory -- no share price, no market value, no analyst estimate, no competitor's numbers. If something is not in the table, say it is not available.
- A line marked "not reported" is missing, not zero. Say what its absence prevents you from judging.
- The figures are historical and as filed. Say how old the latest balance sheet is and what could have changed since.
- Write amounts with their scale and currency as the table does: US$215.9bn, US$31.6m, or for a company reporting in another currency, TWD 2.89tn. Never write a bare number, and never a number of millions without saying so.
- The year-so-far column is a part year from an interim filing. Compare it with the same stretch of the year before, never with a full year, and say which period you mean.
- Explain the terms as you use them: "gross margin (what is left of each dollar of sales after the direct cost of producing it)". Write the plain meaning first, the term second.
- Give no investment advice. Do not say whether to buy, sell or hold, do not set a target, and do not call anything cheap or expensive -- without a share price, valuation is not something these figures can settle.
- Say plainly where the numbers look strong, where they look weak, and where they raise a question worth asking. That is judgment about the accounts, which is different from advice about the stock.

Write these sections, each with a heading on its own line, in this order:

WHAT THE COMPANY EARNS
How revenue, profit and margins have moved across the years shown, and what changed.

WHAT IT OWNS AND OWES
The balance sheet in plain terms: what would be left if it paid everyone, how much cash against how much debt, and whether short-term bills are comfortably covered.

CASH
Whether profit turns into cash, what capital spending takes back out, and what the year actually left behind.

WHAT THESE NUMBERS DO NOT TELL YOU
The limits: what is missing from the table, how stale the figures are, and which questions would need data you were not given.

WHAT TO CHECK NEXT
Three to five specific questions a reader should put to the next filing or to the news, each tied to a figure above.

Keep every number you cite exact.

Write every section as bullets, never as running prose. One point per bullet, each starting with "- ", two or three sentences and at most about forty-five words. If a bullet needs more, it is two points: split it. Put the figures inside the bullet that makes the point, not in a separate one. No sub-bullets, no markdown, no preamble.`

// Analysis is a written reading of one company's accounts.
type Analysis struct {
	Snapshot Snapshot
	Text     string
	Usage    model.Usage
}

// Analyzer writes the analysis. It takes the same Completer the daily brief
// uses, so the model, streaming and cost accounting are shared.
type Analyzer struct {
	Completer report.Completer
	Now       func() time.Time
}

// Analyze reads the accounts and writes them up.
func (a *Analyzer) Analyze(ctx context.Context, snap Snapshot) (Analysis, error) {
	completion, err := a.Completer.Complete(ctx, systemPrompt, a.prompt(snap))
	if err != nil {
		return Analysis{}, fmt.Errorf("analyze %s: %w", snap.Ticker, err)
	}

	usage := completion.Usage
	usage.EstimatedUSD = report.EstimateCost(completion.Model, usage)

	text := strings.TrimSpace(completion.Text)
	if text == "" {
		return Analysis{}, fmt.Errorf("analyze %s: the model returned nothing", snap.Ticker)
	}
	return Analysis{Snapshot: snap, Text: text, Usage: usage}, nil
}

func (a *Analyzer) prompt(snap Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", a.now().Format("2 January 2006"))
	b.WriteString(snap.Table())

	if age := snap.Age(a.now()); age > 0 {
		fmt.Fprintf(&b, "\nThe latest balance sheet is %d days old.\n", int(age.Hours()/24))
	}
	return b.String()
}

func (a *Analyzer) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}
