package app

import (
	"context"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// knownTerms is every term linked without a writer listing it: the
// glossary's, with their checked pages, then the learned ones.
func (a *App) knownTerms() []model.Term {
	out := append([]model.Term{}, a.Terms...)
	if a.Learned == nil {
		return out
	}
	// A learned term folded into the glossary since is the glossary's.
	have := map[string]bool{}
	for _, t := range a.Terms {
		for _, w := range t.Words {
			have[strings.ToLower(w)] = true
		}
	}
	for _, t := range a.Learned.Active() {
		if !have[strings.ToLower(t.Words[0])] {
			out = append(out, t)
		}
	}
	return out
}

// termCheckTimeout bounds the check of the terms due to be learned.
const termCheckTimeout = 5 * time.Minute

// learnTerms notes the terms a report listed, and checks those now seen often
// enough to learn. It runs on its own, after the report has gone: the check is
// a model call, and the reader should not wait for it. A failure costs only
// the learning, and the terms stay due for the next report's check.
func (a *App) learnTerms(listed []model.ListedTerm, where string) {
	if a.Learned == nil || len(listed) == 0 {
		return
	}
	due, err := a.Learned.Record(listed, where, a.now())
	if err != nil {
		a.Log.Warn("terms not recorded", "where", where, "err", err)
		return
	}
	if len(due) == 0 || a.TermCheck == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), termCheckTimeout)
		defer cancel()
		passed, err := a.TermCheck.Check(ctx, due)
		if err != nil {
			a.Log.Warn("terms not checked", "due", len(due), "err", err)
			return
		}
		if err := a.Learned.Settle(due, passed, a.now()); err != nil {
			a.Log.Warn("terms not settled", "err", err)
			return
		}
		a.Log.Info("terms checked", "due", len(due), "learned", len(passed))
	}()
}
