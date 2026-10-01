package app

import (
	"context"
	"sort"
	"time"

	"github.com/joseph1009/market-watch/internal/calendar"
	"github.com/joseph1009/market-watch/internal/model"
)

// The brief's look ahead: the economic releases due from now to the end of
// the week, and the companies reporting results, so the reader is told what
// is coming and what is expected of it before it lands.

const (
	// calendarBudget bounds the reading: one request to ForexFactory and one
	// a day to Nasdaq.
	calendarBudget = time.Minute

	// calendarDays is how many weekdays of results are looked ahead to,
	// today included.
	calendarDays = 5

	// resultsToday and resultsLater are how large a company not followed
	// must be for its results to be listed: today, US$10bn; on a later day,
	// US$50bn, so the week ahead names the reports that move the market and
	// not every one due. Followed companies are listed whatever their size.
	resultsToday = 10e9
	resultsLater = 50e9

	// resultsPerDay caps a day's list, followed companies first.
	resultsPerDay = 8
)

// collectCalendar reads what is due. Each half is best-effort: a calendar
// that cannot be read costs the look ahead, never the brief.
func (a *App) collectCalendar(ctx context.Context, watched []string) model.Calendar {
	ctx, cancel := context.WithTimeout(ctx, calendarBudget)
	defer cancel()

	now := a.now()
	ny := a.Cfg.ScheduleLocation
	if ny == nil {
		ny = time.UTC
	}
	local := now.In(ny)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, ny)
	var days []time.Time
	for len(days) < calendarDays {
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday {
			days = append(days, day)
		}
		day = day.AddDate(0, 0, 1)
	}
	until := days[len(days)-1].AddDate(0, 0, 1)

	var cal model.Calendar
	if a.Calendar != nil {
		week, err := a.Calendar.Week(ctx)
		if err != nil {
			a.Log.Warn("economic calendar unavailable; the brief will not look ahead to releases", "error", err)
		} else {
			cal.Events = calendar.Key(week, now, until)
		}
	}

	if a.Consensus != nil {
		followed := map[string]bool{}
		for _, t := range watched {
			followed[t] = true
		}
		for i, d := range days {
			list, err := a.Consensus.Earnings(ctx, d)
			if err != nil {
				a.Log.Warn("earnings calendar unavailable", "day", d.Format(time.DateOnly), "error", err)
				continue
			}
			floor := float64(resultsLater)
			if i == 0 {
				floor = resultsToday
			}
			var keep []model.Results
			for _, r := range list {
				r.Followed = followed[r.Symbol]
				if r.Followed || r.MarketCap >= floor {
					keep = append(keep, r)
				}
			}
			sort.SliceStable(keep, func(i, j int) bool {
				if keep[i].Followed != keep[j].Followed {
					return keep[i].Followed
				}
				return keep[i].MarketCap > keep[j].MarketCap
			})
			cal.Earnings = append(cal.Earnings, keep[:min(resultsPerDay, len(keep))]...)
		}
	}
	a.Log.Info("calendar", "releases", len(cal.Events), "results", len(cal.Earnings))
	return cal
}
