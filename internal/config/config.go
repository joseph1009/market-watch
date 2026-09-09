package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	// Minimal containers ship no zoneinfo database, so LoadLocation would fail
	// at runtime. Embedding it keeps the schedule correct wherever this runs.
	_ "time/tzdata"
)

const (
	// The report is scheduled in US Eastern time, not the reader's timezone,
	// so it stays pinned to the same point in the US trading day year-round:
	// 20:30 ET is ~4.5h after the close. Read in Singapore that lands at
	// 09:30 SGT while New York is on EST and 08:30 SGT while it is on EDT --
	// Singapore itself has no DST, so the hour shift comes entirely from the
	// US transition and needs no special handling here.
	DefaultScheduleTZ = "America/New_York"
	DefaultReportAt   = "20:30"
	DefaultDisplayTZ  = "Asia/Singapore"

	DefaultModel   = "claude-opus-5"
	DefaultDataDir = "./data"
)

// Config is the process configuration, read entirely from the environment.
// Secrets live here; user-editable preferences live in Prefs on the data
// volume, because the bot rewrites those at runtime.
type Config struct {
	TelegramBotToken string
	TelegramChatID   int64 // 0 until /start records it into Prefs

	AnthropicAPIKey string
	Model           string

	// UserAgent identifies the service to publishers. Empty means the feed
	// package's own default, which carries no contact address -- SEC EDGAR
	// answers that with 403, so a real deployment sets this.
	UserAgent string

	DataDir string

	// ScheduleLocation and ReportAt together fix when the daily report fires.
	// DisplayLocation only affects how timestamps are rendered to the reader.
	ScheduleLocation *time.Location
	ReportAt         ClockTime
	DisplayLocation  *time.Location

	MaxArticles int
	HTTPTimeout time.Duration
	LogLevel    slog.Level
}

// Load reads configuration from the environment, applying defaults and
// reporting every missing required variable at once rather than one per run.
func Load() (*Config, error) {
	// Local convenience only: absent in production, and never overrides a
	// variable the platform already set.
	if err := LoadDotEnv(DefaultEnvFile); err != nil {
		return nil, err
	}

	cfg := &Config{
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		Model:            envOr("CLAUDE_MODEL", DefaultModel),
		UserAgent:        envOr("USER_AGENT", ""),
		DataDir:          envOr("DATA_DIR", DefaultDataDir),
	}

	var missing []string
	if cfg.TelegramBotToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if cfg.AnthropicAPIKey == "" {
		missing = append(missing, "ANTHROPIC_API_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	var err error
	if cfg.DataDir, err = filepath.Abs(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("resolve DATA_DIR: %w", err)
	}
	if cfg.ScheduleLocation, err = loadLocation("SCHEDULE_TZ", DefaultScheduleTZ); err != nil {
		return nil, err
	}
	if cfg.DisplayLocation, err = loadLocation("DISPLAY_TZ", DefaultDisplayTZ); err != nil {
		return nil, err
	}
	if cfg.ReportAt, err = ParseClockTime(envOr("REPORT_AT", DefaultReportAt)); err != nil {
		return nil, fmt.Errorf("parse REPORT_AT: %w", err)
	}
	if cfg.TelegramChatID, err = envInt64("TELEGRAM_CHAT_ID", 0); err != nil {
		return nil, err
	}
	if cfg.MaxArticles, err = envInt("MAX_ARTICLES", 250); err != nil {
		return nil, err
	}
	if cfg.HTTPTimeout, err = envDuration("HTTP_TIMEOUT", 20*time.Second); err != nil {
		return nil, err
	}
	if cfg.LogLevel, err = envLogLevel("LOG_LEVEL", slog.LevelInfo); err != nil {
		return nil, err
	}

	return cfg, nil
}

// PrefsPath is where the user-editable preferences file lives on the volume.
func (c *Config) PrefsPath() string { return filepath.Join(c.DataDir, "prefs.yaml") }

// DatabasePath is where the dedupe history database lives on the volume.
func (c *Config) DatabasePath() string { return filepath.Join(c.DataDir, "market-watch.db") }

// NextRun returns the next scheduled report time after from.
func (c *Config) NextRun(from time.Time) time.Time {
	return c.ReportAt.Next(from, c.ScheduleLocation)
}

// ClockTime is a wall-clock time of day, interpreted in some location.
type ClockTime struct {
	Hour   int
	Minute int
}

func ParseClockTime(s string) (ClockTime, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return ClockTime{}, fmt.Errorf("want HH:MM, got %q", s)
	}
	return ClockTime{Hour: t.Hour(), Minute: t.Minute()}, nil
}

func (c ClockTime) String() string { return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute) }

// Next returns the first occurrence of the clock time strictly after from.
func (c ClockTime) Next(from time.Time, loc *time.Location) time.Time {
	from = from.In(loc)
	next := time.Date(from.Year(), from.Month(), from.Day(), c.Hour, c.Minute, 0, 0, loc)
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func loadLocation(key, fallback string) (*time.Location, error) {
	name := envOr(key, fallback)
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%s: load timezone %q: %w", key, name, err)
	}
	return loc, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: want an integer, got %q", key, raw)
	}
	return v, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: want an integer, got %q", key, raw)
	}
	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: want a duration such as 20s, got %q", key, raw)
	}
	return v, nil
}

func envLogLevel(key string, fallback slog.Level) (slog.Level, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("%s: want debug, info, warn or error, got %q", key, raw)
	}
	return level, nil
}
