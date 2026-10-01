package telegram

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// flags mark the currency a release moves, which is the economy it measures.
var flags = map[string]string{
	"USD": "🇺🇸", "EUR": "🇪🇺", "GBP": "🇬🇧", "JPY": "🇯🇵", "CNY": "🇨🇳",
	"AUD": "🇦🇺", "CAD": "🇨🇦", "CHF": "🇨🇭", "NZD": "🇳🇿",
}

// impactMarks say how much the calendar expects a release to move markets.
var impactMarks = map[string]string{"High": "🔴", "Medium": "🟠"}

// renderCalendar lays out what is due: a block a day, the releases in time
// order with their forecast and previous figure, then the companies
// reporting with what analysts expect. The program writes it from the
// calendars, so the times and figures are theirs, not a model's.
func renderCalendar(cal model.Calendar, now time.Time, display *time.Location) []string {
	if cal.Empty() {
		return nil
	}
	type day struct {
		label    string
		releases []string
		results  []string
	}
	var order []string
	days := map[string]*day{}
	get := func(t time.Time) *day {
		key := t.Format(time.DateOnly)
		if d, ok := days[key]; ok {
			return d
		}
		label := t.Format("Monday 2 January")
		if key == now.In(t.Location()).Format(time.DateOnly) {
			label = "Today, " + label
		}
		days[key] = &day{label: label}
		order = append(order, key)
		return days[key]
	}

	for _, e := range cal.Events {
		line := fmt.Sprintf("%s %s <b>%s</b> (%s New York) %s",
			impactMarks[e.Impact], flagFor(e.Country), e.At.In(display).Format("15:04"), e.At.Format("15:04"), escape(e.Title))
		var figures []string
		if e.Forecast != "" {
			figures = append(figures, "forecast <b>"+escape(e.Forecast)+"</b>")
		}
		if e.Previous != "" {
			figures = append(figures, "previous "+escape(e.Previous))
		}
		if len(figures) > 0 {
			line += " — " + strings.Join(figures, " · ")
		}
		d := get(e.At)
		d.releases = append(d.releases, line)
	}
	for _, r := range cal.Earnings {
		mark := "📈"
		if r.Followed {
			mark = "⭐"
		}
		line := fmt.Sprintf("%s <b>%s</b> %s", mark, escape(r.Symbol), escape(shortName(r.Name)))
		if r.When != "" {
			line += ", " + escape(r.When)
		}
		if r.Forecast != "" {
			line += " — expected <b>" + escape(r.Forecast) + "</b> a share"
			if r.LastYear != "" {
				line += " (" + escape(r.LastYear) + " a year ago)"
			}
		}
		d := get(r.Day)
		d.results = append(d.results, line)
	}

	blocks := []string{divider + "\n<b>📅 COMING UP</b>\n<i>Times in Singapore. 🔴 high impact · 🟠 medium · ⭐ a company you follow. Forecasts are the calendars' consensus.</i>"}
	// Day by day, soonest first: a day with results and no releases is
	// first seen after the releases, wherever it falls.
	sort.Strings(order)
	for _, key := range order {
		d := days[key]
		lines := []string{"<b>" + escape(d.label) + "</b>"}
		lines = append(lines, d.releases...)
		if len(d.results) > 0 {
			lines = append(lines, "<i>Results</i>")
			lines = append(lines, d.results...)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return blocks
}

func flagFor(country string) string {
	if f := flags[country]; f != "" {
		return f
	}
	return country
}

// shortName takes the corporate form off a company's name: "Nike, Inc." is
// "Nike".
func shortName(name string) string {
	for _, cut := range []string{", Inc.", " Inc.", " Incorporated", " Corporation", " Corp.", " plc", " Ltd.", " Limited", ", Inc", " Company", " Co."} {
		if i := strings.Index(name, cut); i > 0 {
			name = name[:i]
		}
	}
	return strings.TrimSuffix(strings.TrimSpace(name), ",")
}
