package terms

import (
	"context"
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// Completer answers one prompt.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

// Checker asks a model whether the terms due for learning are real terms with
// a clear search, before they are linked in every report.
type Checker struct {
	Completer Completer
}

// Check returns the terms that pass, each with the context the check gave
// it. A reply that names a term it was not asked about is ignored.
func (c Checker) Check(ctx context.Context, due []Learned) ([]model.ListedTerm, error) {
	if len(due) == 0 {
		return nil, nil
	}
	var b strings.Builder
	asked := map[string]bool{}
	for _, t := range due {
		asked[strings.ToLower(t.Term)] = true
		fmt.Fprintf(&b, "%s | %s\n", t.Term, t.Context)
	}
	text, _, err := c.Completer.Complete(ctx, config.Prompt("terms.check"), b.String())
	if err != nil {
		return nil, err
	}
	var passed []model.ListedTerm
	for _, l := range model.ParseTerms(strings.Split(text, "\n")) {
		if asked[strings.ToLower(l.Term)] {
			passed = append(passed, l)
		}
	}
	return passed, nil
}
