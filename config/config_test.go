package config

import (
	"slices"
	"testing"
	"time"
)

// The report is scheduled in US Eastern time so it stays fixed relative to the
// US open. This asserts the consequence the schedule actually exists for: a
// Singapore reader gets it at 20:30 while New York is on EST and 19:30 while it
// is on EDT, with the shift coming from the US transition alone.
func TestReportArrivesAt2030SGTUnderESTAnd1930UnderEDT(t *testing.T) {
	schedule, err := time.LoadLocation(DefaultScheduleTZ)
	if err != nil {
		t.Fatalf("load schedule timezone: %v", err)
	}
	display, err := time.LoadLocation(DefaultDisplayTZ)
	if err != nil {
		t.Fatalf("load display timezone: %v", err)
	}

	reportAt, err := ParseClockTime(DefaultReportAt)
	if err != nil {
		t.Fatalf("parse default report time: %v", err)
	}
	cfg := &Config{ScheduleLocation: schedule, DisplayLocation: display, ReportAt: reportAt}

	tests := []struct {
		name string
		from time.Time
		want string
	}{
		{
			name: "New York on EST",
			from: time.Date(2027, time.January, 15, 12, 0, 0, 0, schedule),
			want: "2027-01-16 20:30 +0800",
		},
		{
			name: "New York on EDT",
			from: time.Date(2027, time.July, 15, 12, 0, 0, 0, schedule),
			want: "2027-07-16 19:30 +0800",
		},
		{
			// The Sunday the US springs forward; the following report is the
			// first to land an hour earlier in Singapore.
			name: "day US DST begins",
			from: time.Date(2027, time.March, 14, 12, 0, 0, 0, schedule),
			want: "2027-03-15 19:30 +0800",
		},
		{
			// The Sunday the US falls back, shifting Singapore delivery later.
			name: "day US DST ends",
			from: time.Date(2027, time.November, 7, 12, 0, 0, 0, schedule),
			want: "2027-11-08 20:30 +0800",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.NextRun(tt.from).In(display).Format("2006-01-02 15:04 -0700")
			if got != tt.want {
				t.Errorf("delivery in Singapore = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNextRunRollsToTomorrowOnceTodaysTimeHasPassed(t *testing.T) {
	schedule := time.FixedZone("TEST", 0)
	at := ClockTime{Hour: 20, Minute: 30}

	// Exactly at the scheduled time counts as passed: the run firing now must
	// not immediately reschedule itself for the same instant.
	from := time.Date(2027, time.January, 15, 20, 30, 0, 0, schedule)
	got := at.Next(from, schedule)
	want := time.Date(2027, time.January, 16, 20, 30, 0, 0, schedule)
	if !got.Equal(want) {
		t.Errorf("Next(%s) = %s, want %s", from, got, want)
	}

	from = time.Date(2027, time.January, 15, 20, 29, 59, 0, schedule)
	got = at.Next(from, schedule)
	want = time.Date(2027, time.January, 15, 20, 30, 0, 0, schedule)
	if !got.Equal(want) {
		t.Errorf("Next(%s) = %s, want %s", from, got, want)
	}
}

func TestParseClockTimeRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "9:30am", "25:00", "07-30", "half past eight"} {
		if _, err := ParseClockTime(in); err == nil {
			t.Errorf("ParseClockTime(%q) succeeded, want an error", in)
		}
	}
	if got, err := ParseClockTime(" 08:05 "); err != nil || got.String() != "08:05" {
		t.Errorf("ParseClockTime(\" 08:05 \") = %v, %v; want 08:05, nil", got, err)
	}
}

// Every credential can reach an error's text -- Finnhub's key sits in the
// query string that net/http repeats when a request times out -- so every one
// is in the list the log and the chat messages are scrubbed of.
func TestSecretsNameEveryCredential(t *testing.T) {
	keys := map[string]string{
		"TELEGRAM_BOT_TOKEN":      "123456:telegram-secret",
		"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-claude-secret",
		"FINNHUB_API_KEY":         "finnhub-secret",
		"FRED_API_KEY":            "fred-secret-value",
		"TAVILY_API_KEY":          "tvly-secret-value",
		"MASSIVE_API_KEY":         "massive-secret",
		"TWELVEDATA_API_KEY":      "twelvedata-secret",
	}
	for name, value := range keys {
		t.Setenv(name, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]string{cfg.Secrets(), SecretsFromEnv()} {
		for name, value := range keys {
			if !slices.Contains(list, value) {
				t.Errorf("%s is not among the secrets scrubbed", name)
			}
		}
	}
}

// The chats besides the owner's that may send commands are lists, and a typo
// in one stops the service rather than quietly letting nobody in.
func TestTheCommandChatsAreLists(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:token")
	t.Setenv("TELEGRAM_COMMAND_CHATS", " 111, -100222 ,")
	t.Setenv("TELEGRAM_CONTROL_CHATS", "333")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.TelegramCommandChats, []int64{111, -100222}) {
		t.Errorf("TelegramCommandChats = %v, want [111 -100222]", cfg.TelegramCommandChats)
	}
	if !slices.Equal(cfg.TelegramControlChats, []int64{333}) {
		t.Errorf("TelegramControlChats = %v, want [333]", cfg.TelegramControlChats)
	}

	t.Setenv("TELEGRAM_CONTROL_CHATS", "333,@mybot")
	if _, err := Load(); err == nil {
		t.Error("Load accepted a chat id that is not a number")
	}
}

// The owner's chat was TELEGRAM_CHAT_ID before it was TELEGRAM_MASTER_CHAT_ID.
// A machine whose secrets predate the rename keeps its owner, and the new name
// wins where both are set.
func TestTheMasterChatKeepsItsEarlierName(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:token")
	t.Setenv("TELEGRAM_CHAT_ID", "4242")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TelegramMasterChatID != 4242 {
		t.Errorf("TelegramMasterChatID = %d, want 4242 from TELEGRAM_CHAT_ID", cfg.TelegramMasterChatID)
	}

	t.Setenv("TELEGRAM_MASTER_CHAT_ID", "777")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.TelegramMasterChatID != 777 {
		t.Errorf("TelegramMasterChatID = %d, want 777 from TELEGRAM_MASTER_CHAT_ID", cfg.TelegramMasterChatID)
	}
}

// The brief after the close comes each weekday at its own time, and the
// schedule's next brief is whichever of the two comes first. Friday's is
// followed by Monday's brief before the open.
func TestTheBriefAfterTheCloseIsOnWeekdays(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	close := ClockTime{Hour: 18, Minute: 30}
	cfg := &Config{ScheduleLocation: ny, ReportAt: ClockTime{Hour: 7, Minute: 30}, CloseAt: &close, SkipWeekends: true}
	at := func(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, ny) }

	for _, tc := range []struct {
		from, want time.Time
	}{
		{at(13, 7, 0), at(13, 7, 30)},   // Friday morning: the brief before the open
		{at(13, 10, 0), at(13, 18, 30)}, // Friday daytime: the brief after the close
		{at(13, 19, 0), at(16, 7, 30)},  // Friday night: Monday's before the open
		{at(14, 19, 0), at(16, 7, 30)},  // Saturday: no brief after a close
	} {
		if got := cfg.NextBrief(tc.from); !got.Equal(tc.want) {
			t.Errorf("from %s: next brief %s, want %s", tc.from.Format("Mon 15:04"), got.Format("Mon 15:04"), tc.want.Format("Mon 15:04"))
		}
	}
	for when, want := range map[time.Time]bool{at(13, 18, 30): true, at(13, 16, 5): true, at(13, 7, 30): false, at(14, 18, 30): false} {
		if got := cfg.AfterClose(when); got != want {
			t.Errorf("AfterClose(%s) = %v, want %v", when.Format("Mon 15:04"), got, want)
		}
	}
	cfg.CloseAt = nil
	if cfg.AfterClose(at(13, 18, 30)) || !cfg.NextClose(at(13, 10, 0)).IsZero() {
		t.Error("with the brief after the close off, there still is one")
	}
}

// CLOSE_AT is a time after the close, or off.
func TestCloseAtIsAfterTheClose(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:token")
	t.Setenv("CLOSE_AT", "")
	if cfg, err := Load(); err != nil || cfg.CloseAt == nil || cfg.CloseAt.String() != "18:30" {
		t.Errorf("by default: %v, %v; want 18:30", cfg.CloseAt, err)
	}
	t.Setenv("CLOSE_AT", "off")
	if cfg, err := Load(); err != nil || cfg.CloseAt != nil {
		t.Errorf("CLOSE_AT=off: %v, %v; want none", cfg.CloseAt, err)
	}
	t.Setenv("CLOSE_AT", "15:00")
	if _, err := Load(); err == nil {
		t.Error("Load accepted a brief after the close at 15:00, before the close")
	}
}
