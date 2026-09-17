package fundamentals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
)

// The fixed table answers the questions I thought to ask in advance. Reading
// NVIDIA's accounts, the analysis itself named what it could not judge --
// receivables, share-based pay, buybacks, the prior year of inventory -- and
// every one of those is a tag away in the same API. So the reader gets tools
// rather than a fixed extract: it starts from the table, then looks up whatever
// this particular company's numbers turn out to require. A chip maker and a
// bank raise different questions, and no fixed list of rows is right for both.
const (
	// maxToolCalls bounds the lookups. A wandering agent is a slow and
	// expensive one, and in practice the useful follow-ups are a handful.
	maxToolCalls = 16

	// maxComputations is a separate and much larger budget. A lookup is a
	// request to the SEC and a page of figures into the context; a calculation
	// is a few microseconds and one line, so the reason to be sparing with
	// lookups does not apply to it. An analysis that quotes twenty ratios
	// should be allowed to calculate twenty times.
	maxComputations = 50

	// maxTurns bounds the conversation itself. Each turn is a fresh request
	// carrying everything read so far, so this is the figure that governs cost.
	maxTurns = 30

	// maxRowsPerTag keeps a tool result small. Twenty years of quarterly data
	// would crowd out the reasoning it is meant to support.
	maxRowsPerTag = 14

	maxTagMatches = 25

	agentMaxTokens = 12000
)

// Agent writes the analysis with the filings open in front of it: it can look
// up any concept the company reports, not only the ones the table extracts.
type Agent struct {
	Client   anthropic.Client
	Model    string
	Reader   *Client
	MaxCalls int

	// Log receives one line per lookup, so what the agent chose to read is
	// visible afterwards rather than buried in a transcript.
	Log func(format string, args ...any)

	Now func() time.Time
}

// NewAgent builds an agent against the Anthropic API.
func NewAgent(apiKey, modelID string, reader *Client) *Agent {
	return &Agent{
		Client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		Model:  modelID,
		Reader: reader,
	}
}

var agentSystemPrompt = systemPrompt + `

You have tools that read the same SEC filings the table came from. Use them: the table is a starting point, not the limit of what you can know.

- find_concepts searches what this company actually reports, by keyword. Use it before guessing at a tag name.
- read_concept returns every value the company has filed for one tag.
- compute calculates exactly, from figures you have already read.

Look up what the company in front of you requires. The figures you most need are often the ones the table does not carry: amounts owed by customers against revenue, share-based pay against reported profit, buybacks and dividends against free cash flow, the prior year of a balance-sheet line so that a single figure becomes a trend.

Calculate rather than estimate. Every ratio, margin, growth rate, multiple, per-share figure and days-outstanding number you put in the analysis must come back from compute, not from working it out as you write. An arithmetic slip reads exactly like a correct figure and the reader has no way to catch it. Lookups are limited and calculations are not, so when you are unsure whether a figure is worth checking, check it.

Read what you need, then stop and write. Everything you cite must come from the table, from a tool result, or from a compute result, exactly as returned. The rules above still hold: no figure from memory, no valuation beyond the price you are given, no advice.` + "\n\n" + Method

// Analyze reads the accounts, letting the model pull what it needs, and writes
// them up.
func (a *Agent) Analyze(ctx context.Context, snap Snapshot) (Analysis, error) {
	tools := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "find_concepts",
			Description: anthropic.String("Search the concepts this company reports to the SEC. Give a keyword or two, such as receivable, share repurchase or segment revenue. Returns tag names to pass to read_concept."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Words to match against tag names and labels.",
					},
				},
				Required: []string{"query"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "read_concept",
			Description: anthropic.String("Read every value this company has reported for one concept, newest first, with the period, the filing it came from and the unit."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"taxonomy": map[string]any{
						"type":        "string",
						"description": "us-gaap, ifrs-full or dei, as given by find_concepts.",
					},
					"tag": map[string]any{
						"type":        "string",
						"description": "The exact tag name, for example AccountsReceivableNetCurrent.",
					},
				},
				Required: []string{"taxonomy", "tag"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "compute",
			Description: anthropic.String("Calculate exactly, from figures you have read. Use it for every ratio, margin, growth rate, multiple and per-share figure you quote, instead of working it out yourself. Understands + - * / ^ and brackets, the scale suffixes k m bn tn, thousands commas, a trailing % , and the functions abs, avg, cagr, growth, ln, max, min, pow, round, sqrt and sum. Examples: \"15.4bn / 11.8bn\", \"growth(60922, 130497)\", \"cagr(16675, 130497, 4)\", \"11826 / 130497 * 365\"."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"expression": map[string]any{
						"type":        "string",
						"description": "The arithmetic to perform, over numbers you have already read. There are no variables: write the figures themselves.",
					},
					"of": map[string]any{
						"type":        "string",
						"description": "What the figure is, in a few words, such as \"cash conversion, year to 28 Aug 2025\". It is echoed back with the answer so a page of results stays readable.",
					},
				},
				Required: []string{"expression"},
			},
		}},
	}

	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(a.prompt(snap))),
	}

	var usage model.Usage
	var text strings.Builder
	var spent budget
	nudged := false

	for turn := 0; turn < maxTurns; turn++ {
		msg, err := a.Client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(a.Model),
			MaxTokens: agentMaxTokens,
			System:    []anthropic.TextBlockParam{{Text: agentSystemPrompt}},
			Tools:     tools,
			Messages:  messages,
		})
		if err != nil {
			return Analysis{}, fmt.Errorf("analyse %s: %w", snap.Ticker, err)
		}
		usage.InputTokens += msg.Usage.InputTokens
		usage.OutputTokens += msg.Usage.OutputTokens

		if msg.StopReason != anthropic.StopReasonToolUse {
			// Only the final answer is kept. The running commentary between
			// lookups is the model thinking aloud, not the analysis.
			text.Reset()
			for _, block := range msg.Content {
				if t, ok := block.AsAny().(anthropic.TextBlock); ok {
					text.WriteString(t.Text)
				}
			}
			break
		}

		messages = append(messages, msg.ToParam())
		results := a.run(ctx, snap, msg, &spent)

		// When the lookups are spent, say so plainly rather than cutting the
		// model off mid-investigation, so the last turn is a written analysis
		// and not a half-finished thought. It goes in the same message as the
		// tool results, after them: two user messages in a row is not a shape
		// the API accepts.
		if spent.lookups >= a.maxCalls() && !nudged {
			nudged = true
			results = append(results, anthropic.NewTextBlock(
				"That is all the lookups. You can still calculate as much as you need. Write the analysis now, from the table and what you have read."))
		}
		messages = append(messages, anthropic.NewUserMessage(results...))
	}

	usage.EstimatedUSD = report.EstimateCost(a.Model, usage)
	out := strings.TrimSpace(text.String())
	if out == "" {
		return Analysis{}, fmt.Errorf("analyse %s: the model made lookups but never wrote the analysis", snap.Ticker)
	}
	return Analysis{Snapshot: snap, Text: out, Usage: usage}, nil
}

// budget is what a conversation has spent so far.
//
// The two are counted apart because they cost different things. A lookup is a
// request to the SEC and a page of figures added to the context; a calculation
// is a few microseconds and one line back. Rationing them together would have
// meant discouraging the arithmetic that makes the analysis correct in order to
// save something it does not spend.
type budget struct {
	lookups int
	sums    int
}

// lookup takes one from the lookup budget, or says it is gone.
//
// The limit is enforced here rather than only asked for in the prompt: a model
// told it has run out will usually stop, and "usually" is not a bound on what
// a run costs.
func (b *budget) lookup(max int) error {
	if b.lookups >= max {
		return fmt.Errorf("no lookups left: that was all %d of them. Write the analysis from the table and what you have already read", max)
	}
	b.lookups++
	return nil
}

// run executes the tool calls in one reply and returns their results.
func (a *Agent) run(ctx context.Context, snap Snapshot, msg *anthropic.Message, spent *budget) []anthropic.ContentBlockParamUnion {
	var results []anthropic.ContentBlockParamUnion

	for _, block := range msg.Content {
		use, ok := block.AsAny().(anthropic.ToolUseBlock)
		if !ok {
			continue
		}

		out, err := a.call(ctx, snap, use, spent)
		if err != nil {
			// A failed lookup goes back to the model rather than aborting the
			// analysis: a tag the company does not report is an answer in
			// itself, and the reader can try another.
			results = append(results, anthropic.NewToolResultBlock(use.ID, err.Error(), true))
			continue
		}
		results = append(results, anthropic.NewToolResultBlock(use.ID, out, false))
	}
	return results
}

func (a *Agent) call(ctx context.Context, snap Snapshot, use anthropic.ToolUseBlock, spent *budget) (string, error) {
	switch use.Name {
	case "compute":
		var in struct {
			Expression string `json:"expression"`
			Of         string `json:"of"`
		}
		if err := json.Unmarshal(use.Input, &in); err != nil {
			return "", fmt.Errorf("could not read the calculation: %v", err)
		}
		if spent.sums >= maxComputations {
			return "", fmt.Errorf("that is %d calculations, which is all of them. Write the analysis from what you have", maxComputations)
		}
		spent.sums++

		value, err := Compute(in.Expression)
		if err != nil {
			// The expression comes back with the complaint. The model wrote it
			// several hundred tokens ago and correcting it blind is guesswork.
			return "", fmt.Errorf("%q: %v", in.Expression, err)
		}
		a.log("compute %s = %s", in.Expression, Number(value))

		if label := strings.TrimSpace(in.Of); label != "" {
			return fmt.Sprintf("%s: %s = %s", label, in.Expression, Number(value)), nil
		}
		return fmt.Sprintf("%s = %s", in.Expression, Number(value)), nil

	case "find_concepts":
		var in struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(use.Input, &in); err != nil {
			return "", fmt.Errorf("could not read the query: %v", err)
		}
		if err := spent.lookup(a.maxCalls()); err != nil {
			return "", err
		}
		a.log("find_concepts %q", in.Query)

		tags, err := a.Reader.Tags(ctx, snap.CIK)
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

	case "read_concept":
		var in struct {
			Taxonomy string `json:"taxonomy"`
			Tag      string `json:"tag"`
		}
		if err := json.Unmarshal(use.Input, &in); err != nil {
			return "", fmt.Errorf("could not read the request: %v", err)
		}
		if err := spent.lookup(a.maxCalls()); err != nil {
			return "", err
		}
		a.log("read_concept %s/%s", in.Taxonomy, in.Tag)

		obs, err := a.Reader.Concept(ctx, snap.CIK, in.Taxonomy, in.Tag)
		if err != nil {
			return "", fmt.Errorf("%s/%s is not reported by this company", in.Taxonomy, in.Tag)
		}
		return renderObservations(in.Taxonomy, in.Tag, obs), nil
	}
	return "", fmt.Errorf("no such tool: %s", use.Name)
}

// renderObservations formats a concept's history: the annual figures first,
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
					o.Start.Format(time.DateOnly), o.End.Format(time.DateOnly), o.Days(), o.Form,
					commas(o.Value), o.Unit)
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

func (a *Agent) prompt(snap Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", a.now().Format("2 January 2006"))
	b.WriteString(snap.Table())
	if age := snap.Age(a.now()); age > 0 {
		fmt.Fprintf(&b, "\nThe latest balance sheet is %d days old.\n", int(age.Hours()/24))
	}
	fmt.Fprintf(&b, "\nYou may make up to %d lookups before writing. Calculations are counted separately and are effectively free: compute every figure you quote rather than working it out in your head.\n", a.maxCalls())
	b.WriteString(RelatedFor(snap))
	return b.String()
}

func (a *Agent) maxCalls() int {
	if a.MaxCalls > 0 {
		return a.MaxCalls
	}
	return maxToolCalls
}

func (a *Agent) log(format string, args ...any) {
	if a.Log != nil {
		a.Log(format, args...)
	}
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}
