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

	// RELAY_ANSWER values. Claude is the service as deployed: each model call
	// runs Claude Code headless. Session waits for someone to write the reply,
	// which is how a run is watched and answered by hand.
	AnswerClaude  = "claude"
	AnswerSession = "session"

	// DefaultRelayConcurrency is how many sorting batches run at once. Each is
	// its own Claude Code process, and a small machine runs out of memory
	// before it runs out of patience.
	DefaultRelayConcurrency = 2

	// DefaultMaxArticles bounds one prompt. It is a ceiling for heavy days, not
	// a routine filter: at 400 it cut 82 of 482 articles on an ordinary day, and
	// rating those showed oil-supply and Middle East stories among them. A
	// normal day now fits whole, and triage removes the trivia before the cap
	// is reached.
	DefaultMaxArticles = 600

	// SOURCE_LINKS values. Short is the default; off suits a reader who wants
	// the analysis alone, and full is for checking the brief against its
	// sources.
	SourceLinksOff   = "off"
	SourceLinksShort = "short"
	SourceLinksFull  = "full"
	DefaultDataDir   = "./data"
)

// Config is the process configuration, read entirely from the environment.
// Secrets live here; user-editable preferences live in Prefs on the data
// volume, because the bot rewrites those at runtime.
type Config struct {
	TelegramBotToken string
	TelegramChatID   int64 // 0 until /start records it into Prefs

	// Every model call goes through the relay: written to a file under
	// RelayDir, answered, and the answer written beside it. RelayAnswer says who
	// answers -- AnswerClaude or AnswerSession -- and ClaudeBin is the Claude
	// Code executable the first of those runs.
	RelayDir    string
	RelayAnswer string
	ClaudeBin   string

	// StageModels overrides the model for a stage (triage, brief, names,
	// analysis), by alias or full name, from MODEL_TRIAGE and the like. A stage
	// not set here uses the relay's default: Haiku to sort and spot, Opus to
	// write.
	StageModels map[string]string

	// RelayConcurrency bounds how many sorting batches are answered at once,
	// and CallTimeout how long one call may take.
	RelayConcurrency int
	CallTimeout      time.Duration

	// ClaudeToken is CLAUDE_CODE_OAUTH_TOKEN. The service never uses it: Claude
	// Code reads it from the environment. It is held only so that logs and chat
	// replies can scrub it out of anything that quotes it.
	ClaudeToken string

	// Triage has a small model rate and place every article before the cap and
	// the brief. Off, ranking falls back to keyword matches and source weight
	// alone.
	Triage bool

	// UserAgent identifies the service to publishers. Empty means the feed
	// package's own default, which carries no contact address -- SEC EDGAR
	// answers that with 403, so a real deployment sets this.
	UserAgent string

	// FinnhubAPIKey and TwelveDataAPIKey will carry share prices into the brief:
	// Finnhub for US listings, Twelve Data for the other major exchanges. Empty
	// until the keys exist, and nothing reads them yet.
	FinnhubAPIKey    string
	TwelveDataAPIKey string

	// FREDAPIKey enables the market-levels block. Free to obtain, but not
	// universal, so an empty key omits the block rather than failing the run.
	FREDAPIKey string

	DataDir string

	// ScheduleLocation and ReportAt together fix when the daily report fires.
	// DisplayLocation only affects how timestamps are rendered to the reader.
	ScheduleLocation *time.Location
	ReportAt         ClockTime
	DisplayLocation  *time.Location

	// Discover adds the "new names in the news" section: companies the day's
	// stories were about that no watchlist tracks, with every ticker checked
	// against the exchange before it is shown.
	Discover bool

	// SkipWeekends suppresses the Saturday and Sunday briefs, which would cover
	// days the US market was shut.
	SkipWeekends bool

	// ReplacePrevious deletes the previous brief before sending a new one.
	// Off by default: deleting is irreversible, and a reader may want to look
	// back at yesterday. Useful while iterating, when repeated test runs would
	// otherwise bury the chat.
	ReplacePrevious bool

	// SourceLinks says how much of the source list the brief carries: short
	// (the first few per section), full (every article, plus the general news the
	// overview drew on, for checking the brief against its evidence), or off (no
	// source list at all). Set with SOURCE_LINKS.
	SourceLinks string

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
		ClaudeToken:      os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
		RelayAnswer:      strings.ToLower(envOr("RELAY_ANSWER", AnswerClaude)),
		ClaudeBin:        envOr("CLAUDE_BIN", "claude"),
		UserAgent:        envOr("USER_AGENT", ""),
		FREDAPIKey:       envOr("FRED_API_KEY", ""),
		FinnhubAPIKey:    envOr("FINNHUB_API_KEY", ""),
		TwelveDataAPIKey: envOr("TWELVEDATA_API_KEY", ""),
		DataDir:          envOr("DATA_DIR", DefaultDataDir),
	}

	var missing []string
	if cfg.TelegramBotToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	var err error
	if cfg.DataDir, err = filepath.Abs(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("resolve DATA_DIR: %w", err)
	}
	if cfg.RelayDir, err = filepath.Abs(envOr("RELAY_DIR", filepath.Join(cfg.DataDir, "relay"))); err != nil {
		return nil, fmt.Errorf("resolve RELAY_DIR: %w", err)
	}
	switch cfg.RelayAnswer {
	case AnswerClaude, AnswerSession:
	default:
		return nil, fmt.Errorf("RELAY_ANSWER: want claude or session, got %q", cfg.RelayAnswer)
	}
	cfg.StageModels = map[string]string{}
	for stage, key := range stageModelVars {
		if v := envOr(key, ""); v != "" {
			cfg.StageModels[stage] = v
		}
	}
	if cfg.RelayConcurrency, err = envInt("RELAY_CONCURRENCY", DefaultRelayConcurrency); err != nil {
		return nil, err
	}
	// A person answering by hand may take hours; a headless call that has
	// not answered in twenty minutes has hung.
	defaultTimeout := 20 * time.Minute
	if cfg.RelayAnswer == AnswerSession {
		defaultTimeout = 3 * time.Hour
	}
	if cfg.CallTimeout, err = envDuration("CALL_TIMEOUT", defaultTimeout); err != nil {
		return nil, err
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
	switch links := strings.ToLower(envOr("SOURCE_LINKS", SourceLinksShort)); links {
	case SourceLinksShort, SourceLinksFull, SourceLinksOff:
		cfg.SourceLinks = links
	default:
		return nil, fmt.Errorf("SOURCE_LINKS: want off, short or full, got %q", links)
	}
	if cfg.ReplacePrevious, err = envBool("REPLACE_PREVIOUS", false); err != nil {
		return nil, err
	}
	if cfg.SkipWeekends, err = envBool("SKIP_WEEKENDS", true); err != nil {
		return nil, err
	}
	if cfg.Triage, err = envBool("TRIAGE", true); err != nil {
		return nil, err
	}
	if cfg.Discover, err = envBool("DISCOVER", true); err != nil {
		return nil, err
	}
	if cfg.MaxArticles, err = envInt("MAX_ARTICLES", DefaultMaxArticles); err != nil {
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

// stageModelVars are the variables that override a stage's model.
var stageModelVars = map[string]string{
	"triage":   "MODEL_TRIAGE",
	"brief":    "MODEL_BRIEF",
	"names":    "MODEL_NAMES",
	"analysis": "MODEL_ANALYSIS",
}

// PrefsPath is where the user-editable preferences file lives on the volume.
func (c *Config) PrefsPath() string { return filepath.Join(c.DataDir, "prefs.yaml") }

// DatabasePath is where the dedupe history database lives on the volume.
func (c *Config) DatabasePath() string { return filepath.Join(c.DataDir, "market-watch.db") }

// NextRun returns the next scheduled report time after from, skipping the days
// the brief would have nothing to report on.
//
// The report fires after the US close, so a Saturday run covers a Saturday: the
// market was shut, and the news is Friday's, which Friday's brief already
// carried. Skipping both weekend days means the reader gets a brief on the
// morning after each trading day, and none on a morning that would only repeat
// the last one.
func (c *Config) NextRun(from time.Time) time.Time {
	next := c.ReportAt.Next(from, c.ScheduleLocation)
	if !c.SkipWeekends {
		return next
	}
	// At most two skips: a Saturday moves to Monday, a Sunday to Monday.
	for i := 0; i < 2 && isWeekend(next); i++ {
		next = c.ReportAt.Next(next, c.ScheduleLocation)
	}
	return next
}

// isWeekend reports whether the brief would cover a day the US market was shut.
// Holidays are not handled: they move every year and a quiet brief on Christmas
// Day costs one brief's worth of the plan's allowance, where a wrong holiday
// table would silently skip a trading day.
func isWeekend(t time.Time) bool {
	switch t.Weekday() {
	case time.Saturday, time.Sunday:
		return true
	default:
		return false
	}
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

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: want true or false, got %q", key, raw)
	}
	return v, nil
}
