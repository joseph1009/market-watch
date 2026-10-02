package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// /analyse and /industry run in the background, side by side. Until
// 2026-10-03 every message was handled in turn, so a second /analyse waited
// minutes behind the first, and so did /usage and /help. The owner asked for
// them to run at once.
//
// Each job is a goroutine with a context of its own: its own relay run, its
// own cache folder, and its own Claude Code processes. Every model call is a
// new headless process that keeps no session and exits once it has answered,
// so nothing one job asks can reach another, and nothing is left to clear
// when a job ends.
//
// The owner set the rules for how many run (2026-10-03):
//   - Three at a time. Each is a Claude Code process, and the server has 1 GB;
//     the brief has always run three at once without trouble.
//   - One at a time once any plan window is 85% used, with a warning.
//   - None while a brief is being written. A brief waits for the jobs already
//     running, then holds the rest back until it is done.
//
// Jobs start in the order they were asked for. When a chat's jobs are all
// done, it is told how much of the plan is used.

const (
	// throttleAt is the share of any plan window that slows the jobs down.
	throttleAt = 0.85

	// throttledJobs is how many jobs run at once past throttleAt.
	throttledJobs = 1

	// usualJobs is how many run at once below it.
	usualJobs = 3
)

// jobs is the background work: how much runs, who is waiting, and whether a
// brief holds everything back.
type jobs struct {
	mu        sync.Mutex
	running   int
	line      []uint64 // the jobs waiting to start, first asked first
	next      uint64   // the last ticket given out
	brief     bool     // a brief is running, or waiting for the jobs to end
	changed   chan struct{}
	chats     map[int64]chatJobs // each chat's jobs not yet done
	throttled bool               // whether the last admission was past throttleAt
	wg        sync.WaitGroup
}

// chatJobs is one chat's jobs: how many are not done, and since when it has
// had any, which says whether the plan was read during them.
type chatJobs struct {
	open  int
	since time.Time
}

// background runs a command's work as a job, and reports its failure into
// the chat as HandleMessage would. Its place in line is taken here, in the
// order messages arrive, not when its goroutine first runs.
func (a *App) background(ctx context.Context, msg telegram.Message, command string, work func(context.Context) error) {
	chat := msg.Chat.ID
	ticket := a.work.join(chat)
	a.work.wg.Add(1)
	go func() {
		defer a.work.wg.Done()
		defer a.jobDone(ctx, chat)
		if err := a.admit(ctx, chat, ticket); err != nil {
			return // shutting down
		}
		defer a.work.leave()
		a.reportCommand(ctx, msg, command, work(ctx))
	}()
}

// detach runs a command's work in the background without a place in line,
// for /now: the brief it writes holds the jobs back itself.
func (a *App) detach(ctx context.Context, msg telegram.Message, command string, work func(context.Context) error) {
	a.work.wg.Add(1)
	go func() {
		defer a.work.wg.Done()
		a.reportCommand(ctx, msg, command, work(ctx))
	}()
}

// admit waits for the job's turn. It reads the plan first, again if the last
// reading is old, so the limit follows the plan as it stands.
func (a *App) admit(ctx context.Context, chat int64, ticket uint64) error {
	var limits *relay.Limits
	if a.plan != nil {
		limits, _, _ = a.planNow(ctx) // a failed reading leaves the last one
	}
	used, window := planPeak(limits)
	throttled := used >= throttleAt

	a.work.mu.Lock()
	entering := throttled && !a.work.throttled
	a.work.throttled = throttled
	a.work.mu.Unlock()
	if entering {
		a.Log.Warn("plan nearly used; running one job at a time", "window", window, "used", used)
		_ = a.Bot.SendMessage(ctx, chat, fmt.Sprintf(
			"⚠️ <b>The Claude plan is %d%% used</b> (%s). From now on I run one request at a time, in the order they came.",
			percent(used), escape(strings.ToLower(window))))
	}

	started, held, changed := a.work.try(ticket, a.jobLimit())
	if started {
		return nil
	}
	if held != "" { // not a moment's wait for the jobs ahead to start
		why := "⏳ Three requests are already running. This one starts when one of them finishes."
		switch {
		case held == heldByBrief:
			why = "⏳ A brief is being written. This request starts when the brief is done."
		case a.jobLimit() == throttledJobs:
			why = "⏳ The Claude plan is over 85% used, so I run one request at a time. This one starts when the ones before it finish."
		}
		_ = a.Bot.SendMessage(ctx, chat, why)
	}

	for {
		select {
		case <-changed:
		case <-ctx.Done():
			a.work.drop(ticket)
			return ctx.Err()
		}
		if started, _, changed = a.work.try(ticket, a.jobLimit()); started {
			return nil
		}
	}
}

// jobLimit is how many jobs may run at once, by the plan's latest reading.
func (a *App) jobLimit() int {
	if a.plan == nil {
		return usualJobs
	}
	limits, _ := a.plan.latest()
	if used, _ := planPeak(limits); used >= throttleAt {
		return throttledJobs
	}
	return usualJobs
}

// jobDone closes a job, and once the chat has none left, tells it how much
// of the plan is used. A chat whose jobs never reached a model, such as a
// ticker that was not one, is not told: nothing was spent.
func (a *App) jobDone(ctx context.Context, chat int64) {
	since, last := a.work.closed(chat)
	if !last || a.plan == nil {
		return
	}
	limits, at := a.plan.latest()
	if limits == nil || at.Before(since) {
		return
	}
	if text := planReminder(limits); text != "" {
		_ = a.Bot.SendMessage(ctx, chat, text)
	}
}

// planReminder is the plan's standing after a chat's jobs:
//
//	📊 Claude plan used
//	Next five hours: 41%
//	This week: 63%
func planReminder(l *relay.Limits) string {
	var lines []string
	for _, w := range windowNames {
		if win, ok := l.Windows[w.key]; ok {
			lines = append(lines, fmt.Sprintf("%s: <b>%d%%</b>", w.name, percent(win.Utilization)))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	text := "📊 <b>Claude plan used</b>\n" + strings.Join(lines, "\n")
	if used, _ := planPeak(l); used >= throttleAt {
		text += "\n⚠️ Over 85%, so I run one request at a time."
	}
	return text + "\n<i>/usage has when each one resets.</i>"
}

// planPeak is the most used of the plan's windows, and its name. A plan that
// refuses calls counts as fully used.
func planPeak(l *relay.Limits) (float64, string) {
	if l == nil {
		return 0, ""
	}
	var peak float64
	var name string
	for key, w := range l.Windows {
		if w.Utilization > peak {
			peak, name = w.Utilization, windowName(key)
		}
	}
	if l.Status == "rejected" {
		return 1, "limit reached"
	}
	return peak, name
}

// windowName is a window as /usage names it.
func windowName(key string) string {
	for _, w := range windowNames {
		if w.key == key {
			return w.name
		}
	}
	return strings.ReplaceAll(key, "_", " ")
}

func percent(f float64) int { return int(f*100 + 0.5) }

// join counts a chat's new job and puts it at the back of the line.
func (j *jobs) join(chat int64) uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.chats == nil {
		j.chats = map[int64]chatJobs{}
	}
	c := j.chats[chat]
	if c.open == 0 {
		c.since = time.Now()
	}
	c.open++
	j.chats[chat] = c
	j.next++
	j.line = append(j.line, j.next)
	return j.next
}

// closed counts a chat's job done, and says since when the chat had jobs and
// whether this was its last.
func (j *jobs) closed(chat int64) (since time.Time, last bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	c := j.chats[chat]
	c.open--
	if c.open <= 0 {
		delete(j.chats, chat)
		return c.since, true
	}
	j.chats[chat] = c
	return c.since, false
}

// Why a job waits, when it is more than a moment.
const (
	heldByBrief = "brief" // a brief holds the jobs back
	heldFull    = "full"  // the jobs running and those ahead fill every place
)

// try starts the job holding ticket if it is first in line, no brief holds
// the jobs back, and fewer than limit run. Otherwise it says why it waits,
// or "" when it waits only for the jobs ahead of it to start, and returns a
// channel that is closed when that may change.
func (j *jobs) try(ticket uint64, limit int) (started bool, held string, changed <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	place := slices.Index(j.line, ticket)
	switch {
	case j.brief:
		held = heldByBrief
	case place == 0 && j.running < limit:
		j.line = j.line[1:]
		j.running++
		j.wake() // the next in line may start too
		return true, "", nil
	case j.running+place >= limit:
		held = heldFull
	}
	return false, held, j.waiting()
}

// drop takes a job that will not start out of the line.
func (j *jobs) drop(ticket uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if i := slices.Index(j.line, ticket); i >= 0 {
		j.line = slices.Delete(j.line, i, i+1)
		j.wake()
	}
}

// leave ends a job and wakes the jobs waiting for a turn.
func (j *jobs) leave() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.running--
	j.wake()
}

// hold is a brief taking its turn: from now no job starts, and once the jobs
// already running have ended, it returns. Release lets the jobs go again.
func (j *jobs) hold(ctx context.Context) error {
	j.mu.Lock()
	j.brief = true
	for j.running > 0 {
		changed := j.waiting()
		j.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			j.release()
			return ctx.Err()
		}
		j.mu.Lock()
	}
	j.mu.Unlock()
	return nil
}

// release ends a brief's hold.
func (j *jobs) release() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.brief = false
	j.wake()
}

// waiting is the channel the next change closes. The caller holds mu.
func (j *jobs) waiting() <-chan struct{} {
	if j.changed == nil {
		j.changed = make(chan struct{})
	}
	return j.changed
}

// wake tells everyone waiting that something changed. The caller holds mu.
func (j *jobs) wake() {
	if j.changed != nil {
		close(j.changed)
		j.changed = nil
	}
}

// wait blocks until every job has ended.
func (j *jobs) wait() { j.wg.Wait() }
