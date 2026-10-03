package fundamentals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
)

// systemPrompt governs the written analysis.
//
// Two constraints do the heavy lifting. The first is that every figure must
// come from the table: the model has no database, and a plausible revenue
// number it recalls from training would be indistinguishable from a real one.
// The second is that the view on the stock is kept to one section at the end,
// THE VERDICT, after the accounts have been read through: argued from the
// first line, every section would become a case for it. The verdict is the
// same call the closer look makes -- against the S&P 500 over twelve months --
// so /scorecard can keep score of both. Since 2026-10-02 the verdict is a
// short line for the record: the owner wants the analysis read for the case
// for and against, where the company is heading, and whether its figures
// hold up.
//
// The method follows it: how to read the accounts in order and which traps to
// look for by sector, and CAN SLIM's questions with where each misleads. It
// was written as a playbook for the agent that could look filings up as it
// wrote; that agent spoke to the API directly and went when the API did, and
// the playbook is as much use to the analysis that remains.
var systemPrompt = config.Prompt("analysis.system") + "\n\n" + config.Method

// toolsPrompt is the same with what the tools are for between the two, for
// an analysis that has them (tools.go).
func toolsPrompt() (string, error) {
	tools, err := config.RenderPrompt("analysis.tools", struct{ Lookups int }{ToolLookups})
	if err != nil {
		return "", err
	}
	return config.Prompt("analysis.system") + "\n\n" + strings.TrimSpace(tools) + "\n\n" + config.Method, nil
}

// Analysis is a written reading of one company's accounts.
type Analysis struct {
	Snapshot Snapshot
	Text     string
	Usage    model.Usage
}

// Analyzer writes the analysis. It takes the same Completer the daily brief
// uses: in the service, the relay, answered by the analysis stage's model.
type Analyzer struct {
	Completer report.Completer
	Now       func() time.Time

	// Tools, when set, offers the call the account tools for the company
	// with this CIK, by returning a context that carries them. Nil writes
	// from the facts and the web alone.
	Tools func(ctx context.Context, cik int) context.Context
}

// Analyze reads the accounts and writes them up.
func (a *Analyzer) Analyze(ctx context.Context, snap Snapshot) (Analysis, error) {
	system := systemPrompt
	if a.Tools != nil && snap.CIK > 0 {
		withTools, err := toolsPrompt()
		if err != nil {
			return Analysis{}, err
		}
		system, ctx = withTools, a.Tools(ctx, snap.CIK)
	}
	completion, err := a.Completer.Complete(ctx, system, a.prompt(snap))
	if err != nil {
		return Analysis{}, fmt.Errorf("analyze %s: %w", snap.Ticker, err)
	}

	text := strings.TrimSpace(completion.Text)
	if text == "" {
		return Analysis{}, fmt.Errorf("analyze %s: the model returned nothing", snap.Ticker)
	}
	return Analysis{Snapshot: snap, Text: text, Usage: completion.Usage}, nil
}

func (a *Analyzer) prompt(snap Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", a.now().Format("2 January 2006"))
	b.WriteString(snap.Table())

	if age := snap.Age(a.now()); age > 0 {
		fmt.Fprintf(&b, "\nThe latest balance sheet is %d days old.\n", int(age.Hours()/24))
	}
	b.WriteString(RelatedFor(snap))
	return b.String()
}

func (a *Analyzer) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}
