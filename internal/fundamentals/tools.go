package fundamentals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/mcp"
)

// The analysis's tools. The fixed table answers the questions thought of in
// advance, and an analysis of NVIDIA named what it could not judge without
// more: receivables, share-based pay, buybacks, the prior year of inventory.
// Every one of those is a tag away in the same SEC API. Until 2026-09-22 an
// agent of our own could look them up; it called the API directly and went
// with it. Since 2026-10-03 the same three tools are served to Claude Code by
// the service itself (internal/mcp), for the analysis call alone: find what
// the company reports, read any of it, and calculate exactly.

const (
	// ToolLookups bounds the reads from the SEC an analysis may make. A
	// wandering analysis is a slow one, and in practice the useful follow-ups
	// are a handful.
	ToolLookups = 16

	// toolSums is a separate and much larger budget. A lookup is a request to
	// the SEC and a page of figures to read; a calculation is a few
	// microseconds and one line, so the reason to be sparing with lookups does
	// not apply to it.
	toolSums = 50

	// maxRowsPerTag keeps a lookup's answer small. Twenty years of quarters
	// would crowd out the reasoning it is meant to support.
	maxRowsPerTag = 14

	maxTagMatches = 25
)

// AccountTools are the tools for one company's accounts: find_concepts,
// read_concept and compute. Each call is written to log, so what the
// analysis chose to read can be seen afterwards.
func AccountTools(reader *Client, cik int, log io.Writer) []mcp.Tool {
	var (
		mu      sync.Mutex
		lookups int
		sums    int
	)
	note := func(format string, args ...any) {
		if log != nil {
			mu.Lock()
			fmt.Fprintf(log, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
			mu.Unlock()
		}
	}
	// lookup takes one from the budget, or says it is gone. The limit is
	// enforced here, not only asked for in the prompt: a model told it has
	// run out will usually stop, and "usually" is not a bound.
	lookup := func() error {
		mu.Lock()
		defer mu.Unlock()
		if lookups >= ToolLookups {
			return fmt.Errorf("no lookups left: that was all %d of them. Write the analysis from the facts and what you have already read", ToolLookups)
		}
		lookups++
		return nil
	}

	return []mcp.Tool{
		{
			Name:        "find_concepts",
			Description: "Search the concepts this company reports to the SEC. Give a keyword or two, such as receivable, share repurchase or segment revenue. Returns tag names to pass to read_concept.",
			Schema: schema(map[string]any{
				"query": prop("Words to match against tag names and labels."),
			}, "query"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Query string `json:"query"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("could not read the query: %v", err)
				}
				if err := lookup(); err != nil {
					return "", err
				}
				note("find_concepts %q", in.Query)
				tags, err := reader.Tags(ctx, cik)
				if err != nil {
					return "", fmt.Errorf("could not list this company's concepts: %v", err)
				}
				matches := Search(tags, in.Query, maxTagMatches)
				if len(matches) == 0 {
					return fmt.Sprintf("Nothing this company reports matches %q. It may not disclose it, or it may be filed under another name.", in.Query), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "Concepts matching %q:\n", in.Query)
				for _, m := range matches {
					fmt.Fprintf(&b, "- %s/%s (%s) [%s]\n", m.Taxonomy, m.Tag, m.Label, strings.Join(m.Units, ", "))
				}
				return b.String(), nil
			},
		},
		{
			Name:        "read_concept",
			Description: "Read the values this company has reported for one concept, newest first: the annual periods, the quarters and the balance dates, each with the filing it came from and the unit.",
			Schema: schema(map[string]any{
				"taxonomy": prop("us-gaap, ifrs-full or dei, as find_concepts gives it."),
				"tag":      prop("The exact tag name, for example AccountsReceivableNetCurrent."),
			}, "taxonomy", "tag"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Taxonomy string `json:"taxonomy"`
					Tag      string `json:"tag"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("could not read the request: %v", err)
				}
				if err := lookup(); err != nil {
					return "", err
				}
				note("read_concept %s/%s", in.Taxonomy, in.Tag)
				obs, err := reader.Concept(ctx, cik, in.Taxonomy, in.Tag)
				if err != nil {
					return "", fmt.Errorf("%s/%s is not reported by this company", in.Taxonomy, in.Tag)
				}
				return renderObservations(in.Taxonomy, in.Tag, obs), nil
			},
		},
		{
			Name: "compute",
			Description: "Calculate exactly, from figures you have read. Use it for every ratio, margin, growth rate, multiple and per-share figure you quote, instead of working it out yourself. " +
				"Understands + - * / ^ and brackets, the scale suffixes k m bn tn, thousands commas, a trailing %, and the functions abs, avg, cagr, growth, ln, max, min, pow, round, sqrt and sum. " +
				`Examples: "15.4bn / 11.8bn", "growth(60922, 130497)", "cagr(16675, 130497, 4)", "11826 / 130497 * 365".`,
			Schema: schema(map[string]any{
				"expression": prop("The arithmetic to perform, over numbers you have already read. There are no variables: write the figures themselves."),
				"of":         prop(`What the figure is, in a few words, such as "cash conversion, year to 28 Aug 2025". It is echoed back with the answer.`),
			}, "expression"),
			Call: func(_ context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Expression string `json:"expression"`
					Of         string `json:"of"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("could not read the calculation: %v", err)
				}
				mu.Lock()
				over := sums >= toolSums
				if !over {
					sums++
				}
				mu.Unlock()
				if over {
					return "", fmt.Errorf("that is %d calculations, which is all of them. Write the analysis from what you have", toolSums)
				}
				value, err := Compute(in.Expression)
				if err != nil {
					// The expression comes back with the complaint, since
					// correcting it blind is guesswork.
					return "", fmt.Errorf("%q: %v", in.Expression, err)
				}
				note("compute %s = %s", in.Expression, Number(value))
				if label := strings.TrimSpace(in.Of); label != "" {
					return fmt.Sprintf("%s: %s = %s", label, in.Expression, Number(value)), nil
				}
				return fmt.Sprintf("%s = %s", in.Expression, Number(value)), nil
			},
		},
	}
}

func schema(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// renderObservations writes a concept's history: the annual figures first,
// since a trend is usually what was wanted, then the shorter periods.
func renderObservations(taxonomy, tag string, obs []Observation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s, as filed:\n", taxonomy, tag)
	write := func(heading string, rows []Observation) {
		if len(rows) == 0 {
			return
		}
		if len(rows) > maxRowsPerTag {
			rows = rows[:maxRowsPerTag]
		}
		fmt.Fprintf(&b, "%s\n", heading)
		for _, o := range rows {
			if o.Duration() {
				fmt.Fprintf(&b, "  %s to %s (%d days, %s): %s %s\n",
					o.Start.Format(time.DateOnly), o.End.Format(time.DateOnly), o.Days(), o.Form, commas(o.Value), o.Unit)
				continue
			}
			fmt.Fprintf(&b, "  at %s (%s): %s %s\n", o.End.Format(time.DateOnly), o.Form, commas(o.Value), o.Unit)
		}
	}
	write("Annual periods:", Annual(obs))
	write("Quarterly periods:", Quarterly(obs))
	write("Balance dates:", Instant(obs))
	b.WriteString("Values are exactly as filed, in the units shown, and are not scaled to millions.\n")
	return b.String()
}
