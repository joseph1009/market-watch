package app

import (
	"context"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
)

const (
	// recheckMax is how many live calls one month's re-check looks at: more
	// than the list has held so far, in about eight requests, two at a time.
	recheckMax = 40

	// recheckBudget bounds the pricing, and each try at the calls.
	recheckBudget = 45 * time.Minute
)

// recheckDue reports whether the monthly re-check should run with this
// brief: on a Friday, the owner's choice (2026-10-15), so it does not add to
// Monday's themes and the next Monday's picks start from the updated list,
// and once a month. It is the first Friday's brief unless that one missed
// it, when the next Friday's catches up. The very first waits for the
// month's first two Fridays, so a deploy late in a month does not run it
// twice in a week.
func (a *App) recheckDue(now time.Time) bool {
	local := now.In(a.Cfg.ScheduleLocation)
	if local.Weekday() != time.Friday {
		return false
	}
	last := a.Prefs().LastRecheck
	if last.IsZero() {
		return local.Day() <= 14
	}
	l := last.In(a.Cfg.ScheduleLocation)
	return l.Year() != local.Year() || l.Month() != local.Month()
}

// monthlyRecheck looks again at every live call a month old or more, after
// the scheduled brief and its closer look, and tells the owner what it
// changed and closed. Calls that get no answer -- the plan at its limit, a
// request that failed or timed out, a reply that could not be read -- are
// tried again later the same Friday (recheckRetries), and the owner hears
// once, when the last try is done. A Friday on which no call got an answer
// is left to the next Friday.
func (a *App) monthlyRecheck(ctx context.Context) {
	if a.Recheck == nil || a.Scorecard == nil || !a.recheckDue(a.now()) {
		return
	}
	chat := a.Prefs().ChatID
	if chat == 0 {
		return
	}
	now := a.now()
	priceCtx, cancel := context.WithTimeout(ctx, recheckBudget)
	paths, bench, rates := a.priceCalls(priceCtx)
	cancel()
	run := &recheckRun{at: now, chat: chat, rates: rates, started: time.Now()}
	for _, r := range a.Scorecard.Live(now, recheckMax) {
		run.pending = append(run.pending, ideas.NewLive(r, now, paths[r.Chart], bench, rates))
	}
	if len(run.pending) == 0 {
		a.Log.Info("re-check: no live call is a month old yet")
		a.recheckDone(now)
		return
	}

	a.recheckRound(ctx, run)
	if len(run.pending) == 0 {
		a.finishRecheck(run)
		return
	}
	// The retries wait outside the brief, so the jobs held back by it can
	// run in between; each try holds them again, as a brief does.
	a.work.wg.Add(1)
	go func() {
		defer a.work.wg.Done()
		a.retryRecheck(ctx, run)
	}()
}

// recheckRetries are how long after the try before each the calls with no
// answer are tried again: soon, for a request that failed by chance, and
// hours on, for a plan whose window has to reset. All on the same Friday.
var recheckRetries = []time.Duration{10 * time.Minute, time.Hour, 3 * time.Hour}

// recheckRun is one month's re-check as it goes: the calls not yet answered,
// what was done with the rest, and what it has cost.
type recheckRun struct {
	at      time.Time
	chat    int64
	rates   ideas.Rates
	started time.Time
	pending []ideas.Live
	done    []ideas.RecheckDone
	tries   int
	usage   model.Usage
	marked  bool // the month is written down as done
}

// recheckRound asks about the calls still pending, writes each answer onto
// the scorecard, and keeps the calls without one pending. The first answer
// writes the month down as done.
func (a *App) recheckRound(ctx context.Context, run *recheckRun) {
	ctx, cancel := context.WithTimeout(ctx, recheckBudget)
	defer cancel()
	run.tries++
	found, usage, err := a.Recheck.Check(ctx, run.pending, a.Scorecard.RecordNote(), run.at)
	run.usage.InputTokens += usage.InputTokens
	run.usage.OutputTokens += usage.OutputTokens
	if err != nil {
		a.Log.Warn("re-check: some calls were not looked at", "try", run.tries, "error", err)
	}
	var still []ideas.Live
	for _, l := range run.pending {
		key := l.Record.Key()
		rt, ok := found[key]
		if !ok {
			still = append(still, l)
			continue
		}
		var next *ideas.Record
		if rt.Action == ideas.RecheckChange {
			if r, ok := l.Changed(rt, run.at, run.rates); ok && r.Verdict != l.Record.Verdict {
				next = &r
			} else {
				rt.Action = ideas.RecheckKeep
			}
		}
		if _, err := a.Scorecard.ApplyRecheck(key, run.at, rt, next); err != nil {
			a.Log.Warn("re-check: could not save a call", "key", key, "error", err)
			still = append(still, l)
			continue
		}
		d := ideas.RecheckDone{Live: l, Action: rt.Action, Why: rt.Why}
		if next != nil {
			d.Now, d.Below = next.Verdict, next.Trigger
		}
		run.done = append(run.done, d)
	}
	run.pending = still
	if len(run.done) > 0 && !run.marked {
		a.recheckDone(run.at)
		run.marked = true
	}
}

// retryRecheck tries the calls with no answer again, at each of
// recheckRetries, while it is still the same Friday in New York, then tells
// the owner. Each try waits for the jobs running to end and holds the rest
// back, and waits for any brief, as a brief does.
func (a *App) retryRecheck(ctx context.Context, run *recheckRun) {
	day := run.at.In(a.Cfg.ScheduleLocation).YearDay()
	for _, wait := range recheckRetries {
		if len(run.pending) == 0 {
			break
		}
		a.Log.Info("re-check: trying again later", "calls", len(run.pending), "in", wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil || a.now().In(a.Cfg.ScheduleLocation).YearDay() != day {
			break
		}
		a.running.Lock()
		if err := a.work.hold(ctx); err != nil {
			a.running.Unlock()
			break
		}
		a.recheckRound(ctx, run)
		a.work.release()
		a.running.Unlock()
	}
	a.finishRecheck(run)
}

// finishRecheck tells the owner what the month's re-check did. Nothing is
// sent where no call got an answer: the month is not written down as done,
// and the next Friday tries again.
func (a *App) finishRecheck(run *recheckRun) {
	if len(run.done) == 0 {
		a.Log.Warn("re-check: no call got an answer today; the next Friday tries again", "tries", run.tries)
		return
	}
	// Sent even where the service is stopping: the answers are already on
	// the scorecard.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, text := range ideas.RecheckMessages(run.done, len(run.pending), a.Cfg.DisplayLocation) {
		if err := a.Bot.SendMessage(ctx, run.chat, text); err != nil {
			a.Log.Warn("re-check: could not send its message", "error", err)
			break
		}
	}
	a.Log.Info("re-check done", "looked", len(run.done), "missing", len(run.pending), "tries", run.tries,
		"input_tokens", run.usage.InputTokens, "output_tokens", run.usage.OutputTokens,
		"took", time.Since(run.started).Round(time.Second))
}

// recheckDone writes down that this month's re-check has run.
func (a *App) recheckDone(now time.Time) {
	if err := a.UpdatePrefs(func(p *config.Prefs) error {
		p.LastRecheck = now
		return nil
	}); err != nil {
		a.Log.Warn("re-check: could not record that it ran", "error", err)
	}
}
