package ideas

import (
	"context"
	"fmt"
	"sort"
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
	// DefaultBatch keeps one request to three fact sheets. Each carries what
	// /analyse reads -- five years of accounts, the business, the results
	// release, the news -- at twenty to thirty kilobytes, and five of them in
	// one request is more than the model reads with care.
	DefaultBatch = 3

	// DefaultJudgeConcurrency runs two batches at a time, without asking the
	// plan for four Opus calls at once.
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
		idea.Value = v.value
		idea.Catalyst, idea.Sensitivity = v.catalyst, v.sensitivity
		out = append(out, hold(idea))
	}
	if failed > 0 {
		return out, usage, fmt.Errorf("%d of %d verdict batches failed: %w", failed, len(bounds), first)
	}
	return out, usage, nil
}

// hold applies the rules the model is told but may not bend. A BUY the
// valuation has closed is a HOLD, and says why; a verdict without accounts is
// low confidence at most, since the numbers that would justify more were
// never read.
func hold(idea model.Idea) model.Idea {
	if idea.Verdict == model.Buy && idea.BuyClosed != "" {
		idea.Verdict = model.Hold
		idea.Overruled = "BUY turned to HOLD: " + idea.BuyClosed
	}
	if !idea.Accounts && idea.Confidence != "" && idea.Confidence != "low" {
		idea.Confidence = "low"
	}
	return idea
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
	fmt.Fprintf(&b, "Today is %s.\n", today.Format("Monday 2 January 2006"))

	// Only the articles these companies hang on, by the brief's numbers: the
	// whole day's list is five hundred headlines, most of them about
	// something else.
	var listed []int
	seen := map[int]bool{}
	for _, idea := range ideas {
		for _, a := range idea.Articles {
			if n := number(a, cited); n > 0 && !seen[n] {
				seen[n] = true
				listed = append(listed, n)
			}
		}
	}
	if len(listed) > 0 {
		sort.Ints(listed)
		b.WriteString("\nThe articles these companies are tied to, numbered as in today's brief:\n")
		for _, n := range listed {
			a := cited[n-1]
			fmt.Fprintf(&b, "[%d] %s (%s, %s)\n", n, oneLine(a.Title), a.SourceName, a.Published.Format("2 Jan 15:04 MST"))
			if summary := oneLine(a.Summary); summary != "" {
				fmt.Fprintf(&b, "    %s\n", clip(summary, 400))
			}
		}
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

		switch idea.Kind {
		case model.IdeaTheme:
			fmt.Fprintf(&b, "A theme pick, under %q. The research proposed it as a %s candidate: %s\n", idea.Theme, idea.Lean, idea.Link)
			b.WriteString("Answer VALUE, and leave out CHANGED, MOVE and REACTION.\n")
		default:
			fmt.Fprintf(&b, "A reaction: %s", idea.Link)
			for _, a := range idea.Articles {
				if n := number(a, cited); n > 0 {
					fmt.Fprintf(&b, " [%d]", n)
				}
			}
			b.WriteString("\nAnswer CHANGED, MOVE and REACTION, and leave out VALUE.\n")
		}
		if idea.Before != "" {
			fmt.Fprintf(&b, "It was picked in the last eight weeks: %s. Say what has changed since, if anything.\n", idea.Before)
		}
		if !idea.Accounts {
			b.WriteString("Its accounts could not be read, so your confidence can be low at most.\n")
		}

		if q := idea.Quote; q != nil {
			// With the unit, always. A Singapore listing is quoted in
			// Singapore dollars, and a bare 42.10 set beside a US company's
			// invites a comparison that means nothing.
			fmt.Fprintf(&b, "Last session: %s, at %s %.2f.\n", q.Move(), q.Unit(), q.Price)
			if late := untraded(idea.Articles, q.AsOf, cited); late != "" {
				fmt.Fprintf(&b, "Articles %s came out after that price: the market has not traded on them yet.\n", late)
			}
		}
		if idea.Valuation != "" {
			b.WriteString(strings.TrimSpace(idea.Valuation) + "\n")
		}
		if i < len(facts) && strings.TrimSpace(facts[i]) != "" {
			b.WriteString(strings.TrimSpace(facts[i]))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// clip cuts s to n runes at a word.
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	cut := string(rs[:n])
	if at := strings.LastIndex(cut, " "); at > n/2 {
		cut = cut[:at]
	}
	return cut + "..."
}

type verdict struct {
	verdict, confidence, theCase, numbers, risk string
	changed, moved, reaction, value             string
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
		{"VALUE:", func(v *verdict) *string { return &v.value }},
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
