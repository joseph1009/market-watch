package fundamentals

import (
	"context"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/report"
)

type toolsKey struct{}

// asked keeps what an analysis call was given.
type asked struct {
	system string
	tools  any
}

func (a *asked) Complete(ctx context.Context, system, _ string) (report.Completion, error) {
	a.system, a.tools = system, ctx.Value(toolsKey{})
	return report.Completion{Text: "THE BUSINESS\nIt makes chips."}, nil
}

// An analysis with tools is told what they are for and given them for its
// call; one without is told nothing of them.
func TestAnAnalysisIsGivenItsToolsWhereItHasThem(t *testing.T) {
	model := &asked{}
	a := &Analyzer{Completer: model, Tools: func(ctx context.Context, cik int) context.Context {
		return context.WithValue(ctx, toolsKey{}, cik)
	}}
	if _, err := a.Analyze(context.Background(), Snapshot{Ticker: "MU", CIK: 723125}); err != nil {
		t.Fatal(err)
	}
	if model.tools != 723125 || !strings.Contains(model.system, "find_concepts searches what this company actually reports") ||
		!strings.Contains(model.system, "You have 16 lookups") {
		t.Errorf("tools = %v; the system prompt has the tools' part: %v", model.tools, strings.Contains(model.system, "find_concepts"))
	}

	model = &asked{}
	a = &Analyzer{Completer: model}
	if _, err := a.Analyze(context.Background(), Snapshot{Ticker: "MU", CIK: 723125}); err != nil {
		t.Fatal(err)
	}
	if model.tools != nil || strings.Contains(model.system, "find_concepts") {
		t.Error("an analysis without tools was told of them")
	}
}
