package config

import (
	"testing"
	"time"
)

func newYork(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	return loc
}

func scheduled(t *testing.T, skip bool) *Config {
	t.Helper()
	return &Config{
		ScheduleLocation: newYork(t),
		ReportAt:         ClockTime{Hour: 20, Minute: 30},
		SkipWeekends:     skip,
	}
}

// The brief fires after the close, so a Saturday run would report on a day the
// market was shut, with Friday's news that Friday's brief already carried.
func TestNextRunSkipsTheWeekend(t *testing.T) {
	ny := newYork(t)
	cfg := scheduled(t, true)

	tests := []struct {
		name string
		from time.Time
		want string
	}{
		{
			name: "Friday evening after the brief goes to Monday",
			from: time.Date(2026, 9, 11, 21, 0, 0, 0, ny),
			want: "Mon, 14 Sep 2026 20:30",
		},
		{
			name: "Saturday goes to Monday",
			from: time.Date(2026, 9, 12, 9, 0, 0, 0, ny),
			want: "Mon, 14 Sep 2026 20:30",
		},
		{
			name: "Sunday goes to Monday",
			from: time.Date(2026, 9, 13, 9, 0, 0, 0, ny),
			want: "Mon, 14 Sep 2026 20:30",
		},
		{
			name: "a weekday is untouched",
			from: time.Date(2026, 9, 15, 9, 0, 0, 0, ny),
			want: "Tue, 15 Sep 2026 20:30",
		},
		{
			name: "Friday before the brief still fires that evening",
			from: time.Date(2026, 9, 11, 9, 0, 0, 0, ny),
			want: "Fri, 11 Sep 2026 20:30",
		},
	}
	for _, tt := range tests {
		got := cfg.NextRun(tt.from).Format("Mon, 2 Jan 2006 15:04")
		if got != tt.want {
			t.Errorf("%s: NextRun = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestWeekendBriefsCanBeKept(t *testing.T) {
	ny := newYork(t)
	cfg := scheduled(t, false)

	got := cfg.NextRun(time.Date(2026, 9, 11, 21, 0, 0, 0, ny)).Format("Mon, 2 Jan")
	if got != "Sat, 12 Sep" {
		t.Errorf("NextRun = %s, want Sat, 12 Sep when weekends are kept", got)
	}
}

func TestSkipWeekendsIsOnByDefault(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SKIP_WEEKENDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SkipWeekends {
		t.Error("weekend briefs are on by default")
	}
}
