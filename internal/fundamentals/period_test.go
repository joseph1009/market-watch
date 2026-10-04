package fundamentals

import (
	"testing"
	"time"
)

// A period is named by its months, the first week of a month counting as the
// month before: Micron's year to 3 September 2026 is Sep 2025–Aug 2026.
func TestAPeriodIsNamedByItsMonths(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	for _, c := range []struct {
		end    string
		months int
		want   string
	}{
		{"2026-09-03", 12, "Sep 2025–Aug 2026"},
		{"2026-09-03", 3, "Jun–Aug 2026"},
		{"2026-02-26", 3, "Dec 2025–Feb 2026"},
		{"2025-12-31", 12, "2025"},
		{"2026-05-28", 9, "Sep 2025–May 2026"},
	} {
		if got := MonthSpan(day(c.end), c.months); got != c.want {
			t.Errorf("MonthSpan(%s, %d) = %q, want %q", c.end, c.months, got, c.want)
		}
	}
	if got := YearEnding("Aug 2027"); got != "Sep 2026–Aug 2027" {
		t.Errorf("YearEnding = %q", got)
	}
}
