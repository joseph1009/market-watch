package ideas

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// Judge gives each idea its verdict.
type Judge struct {
	Completer Completer

	// Batch is how many companies one call judges, and Concurrency how many
	// calls run at once.
	Batch       int
	Concurrency int

	// Now is the clock the dated events ahead are counted from. Nil means
	// time.Now.
	Now func() time.Time
}

const (
	// DefaultBatch keeps one request to five fact sheets, a little under a
	// hundred kilobytes: twenty companies in one would be a request of four
	// hundred, most of which the model would skim.
	DefaultBatch = 5

	// DefaultJudgeConcurrency runs two batches at a time, which judges twenty
	// in two rounds without asking the plan for four Opus calls at once.
	DefaultJudgeConcurrency = 2
)

// verdictsPrompt governs the verdicts. Its text lives in config/prompts.md.
var verdictsPrompt = config.Prompt("verdicts.system")

// Judge returns the ideas that received a verdict, in the order given. facts
// is each idea's fact sheet, by position: its trading, and its accounts and
// expectations where they were read. backdrop is the market's commodities and
// rates, written once above them all.
//
// An idea the reply skips, or answers with something other than BUY, HOLD or
// SELL, is dropped rather than shown without one: the section is verdicts, and
// a company listed without one would read as an oversight. A batch that fails
// costs its own companies; the error says how many batches that was, and the
// rest are returned.
func (j *Judge) Judge(ctx context.Context, ideas []model.Idea, facts []string, cited []model.Article, backdrop []string) ([]model.Idea, model.Usage, error) {
	if j.Completer == nil || len(ideas) == 0 {
		return nil, model.Usage{}, nil
	}

	size := j.batch()
	type outcome struct {
		blocks map[string]verdict
		usage  model.Usage
		err    error
	}
	var bounds [][2]int
	for start := 0; start < len(ideas); start += size {
		bounds = append(bounds, [2]int{start, min(start+size, len(ideas))})
	}
	outcomes := make([]outcome, len(bounds))
	sem := make(chan struct{}, j.concurrency())
	today := j.now()
	var wg sync.WaitGroup
	for i, b := range bounds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outcomes[i].err = ctx.Err()
				return
			}
			text, usage, err := j.Completer.Complete(ctx, verdictsPrompt,
				judgePrompt(ideas[b[0]:b[1]], facts[min(b[0], len(facts)):min(b[1], len(facts))], cited, backdrop, today))
			outcomes[i] = outcome{blocks: parseVerdicts(text), usage: usage, err: err}
		}()
	}
	wg.Wait()

	blocks := map[string]verdict{}
	var (
		usage  model.Usage
		failed int
		first  error
	)
	for _, o := range outcomes {
		usage.InputTokens += o.usage.InputTokens
		usage.OutputTokens += o.usage.OutputTokens
		if o.err != nil {
			failed++
			if first == nil {
				first = o.err
			}
			continue
		}
		for k, v := range o.blocks {
			blocks[k] = v
		}
	}

	var out []model.Idea
	for _, idea := range ideas {
		v, ok := blocks[strings.ToUpper(idea.Symbol())]
		if !ok {
			v, ok = blocks[strings.ToUpper(idea.Ticker)]
		}
		if !ok {
			continue
		}
		idea.Verdict, idea.Confidence = v.verdict, v.confidence
		idea.Case, idea.Numbers, idea.Risk = v.theCase, v.numbers, v.risk
		idea.Changed, idea.Moved, idea.Reaction = v.changed, v.moved, v.reaction
		idea.Catalyst, idea.Sensitivity = v.catalyst, v.sensitivity
		out = append(out, idea)
	}
	if failed > 0 {
		return out, usage, fmt.Errorf("%d of %d verdict batches failed: %w", failed, len(bounds), first)
	}
	return out, usage, nil
}

func (j *Judge) batch() int {
	if j.Batch > 0 {
		return j.Batch
	}
	return DefaultBatch
}

func (j *Judge) concurrency() int {
	if j.Concurrency > 0 {
		return j.Concurrency
	}
	return DefaultJudgeConcurrency
}

func (j *Judge) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func judgePrompt(ideas []model.Idea, facts []string, cited []model.Article, backdrop []string, today time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", today.Format("Monday 2 January 2006"))
	b.WriteString("The articles, numbered as in today's brief:\n")
	for i, a := range cited {
		fmt.Fprintf(&b, "[%d] %s (%s)\n", i+1, oneLine(a.Title), a.SourceName)
	}

	if len(backdrop) > 0 {
		b.WriteString("\nThe market backdrop, as measured by FRED:\n")
		for _, line := range backdrop {
			b.WriteString("- " + line + "\n")
		}
	}

	b.WriteString("\nThe companies:\n")
	for i, idea := range ideas {
		fmt.Fprintf(&b, "\n=== %s\n", idea.Symbol())
		fmt.Fprintf(&b, "%s, registered as %s.\n", idea.Name, idea.Listed)

		kind := "In today's news"
		switch {
		case idea.Followed:
			kind = "Followed by the investor, and chosen today because"
		case idea.Connected:
			kind = "Not in today's news, but connected to it"
		}
		fmt.Fprintf(&b, "%s: %s", kind, idea.Link)
		for _, a := range idea.Articles {
			if n := number(a, cited); n > 0 {
				fmt.Fprintf(&b, " [%d]", n)
			}
		}
		b.WriteString("\n")
		if idea.Followed {
			b.WriteString("Only BUY or SELL will be shown for it: a HOLD is left out.\n")
		}
		if e := idea.Event; e != nil {
			fmt.Fprintf(&b, "Ahead, as the research found it on the web and unchecked: %s on %s", e.Name, e.Date.Format("Monday 2 Jan 2006"))
			var rated []string
			if e.Impact != "" {
				rated = append(rated, strings.ToLower(e.Impact)+" impact")
			}
			if e.Bias != "" {
				rated = append(rated, strings.ToLower(e.Bias))
			}
			if len(rated) > 0 {
				fmt.Fprintf(&b, " (the research rates it %s)", strings.Join(rated, ", "))
			}
			b.WriteString(".\n")
		}

		if q := idea.Quote; q != nil {
			// With the unit, always. A Hong Kong listing is quoted in Hong Kong
			// dollars, and a bare 512.40 set beside a US company's 512.40
			// invites a comparison that means nothing.
			fmt.Fprintf(&b, "Last session: %s, at %s %.2f.\n", q.Move(), q.Unit(), q.Price)
			if late := untraded(idea.Articles, q.AsOf, cited); late != "" {
				fmt.Fprintf(&b, "Articles %s came out after that price: the market has not traded on them yet.\n", late)
			}
		}
		if i < len(facts) && strings.TrimSpace(facts[i]) != "" {
			b.WriteString(strings.TrimSpace(facts[i]))
			b.WriteString("\n")
		}
	}
	return b.String()
}

type verdict struct {
	verdict, confidence, theCase, numbers, risk string
	changed, moved, reaction                    string
	catalyst, sensitivity                       string
}

// parseVerdicts reads the reply's blocks, keyed by the symbol each opens with.
// A field may run onto the next line; a block with no valid verdict is left
// out.
func parseVerdicts(text string) map[string]verdict {
	out := map[string]verdict{}
	symbol := ""
	var cur verdict
	var last *string

	flush := func() {
		if symbol != "" && cur.verdict != "" {
			out[symbol] = cur
		}
		symbol, cur, last = "", verdict{}, nil
	}

	fields := []struct {
		label string
		into  func(*verdict) *string
	}{
		{"VERDICT:", func(v *verdict) *string { return &v.verdict }},
		{"CONFIDENCE:", func(v *verdict) *string { return &v.confidence }},
		{"CASE:", func(v *verdict) *string { return &v.theCase }},
		{"NUMBERS:", func(v *verdict) *string { return &v.numbers }},
		{"RISK:", func(v *verdict) *string { return &v.risk }},
		{"CHANGED:", func(v *verdict) *string { return &v.changed }},
		{"MOVE:", func(v *verdict) *string { return &v.moved }},
		{"REACTION:", func(v *verdict) *string { return &v.reaction }},
		{"CATALYST:", func(v *verdict) *string { return &v.catalyst }},
		{"SENSITIVITY:", func(v *verdict) *string { return &v.sensitivity }},
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "===") {
			flush()
			symbol = strings.ToUpper(strings.TrimSpace(strings.Trim(line, "= ")))
			continue
		}
		if symbol == "" || line == "" {
			continue
		}

		matched := false
		for _, f := range fields {
			if len(line) >= len(f.label) && strings.EqualFold(line[:len(f.label)], f.label) {
				p := f.into(&cur)
				*p = strings.TrimSpace(line[len(f.label):])
				last, matched = p, true
				break
			}
		}
		if !matched && last != nil {
			*last += " " + line
		}
	}
	flush()

	for k, v := range out {
		v.verdict = normaliseVerdict(v.verdict)
		v.confidence = normaliseConfidence(v.confidence)
		// Asked for "none" where the facts have no sensitivity figures,
		// which is every company without SEC accounts: not worth a line each.
		if strings.EqualFold(strings.Trim(v.sensitivity, ". "), "none") {
			v.sensitivity = ""
		}
		if v.verdict == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func normaliseVerdict(s string) string {
	s = strings.ToUpper(strings.Trim(strings.TrimSpace(s), ".*"))
	for _, v := range []string{model.Buy, model.Hold, model.Sell} {
		if s == v {
			return v
		}
	}
	return ""
}

func normaliseConfidence(s string) string {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), ".*"))
	for _, c := range []string{"low", "medium", "high"} {
		if s == c {
			return c
		}
	}
	return ""
}

// untraded lists, as "[3][7]", the articles published after a price was
// struck: news the market has not traded on yet. The look is read before the
// US open, so for a US share that is everything since the last close --
// results after it, the night's news, a release before the open -- and a
// share that has not moved on it has not shrugged it off, only not had the
// chance. An article with no time, or a price with none, says nothing.
func untraded(articles []model.Article, pricedAt time.Time, cited []model.Article) string {
	if pricedAt.IsZero() {
		return ""
	}
	var out strings.Builder
	for _, a := range articles {
		if n := number(a, cited); n > 0 && a.Published.After(pricedAt) {
			fmt.Fprintf(&out, "[%d]", n)
		}
	}
	return out.String()
}

func number(a model.Article, cited []model.Article) int {
	for i, c := range cited {
		if c.ID == a.ID {
			return i + 1
		}
	}
	return 0
}
