package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// renderCalendar writes what is due into the prompt: the releases with their
// time, forecast and previous figure, and the companies reporting with what
// analysts expect. It is measured rather than written, like the levels, so it
// is labelled as such and carries no citation numbers.
func renderCalendar(cal model.Calendar, display *time.Location) string {
	if cal.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Coming up -- scheduled, not news. Releases and results due from now to the end of the week. These figures come from economic and earnings calendars, not from the articles: quote them exactly as given, and do not cite them with a number.\n")
	if len(cal.Events) > 0 {
		b.WriteString("Economic releases (the currency each moves; impact as the calendar rates it):\n")
		for _, e := range cal.Events {
			fmt.Fprintf(&b, "- %s, %s New York (%s Singapore): %s %s, %s impact",
				e.At.Format("Mon 2 Jan"), e.At.Format("15:04"), e.At.In(display).Format("15:04"),
				e.Country, e.Title, strings.ToLower(e.Impact))
			if e.Forecast != "" {
				fmt.Fprintf(&b, "; forecast %s", e.Forecast)
			}
			if e.Previous != "" {
				fmt.Fprintf(&b, "; previous %s", e.Previous)
			}
			b.WriteString("\n")
		}
	}
	if len(cal.Earnings) > 0 {
		b.WriteString("Company results due (consensus is analysts' average forecast of earnings per share):\n")
		for _, r := range cal.Earnings {
			fmt.Fprintf(&b, "- %s", r.Day.Format("Mon 2 Jan"))
			if r.When != "" {
				fmt.Fprintf(&b, ", %s", r.When)
			}
			fmt.Fprintf(&b, ": %s (%s)", r.Name, r.Symbol)
			if r.Forecast != "" {
				fmt.Fprintf(&b, ", consensus %s a share", r.Forecast)
			}
			if r.LastYear != "" {
				fmt.Fprintf(&b, ", %s a year earlier", r.LastYear)
			}
			if r.Estimates > 0 {
				fmt.Fprintf(&b, ", %d analysts", r.Estimates)
			}
			if r.Followed {
				b.WriteString(" -- on the reader's watchlist")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
