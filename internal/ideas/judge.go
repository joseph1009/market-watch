package ideas

import (
	"context"
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prompts"
)

// Judge gives each idea its verdict.
type Judge struct {
	Completer Completer
}

// verdictsPrompt governs the verdicts. Its text lives in internal/prompts.
var verdictsPrompt = prompts.Get("verdicts.system")

// Judge returns the ideas that received a verdict, in the order given. facts
// is each idea's fact sheet, by position: its trading, and its accounts where
// they were read.
//
// An idea the reply skips, or answers with something other than BUY, HOLD or
// SELL, is dropped rather than shown without one: the section is verdicts, and
// a company listed without one would read as an oversight.
func (j *Judge) Judge(ctx context.Context, ideas []model.Idea, facts []string, cited []model.Article) ([]model.Idea, model.Usage, error) {
	if j.Completer == nil || len(ideas) == 0 {
		return nil, model.Usage{}, nil
	}

	text, usage, err := j.Completer.Complete(ctx, verdictsPrompt, judgePrompt(ideas, facts, cited))
	if err != nil {
		return nil, usage, err
	}

	blocks := parseVerdicts(text)
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
		out = append(out, idea)
	}
	return out, usage, nil
}

func judgePrompt(ideas []model.Idea, facts []string, cited []model.Article) string {
	var b strings.Builder
	b.WriteString("The articles, numbered as in today's brief:\n")
	for i, a := range cited {
		fmt.Fprintf(&b, "[%d] %s (%s)\n", i+1, oneLine(a.Title), a.SourceName)
	}

	b.WriteString("\nThe companies:\n")
	for i, idea := range ideas {
		fmt.Fprintf(&b, "\n=== %s\n", idea.Symbol())
		fmt.Fprintf(&b, "%s, registered as %s.\n", idea.Name, idea.Listed)

		kind := "In today's news"
		if idea.Connected {
			kind = "Not in today's news, but connected to it"
		}
		fmt.Fprintf(&b, "%s: %s", kind, idea.Link)
		for _, a := range idea.Articles {
			if n := number(a, cited); n > 0 {
				fmt.Fprintf(&b, " [%d]", n)
			}
		}
		b.WriteString("\n")

		if q := idea.Quote; q != nil {
			// With the unit, always. A Hong Kong listing is quoted in Hong Kong
			// dollars, and a bare 512.40 set beside a US company's 512.40
			// invites a comparison that means nothing.
			fmt.Fprintf(&b, "Today: %s, at %s %.2f.\n", q.Move(), q.Unit(), q.Price)
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

func number(a model.Article, cited []model.Article) int {
	for i, c := range cited {
		if c.ID == a.ID {
			return i + 1
		}
	}
	return 0
}
