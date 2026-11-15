package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/alerts"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// alertBudget bounds one check: the feeds and one short call.
const alertBudget = 10 * time.Minute

// RunAlerts looks at the feeds every AlertEvery, day and night, and sends the
// owner the stories big enough not to wait for the next brief. It runs beside
// the briefs rather than waiting for them: a brief takes half an hour, and
// an alert is worth having at once.
func (a *App) RunAlerts(ctx context.Context) error {
	if a.Alerts == nil || a.AlertLog == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	a.Log.Info("alerts on", "every", a.Cfg.AlertEvery)
	tick := time.NewTicker(a.Cfg.AlertEvery)
	defer tick.Stop()
	for {
		a.checkAlerts(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// checkAlerts is one look at the feeds. Nothing in it is fatal: a check that
// fails is followed by the next, which looks at the same stories again.
func (a *App) checkAlerts(ctx context.Context) {
	prefs := a.Prefs()
	if prefs.ChatID == 0 || prefs.AlertsOff {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, alertBudget)
	defer cancel()
	now := a.now()

	var sources []model.Source
	for _, s := range prefs.EnabledSources() {
		if s.URL != "" {
			sources = append(sources, s)
		}
	}
	fetched, failed := a.Fetcher.Fetch(ctx, sources)
	stories := feed.DropStale(fetched, now, alerts.Window)
	stories = feed.Dedupe(stories, sources)

	// The first look only takes note of what is there: none of it is news
	// to alert on.
	if !a.AlertLog.Started() {
		if err := a.AlertLog.MarkSeen(stories, now); err != nil {
			a.Log.Warn("alerts: could not save what was seen", "error", err)
		}
		a.Log.Info("alerts: first look", "stories", len(stories), "failed_sources", len(failed))
		return
	}
	fresh := a.AlertLog.Unseen(stories)
	if len(fresh) == 0 {
		return
	}
	fresh = feed.Match(fresh, prefs.Groups)

	display := a.Cfg.DisplayLocation
	local := now.In(display)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, display)
	sent := a.AlertLog.SentSince(today)
	if len(sent) >= alerts.PerDay {
		// Marked seen all the same: tomorrow's allowance is for tomorrow's news.
		_ = a.AlertLog.MarkSeen(fresh, now)
		a.Log.Info("alerts: today's allowance is used", "sent", len(sent), "skipped", len(fresh))
		return
	}

	// Not while a brief is being written: it runs two Claude processes of
	// its own, and a third would crowd the machine's memory. The stories
	// stay unseen, so the first check after the brief looks at them.
	if !a.running.TryLock() {
		a.Log.Info("alerts: a brief is running; looking again next time", "stories", len(fresh))
		return
	}
	picked, usage, err := a.Alerts.Pick(ctx, fresh, prefs.Groups, sent, now, display)
	a.running.Unlock()
	if err != nil {
		a.Log.Warn("alerts: the check failed; the next one looks again", "stories", len(fresh), "error", err)
		return
	}
	if err := a.AlertLog.MarkSeen(fresh, now); err != nil {
		a.Log.Warn("alerts: could not save what was seen", "error", err)
	}
	a.Log.Info("alerts: checked", "stories", len(fresh), "alerts", len(picked),
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)

	for _, al := range picked[:min(len(picked), alerts.PerDay-len(sent))] {
		if err := a.Bot.SendMessage(ctx, prefs.ChatID, alertText(al, display)); err != nil {
			a.Log.Warn("alerts: could not send", "headline", al.Headline, "error", err)
			continue
		}
		if err := a.AlertLog.Record(alerts.Sent{At: a.now(), Headline: al.Headline, URL: al.Articles[0].URL}); err != nil {
			a.Log.Warn("alerts: could not record what was sent", "error", err)
		}
	}
}

// maxAlertLinks is how many of a story's sources an alert links to.
const maxAlertLinks = 3

// everyMinutes says how often, as the reader would.
func everyMinutes(d time.Duration) string {
	return fmt.Sprintf("%d minutes", int(d.Round(time.Minute).Minutes()))
}

// alertText is one alert as the chat shows it: what happened, why it
// matters, and where it was reported, with the time.
func alertText(al alerts.Alert, display *time.Location) string {
	var links []string
	for _, art := range al.Articles[:min(len(al.Articles), maxAlertLinks)] {
		links = append(links, fmt.Sprintf(`<a href="%s">%s</a>`, escape(art.URL), escape(art.SourceName)))
	}
	text := "🚨 <b>" + escape(al.Headline) + "</b>"
	if al.What != "" {
		text += "\n\n" + escape(al.What)
	}
	when := al.Articles[0].Published.In(display).Format("15:04") + " " + placeName(display) + " time"
	return text + "\n\n" + strings.Join(links, " · ") + " · " + escape(when)
}

// handleAlerts answers /alerts: whether they are on, and what today's were.
// /alerts off pauses them, and /alerts on starts them again.
func (a *App) handleAlerts(ctx context.Context, msg telegram.Message, args []string) error {
	if a.Alerts == nil || a.AlertLog == nil {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, "Alerts are switched off on the server (ALERTS=false).")
	}
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "on", "off":
			off := strings.EqualFold(args[0], "off")
			if err := a.UpdatePrefs(func(p *config.Prefs) error {
				p.AlertsOff = off
				return nil
			}); err != nil {
				return err
			}
			if off {
				return a.Bot.SendMessage(ctx, msg.Chat.ID, "Alerts are paused. The briefs carry on as usual. Send /alerts on to start them again.")
			}
			return a.Bot.SendMessage(ctx, msg.Chat.ID, "Alerts are on again.")
		default:
			return a.Bot.SendMessage(ctx, msg.Chat.ID, "Send /alerts to see today's, or /alerts on or /alerts off.")
		}
	}

	display := a.Cfg.DisplayLocation
	local := a.now().In(display)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, display)
	sent := a.AlertLog.SentSince(today)

	var b strings.Builder
	if a.Prefs().AlertsOff {
		b.WriteString("<b>Alerts</b>\nPaused. Send /alerts on to start them again.")
	} else {
		fmt.Fprintf(&b, "<b>Alerts</b>\nOn. I look at the news every %s, day and night, and send what could move the whole market or is major news for one of your companies. At most %d a day.",
			everyMinutes(a.Cfg.AlertEvery), alerts.PerDay)
	}
	if len(sent) == 0 {
		b.WriteString("\n\nNone today so far.")
	} else {
		fmt.Fprintf(&b, "\n\nToday's, %d:", len(sent))
		for _, s := range sent {
			fmt.Fprintf(&b, "\n• %s %s", escape(s.At.In(display).Format("15:04")), escape(s.Headline))
		}
	}
	if !a.Prefs().AlertsOff {
		b.WriteString("\n\nSend /alerts off to pause them.")
	}
	return a.Bot.SendMessage(ctx, msg.Chat.ID, b.String())
}
