package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// /usage says how much of the owner's allowances is left: the Claude plan
// every model call draws on, and the month's news-search credits. The owner
// asked for it on 2026-10-01, for every chat that may send commands.

// planFresh is how old a reading of the plan may be and still be shown as it
// is. An older one is read again, with a one-word call to the cheapest model.
const planFresh = 5 * time.Minute

// planWatch keeps the plan's latest standing, which every call to Claude Code
// reports.
type planWatch struct {
	mu     sync.Mutex
	limits *relay.Limits
	at     time.Time
}

func (p *planWatch) note(l relay.Limits) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limits, p.at = &l, time.Now()
}

func (p *planWatch) latest() (*relay.Limits, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.limits, p.at
}

// windowNames are the plan's windows as /usage names them, in its order.
var windowNames = []struct{ key, name string }{
	{"five_hour", "Next five hours"},
	{"seven_day", "This week"},
	{"seven_day_opus", "This week, Opus"},
	{"seven_day_sonnet", "This week, Sonnet"},
}

func (a *App) handleUsage(ctx context.Context, msg telegram.Message) error {
	now := a.now().In(a.Cfg.DisplayLocation)
	parts := []string{fmt.Sprintf("📊 <b>Usage</b> @ %s %s time, %s",
		now.Format("15:04"), placeName(a.Cfg.DisplayLocation), now.Format("Mon 2 Jan"))}
	parts = append(parts, "<b>CLAUDE PLAN</b>\n"+a.planText(ctx, now))
	parts = append(parts, "<b>NEWS SEARCH</b>\n"+a.searchText(ctx))
	return a.Bot.SendMessage(ctx, msg.Chat.ID, strings.Join(parts, "\n\n"))
}

// planNow is the plan's standing: the last call's if it is recent, or a new
// reading. A reading that fails falls back to the last one, however old.
func (a *App) planNow(ctx context.Context) (*relay.Limits, time.Time, error) {
	if a.plan == nil {
		a.plan = &planWatch{}
	}
	limits, at := a.plan.latest()
	if limits != nil && time.Since(at) < planFresh {
		return limits, at, nil
	}
	claude, ok := a.claude()
	if !ok {
		return limits, at, fmt.Errorf("model calls here are answered by hand, not by Claude Code")
	}
	fresh, err := claude.CheckLimits(ctx)
	if err != nil {
		return limits, at, err
	}
	a.plan.note(fresh)
	return &fresh, time.Now(), nil
}

// claude is the Claude Code answerer, when calls are answered by it.
func (a *App) claude() (relay.Claude, bool) {
	if a.Relay == nil {
		return relay.Claude{}, false
	}
	c, ok := a.Relay.Answer.(relay.Claude)
	return c, ok
}

func (a *App) planText(ctx context.Context, now time.Time) string {
	limits, at, err := a.planNow(ctx)
	if limits == nil {
		return "Could not be read: " + escape(err.Error())
	}
	var lines []string
	switch limits.Status {
	case "rejected":
		lines = append(lines, "🛑 <b>Limit reached.</b> Briefs and commands fail until it resets.")
	case "allowed_warning":
		lines = append(lines, "⚠️ <b>Close to a limit.</b>")
	}
	seen := map[string]bool{}
	for _, w := range windowNames {
		if win, ok := limits.Windows[w.key]; ok {
			lines = append(lines, a.windowLine(w.name, win, now))
			seen[w.key] = true
		}
	}
	var others []string
	for key := range limits.Windows {
		if !seen[key] {
			others = append(others, key)
		}
	}
	sort.Strings(others)
	for _, key := range others {
		lines = append(lines, a.windowLine(strings.ReplaceAll(key, "_", " "), limits.Windows[key], now))
	}
	switch limits.Overage {
	case "rejected":
		lines = append(lines, "Extra usage past the plan: off")
	case "allowed", "allowed_warning":
		lines = append(lines, "Extra usage past the plan: on")
	}
	note := "As of " + at.In(a.Cfg.DisplayLocation).Format("15:04") + ", from Claude Code."
	if err != nil {
		note = "As of " + at.In(a.Cfg.DisplayLocation).Format("Mon 2 Jan 15:04") + "; a new reading failed: " + escape(err.Error())
	}
	return strings.Join(lines, "\n") + "\n<i>" + note + "</i>"
}

// windowLine is "This week: 21% used, 79% left · resets @ Tue 6 Oct 09:00".
func (a *App) windowLine(name string, w relay.Window, now time.Time) string {
	used := int(w.Utilization*100 + 0.5)
	left := max(100-used, 0)
	line := fmt.Sprintf("%s: <b>%d%% left</b> (%d%% used)", escape(name), left, used)
	if w.ResetsAt > 0 {
		resets := w.Resets().In(a.Cfg.DisplayLocation)
		when := resets.Format("15:04")
		if resets.YearDay() != now.YearDay() || resets.Year() != now.Year() {
			when = resets.Format("Mon 2 Jan 15:04")
		}
		line += " · resets @ " + when
	}
	return line
}

func (a *App) searchText(ctx context.Context) string {
	if !a.Search.Enabled() {
		return "Not set up: no TAVILY_API_KEY."
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u, err := a.Search.Usage(ctx)
	if err != nil {
		return "Could not be read: " + escape(err.Error())
	}
	text := fmt.Sprintf("<b>%s of %s credits left</b> this month (%s used)",
		thousands(max(u.Limit-u.Used, 0)), thousands(u.Limit), thousands(u.Used))
	if u.Plan != "" {
		text += ", " + escape(u.Plan) + " plan"
	}
	return text + "\n<i>Tavily's count runs a little behind the searches.</i>"
}

// thousands writes 1000 as "1,000".
func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// placeName is a zone as a reader says it: "Singapore" for Asia/Singapore.
func placeName(loc *time.Location) string {
	name := loc.String()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ReplaceAll(name, "_", " ")
}
