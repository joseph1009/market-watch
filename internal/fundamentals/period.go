package fundamentals

import (
	"math"
	"time"
)

// MonthSpan names a period by its months: "Sep 2025–Aug 2026" for a year,
// "Jun–Aug 2026" for a quarter within one calendar year. The owner found "the
// year to 3 September 2026" vague on 4 October 2026 and asked for the dates.
//
// A company whose year is weeks rather than months ends it on a weekday:
// Micron's quarter of June, July and August ended on 3 September 2026. A
// period ending in a month's first week is named for the month before, as
// the company names it.
func MonthSpan(end time.Time, months int) string {
	last := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	if end.Day() <= 7 {
		last = last.AddDate(0, -1, 0)
	}
	if months < 1 {
		months = 1
	}
	first := last.AddDate(0, -(months - 1), 0)
	switch {
	case months == 1:
		return last.Format("Jan 2006")
	case months == 12 && first.Month() == time.January:
		return last.Format("2006") // a calendar year

	case first.Year() == last.Year() && months < 12:
		return first.Format("Jan") + "–" + last.Format("Jan 2006")
	}
	return first.Format("Jan 2006") + "–" + last.Format("Jan 2006")
}

// spanOfDays is MonthSpan for a period of so many days.
func spanOfDays(end time.Time, days int) string {
	return MonthSpan(end, int(math.Round(float64(days)/30.44)))
}

// YearEnding names the twelve months to a year's end as Nasdaq writes it,
// "Aug 2027", as "Sep 2026–Aug 2027". Anything else is returned as it is.
func YearEnding(end string) string {
	t, err := time.Parse("Jan 2006", end)
	if err != nil {
		return end
	}
	return MonthSpan(t.AddDate(0, 0, 14), 12)
}
