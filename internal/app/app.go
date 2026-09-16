// Package app wires collection, summarization and delivery into the running
// service: a scheduled daily brief plus a bot that answers commands.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/marketdata"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/sec"
	"github.com/joseph1009/market-watch/internal/telegram"
	"github.com/joseph1009/market-watch/internal/triage"
)

// App holds everything one process needs. Prefs are guarded because the bot
// rewrites them from the polling goroutine while the scheduler reads them.
type App struct {
	Cfg       *config.Config
	Log       *slog.Logger
	Fetcher   *feed.Fetcher
	Generator *report.Generator
	Bot       *telegram.Client

	// Filings reads SEC 8-K item codes. Nil disables filing collection, which
	// is what the tests use and what a run without a User-Agent falls back to.
	Filings *sec.Client

	// Levels reads market data. Disabled without a FRED key.
	Levels *marketdata.Client

	// Covered remembers which stories earlier briefs carried, so today's can say
	// what is new rather than repeating them. Nil disables the check.
	Covered *history.Store

	// Triage rates and places articles before the cap. Nil disables it.
	Triage *triage.Triager

	// Accounts and Analyzer answer /accounts: reported figures from EDGAR, and
	// the writing up of them. Nil on either disables the command.
	Accounts *fundamentals.Client
	Analyzer *fundamentals.Analyzer

	// Agent is the same analysis with the filings open: it looks up whatever
	// the company turns out to need. Nil falls back to Analyzer and its fixed
	// table.
	Agent *fundamentals.Agent

	mu    sync.RWMutex
	prefs *config.Prefs

	// running serializes report generation. A /now arriving while the daily
	// brief is being written must wait rather than start a second run, which
	// would double the API spend and interleave two deliveries.
	running sync.Mutex

	// Now is injected for tests.
	Now func() time.Time
}

// New builds the service from configuration, loading preferences from the data
// volume and seeding them on first run.
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	prefs, err := config.LoadPrefs(cfg.PrefsPath())
	if err != nil {
		return nil, err
	}

	// The chat can be pinned by environment for a first deployment; otherwise
	// /start records it. The stored value wins once set, so redeploying with a
	// stale variable cannot silently redirect the brief.
	if prefs.ChatID == 0 && cfg.TelegramChatID != 0 {
		prefs.ChatID = cfg.TelegramChatID
		if err := prefs.Save(cfg.PrefsPath()); err != nil {
			return nil, err
		}
	}

	a := &App{
		Cfg: cfg,
		Log: log,
		Fetcher: &feed.Fetcher{
			Client:    &http.Client{Timeout: cfg.HTTPTimeout},
			UserAgent: cfg.UserAgent,
		},
		Generator: &report.Generator{
			Completer:       report.NewClaude(cfg.AnthropicAPIKey, cfg.Model),
			DisplayLocation: cfg.DisplayLocation,
		},
		// Polling holds a request open for 30s, so this client must outlast it.
		Bot: telegram.New(cfg.TelegramBotToken, &http.Client{Timeout: 90 * time.Second}),
		Levels: &marketdata.Client{
			APIKey: cfg.FREDAPIKey,
			HTTP:   &http.Client{Timeout: 20 * time.Second},
		},
		Filings: &sec.Client{
			HTTP:      &http.Client{Timeout: 30 * time.Second},
			UserAgent: cfg.UserAgent,
		},
		prefs: prefs,
	}
	covered, err := history.Load(filepath.Join(cfg.DataDir, "covered.json"))
	if err != nil {
		return nil, err
	}
	a.Covered = covered

	a.Accounts = &fundamentals.Client{
		Lookup:    a.Filings,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: cfg.UserAgent,
	}
	a.Analyzer = &fundamentals.Analyzer{
		Completer: report.NewClaude(cfg.AnthropicAPIKey, cfg.Model),
	}
	a.Agent = fundamentals.NewAgent(cfg.AnthropicAPIKey, cfg.Model, a.Accounts)
	a.Agent.Log = func(format string, args ...any) {
		log.Debug("analysis lookup", "detail", fmt.Sprintf(format, args...))
	}
	if cfg.Triage {
		a.Triage = &triage.Triager{Completer: triage.NewClaude(cfg.AnthropicAPIKey, cfg.TriageModel)}
	}
	return a, nil
}

// Prefs returns a snapshot safe to read without holding the lock.
func (a *App) Prefs() config.Prefs {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return *a.prefs
}

// UpdatePrefs applies a change and persists it. The write happens under the
// lock so a bot command and a scheduled run cannot interleave.
func (a *App) UpdatePrefs(change func(*config.Prefs) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := change(a.prefs); err != nil {
		return err
	}
	return a.prefs.Save(a.Cfg.PrefsPath())
}

// ErrNoChat means nobody has run /start and no chat was configured, so there is
// nowhere to deliver.
var ErrNoChat = errors.New("no chat configured: send /start to the bot")

// SendReport runs the whole pipeline and delivers the result.
func (a *App) SendReport(ctx context.Context) error {
	// One report at a time, whoever asked for it.
	a.running.Lock()
	defer a.running.Unlock()

	prefs := a.Prefs()
	if prefs.ChatID == 0 {
		return ErrNoChat
	}

	started := a.now()

	// SEC filings are gathered before the feeds so they arrive on the same
	// footing: deduped, matched and scored with everything else rather than
	// bolted on afterwards.
	filings := a.collectFilings(ctx, prefs)

	opts := feed.Options{
		Sources: append(prefs.EnabledSources(), SECSourceEntry()),
		Groups:  prefs.Groups,
		Max:     a.Cfg.MaxArticles,
		Extra:   filings,
	}
	// Assigned only when set: a nil *Triager stored in the interface would not
	// compare equal to nil, and Collect would call through it.
	if a.Triage != nil {
		opts.Triage = a.Triage
	}
	collected := feed.Collect(ctx, a.Fetcher, opts)
	for _, e := range collected.Errors {
		a.Log.Warn("source failed", "source", e.SourceID, "error", e.Err)
	}
	// match_rate is the number a keyword change moves most directly, and the
	// one that was invisible while keywords were being tuned.
	matchRate := 0.0
	if kept := len(collected.Articles); kept > 0 {
		matchRate = float64(collected.Matched) / float64(kept)
	}
	a.Log.Info("collected",
		"fetched", collected.Fetched,
		"deduped", collected.Deduped,
		"kept", len(collected.Articles),
		"dropped", collected.Dropped,
		"matched", collected.Matched,
		"match_rate", fmt.Sprintf("%.2f", matchRate),
		"failed_sources", len(collected.Errors),
		"took", time.Since(started).Round(time.Second))

	if a.Triage != nil {
		if collected.TriageErr != nil {
			a.Log.Warn("triage incomplete; unrated articles rank on the other signals", "error", collected.TriageErr)
		}
		// cut_rated_4_plus is the number to watch: if it stays above zero, the
		// cap is discarding news that matters and should rise.
		a.Log.Info("triaged",
			"rated", collected.Rated,
			"newly_matched", collected.NewlyMatched,
			"placements_added", collected.Placements,
			"trivial_removed", collected.Trivial,
			"cut_rated_4_plus", collected.CutImportant,
			"estimated_usd", fmt.Sprintf("%.4f", collected.TriageUsage.EstimatedUSD))
	}

	if collected.AllFailed() {
		return fmt.Errorf("every source failed (%d): %v", len(collected.Errors), collected.Errors[0])
	}

	// Stories earlier briefs already carried are marked rather than dropped: a
	// running story should be reported when it moves, and silently removing it
	// would leave the reader with a development and no thread to hang it on.
	articles := collected.Articles
	if a.Covered != nil {
		repeats := a.Covered.Seen(articles)
		articles = a.Covered.Mark(articles)
		a.Log.Info("previously covered", "articles", repeats, "remembered", a.Covered.Len())
	}

	// Market levels are context, not content: a failure here costs the anchor
	// numbers, never the brief.
	a.Generator.Levels = a.collectLevels(ctx)

	rep, err := a.Generator.Generate(ctx, articles, prefs.Groups)
	if errors.Is(err, report.ErrNoArticles) {
		// A genuinely empty day is worth saying out loud, rather than leaving
		// the reader wondering whether the service died.
		return a.Bot.SendMessage(ctx, prefs.ChatID,
			"<b>📊 Market Watch</b>\nNo news was collected today. The feeds returned nothing usable.")
	}
	if err != nil {
		return err
	}
	rep.Triage = collected.TriageUsage

	a.Log.Info("generated",
		"sections", len(rep.Sections),
		"input_tokens", rep.Usage.InputTokens,
		"output_tokens", rep.Usage.OutputTokens,
		"estimated_usd", rep.Usage.EstimatedUSD,
		"took", time.Since(started).Round(time.Second))

	messages := telegram.RenderWith(rep, telegram.Options{
		Display: a.Cfg.DisplayLocation,
		Sources: sourceMode(a.Cfg.SourceLinks),
	})

	// Clearing happens after generation, not before: a run that fails to
	// produce a brief must not also have thrown away the last one.
	if a.Cfg.ReplacePrevious && len(prefs.LastBrief) > 0 {
		deleted, failed := a.Bot.DeleteMessages(ctx, prefs.ChatID, prefs.LastBrief)
		a.Log.Info("cleared previous brief", "deleted", deleted, "unavailable", failed)
	}

	ids, err := a.Bot.SendReport(ctx, prefs.ChatID, messages)
	// The ids are recorded even on a partial send, so a half-delivered brief
	// still gets cleaned up by the next run rather than lingering forever.
	if len(ids) > 0 {
		if saveErr := a.UpdatePrefs(func(p *config.Prefs) error {
			p.LastBrief = ids
			return nil
		}); saveErr != nil {
			a.Log.Warn("could not record the brief's message ids", "error", saveErr)
		}
	}
	if err != nil {
		return err
	}

	// Recorded only after delivery: a brief that never reached the reader has
	// not covered anything, and marking it would silence tomorrow's.
	if a.Covered != nil {
		if err := a.Covered.Record(articles, a.now()); err != nil {
			a.Log.Warn("could not record what this brief covered", "error", err)
		}
	}

	a.Log.Info("delivered", "messages", len(messages), "chat", prefs.ChatID)
	return nil
}

// RunScheduler fires the daily brief. It recomputes the next run each time
// rather than ticking on a fixed interval, so the schedule stays anchored to
// the wall clock in the configured timezone across a DST transition.
func (a *App) RunScheduler(ctx context.Context) error {
	for {
		next := a.Cfg.NextRun(a.now())
		wait := next.Sub(a.now())
		a.Log.Info("next brief scheduled",
			"at", next.In(a.Cfg.DisplayLocation).Format(time.RFC1123),
			"in", wait.Round(time.Minute))

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		if err := a.SendReport(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A failed brief must not stop tomorrow's.
			a.Log.Error("scheduled brief failed", "error", err)
		}
	}
}

// Serve runs the scheduler and the bot together until the context is cancelled
// or one of them fails.
func (a *App) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Publishing the menu is what makes the commands discoverable: without it,
	// typing "/" in the chat offers nothing and they may as well not exist.
	if err := a.Bot.SetMyCommands(ctx, BotCommands()); err != nil {
		a.Log.Warn("could not publish the command menu", "error", err)
	}

	if n, err := a.Bot.DrainUpdates(ctx); err != nil {
		a.Log.Warn("could not clear pending updates", "error", err)
	} else if n > 0 {
		// Commands sent while the process was down are stale by the time it
		// returns; replaying them would surprise the reader.
		a.Log.Info("discarded updates queued while offline", "count", n)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		errs <- a.RunScheduler(ctx)
	}()
	go func() {
		defer wg.Done()
		errs <- a.Bot.Poll(ctx, a.Log, a.HandleMessage)
	}()

	err := <-errs
	cancel()
	wg.Wait()

	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// articleCount is a small helper for the command replies.
func groupSummary(g model.Group) string {
	return fmt.Sprintf("%d tickers, %d names, %d keywords",
		len(g.Tickers), len(g.Names), len(g.Keywords))
}

// sourceMode maps the configured SOURCE_LINKS value onto the renderer's mode.
// An unknown value cannot arrive here -- configuration rejects it at startup --
// so the fallback is the usual short list rather than an error.
func sourceMode(links string) telegram.SourceMode {
	switch links {
	case config.SourceLinksFull:
		return telegram.SourcesFull
	case config.SourceLinksOff:
		return telegram.SourcesOff
	default:
		return telegram.SourcesShort
	}
}
