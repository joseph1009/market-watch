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
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/marketdata"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/relay"
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

	// Quotes reads share prices, so the brief can say how the market answered
	// the news. Disabled without a Finnhub key.
	Quotes *prices.Client

	// Market reads daily price history for the analysis, and Press what has
	// been written about a company lately. Both answer the half of the question
	// the filings cannot: what the share has been doing, and what has happened
	// since the last period closed. Either being nil costs one section.
	Market *prices.History
	Press  *prices.News

	// Runs records what each brief cost and did, so the numbers that only ever
	// reached a log can be read back with /stats.
	Runs *history.Runs

	// Covered remembers which stories earlier briefs carried, so today's can say
	// what is new rather than repeating them. Nil disables the check.
	Covered *history.Store

	// Triage rates and places articles before the cap. Nil disables it.
	Triage *triage.Triager

	// Finder proposes companies the news is about that no watchlist tracks, and
	// Names remembers them across days. Nil on either disables the section.
	Finder *discover.Finder
	Names  *discover.Store

	// Accounts and Analyzer answer /accounts: reported figures from EDGAR, and
	// the writing up of them. Nil on either disables the command.
	Accounts *fundamentals.Client
	Analyzer *fundamentals.Analyzer

	// Relay carries every model call: sorting, writing, spotting names, and
	// the analysis. Each brief and each analysis is a run of its own, a
	// directory of requests and replies with a ledger.
	Relay *relay.Relay

	mu    sync.RWMutex
	prefs *config.Prefs

	// running serializes report generation. A /now arriving while the daily
	// brief is being written must wait rather than start a second run, which
	// would draw twice on the plan and interleave two deliveries.
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

	rel := newRelay(cfg, log)

	a := &App{
		Cfg:   cfg,
		Log:   log,
		Relay: rel,
		Fetcher: &feed.Fetcher{
			Client:    &http.Client{Timeout: cfg.HTTPTimeout},
			UserAgent: cfg.UserAgent,
		},
		Generator: &report.Generator{
			Completer:       rel.Stage(relay.Brief),
			DisplayLocation: cfg.DisplayLocation,
		},
		// Polling holds a request open for 30s, so this client must outlast it.
		Bot: telegram.New(cfg.TelegramBotToken, &http.Client{Timeout: 90 * time.Second}),
		Levels: &marketdata.Client{
			APIKey: cfg.FREDAPIKey,
			HTTP:   &http.Client{Timeout: 20 * time.Second},
		},
		Quotes: &prices.Client{
			APIKey: cfg.FinnhubAPIKey,
			HTTP:   &http.Client{Timeout: 20 * time.Second},
		},
		Market: &prices.History{
			HTTP: &http.Client{Timeout: 20 * time.Second},
		},
		Press: &prices.News{
			APIKey: cfg.FinnhubAPIKey,
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

	runs, err := history.LoadRuns(filepath.Join(cfg.DataDir, "runs.json"))
	if err != nil {
		return nil, err
	}
	a.Runs = runs

	a.Accounts = &fundamentals.Client{
		Lookup:    a.Filings,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: cfg.UserAgent,
	}
	a.Analyzer = &fundamentals.Analyzer{Completer: rel.Stage(relay.Analysis)}
	if cfg.Triage {
		a.Triage = &triage.Triager{
			Completer:   rel.Plain(relay.Triage),
			Concurrency: cfg.RelayConcurrency,
			// The pass as a whole gets as long as one call may take. A batch
			// that hangs costs its articles their ratings, never the brief.
			Timeout: cfg.CallTimeout,
		}
		if cfg.RelayAnswer == config.AnswerSession {
			// Each batch is a file someone has to answer, so fewer and larger
			// is kinder than many and small.
			a.Triage.BatchSize = 150
		}
	}
	if cfg.Discover {
		a.Finder = &discover.Finder{
			Completer: rel.Plain(relay.Names),
			Verifier:  &discover.FIGI{HTTP: &http.Client{Timeout: 30 * time.Second}},
		}
		names, err := discover.LoadStore(filepath.Join(cfg.DataDir, "candidates.json"))
		if err != nil {
			return nil, err
		}
		a.Names = names
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

	// Every model call this brief makes lands in one directory, numbered in
	// the order it was asked, so the whole run can be read afterwards.
	if a.Relay != nil {
		var run *relay.Run
		var err error
		if ctx, run, err = a.Relay.Begin(ctx, "brief"); err != nil {
			return err
		}
		a.Log.Info("relay run", "dir", run.Dir)
	}

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
			"input_tokens", collected.TriageUsage.InputTokens,
			"output_tokens", collected.TriageUsage.OutputTokens)
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

	// Market levels and prices are context, not content: a failure here costs
	// the anchor numbers, never the brief.
	a.Generator.Levels = a.collectLevels(ctx)
	a.Generator.Quotes = a.collectQuotes(ctx, articles, watchedTickers(prefs.Groups))

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

	// New names are looked for after the brief is written, and a failure only
	// costs the section: the brief is the product, and this is an addition to
	// it. Its size joins the triage line in the footer, being the same small
	// model doing the same kind of work.
	if a.Finder != nil {
		candidates, usage, err := a.Finder.Find(ctx, articles, watchedNames(prefs.Groups))
		rep.Triage.InputTokens += usage.InputTokens
		rep.Triage.OutputTokens += usage.OutputTokens

		switch {
		case err != nil:
			a.Log.Warn("could not look for new names", "error", err)
		default:
			if a.Names != nil {
				candidates = a.Names.Note(candidates, a.now())
				if err := a.Names.Save(); err != nil {
					a.Log.Warn("could not record the new names", "error", err)
				}
			}
			rep.Candidates = a.priceCandidates(ctx, candidates)
			a.Log.Info("new names",
				"found", len(candidates),
				"remembered", namesRemembered(a.Names),
				"input_tokens", usage.InputTokens,
				"output_tokens", usage.OutputTokens)
		}
	}

	a.Log.Info("generated",
		"sections", len(rep.Sections),
		"input_tokens", rep.Usage.InputTokens,
		"output_tokens", rep.Usage.OutputTokens,
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

	if a.Runs != nil {
		run := history.Run{
			At:           a.now(),
			Fetched:      collected.Fetched,
			Deduped:      collected.Deduped,
			Kept:         len(collected.Articles),
			Matched:      collected.Matched,
			Dropped:      collected.Dropped,
			Trivial:      collected.Trivial,
			CutImportant: collected.CutImportant,
			Placed:       collected.NewlyMatched,
			Sections:     len(rep.Sections),
			Messages:     len(messages),
			NewNames:     len(rep.Candidates),
		}
		if a.Covered != nil {
			run.Repeats = a.Covered.Seen(collected.Articles)
		}
		run.PlacedExamples = placedExamples(collected.Placed, prefs.Groups)
		for _, e := range collected.Errors {
			run.Failed = append(run.Failed, e.SourceID)
		}
		if err := a.Runs.Add(run); err != nil {
			a.Log.Warn("could not record the run", "error", err)
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
			a.reportFailure(ctx, err)
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

// watchedNames is every company the reader already tracks, by ticker and by
// name. A watchlist name is not a discovery: its section already covers it.
func watchedNames(groups []model.Group) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g.Tickers...)
		out = append(out, g.Names...)
	}
	return out
}

// newRelay builds the relay the service's model calls go through, answered as
// the configuration says: by Claude Code headless, or by a person.
func newRelay(cfg *config.Config, log *slog.Logger) *relay.Relay {
	note := func(format string, args ...any) {
		log.Info("relay", "detail", fmt.Sprintf(format, args...))
	}
	var answer relay.Answerer = relay.Claude{
		Bin:     cfg.ClaudeBin,
		Models:  cfg.StageModels,
		Timeout: cfg.CallTimeout,
	}
	if cfg.RelayAnswer == config.AnswerSession {
		answer = relay.Session{Log: note}
	}
	return &relay.Relay{Root: cfg.RelayDir, Answer: answer, Log: note}
}

// exampleTitleRunes keeps an example to about one line on a phone.
const exampleTitleRunes = 90

// placedExamples writes the sampled placements as "headline → sector", which is
// what /stats shows.
//
// Watchlists are named rather than given by id, because this is read by a
// person deciding whether the sector descriptions are working, and "Energy"
// answers that faster than "energy" did as an id.
func placedExamples(placed []feed.Placement, groups []model.Group) []string {
	if len(placed) == 0 {
		return nil
	}

	names := make(map[string]string, len(groups))
	for _, g := range groups {
		names[g.ID] = g.Name
	}

	out := make([]string, 0, len(placed))
	for _, p := range placed {
		sectors := make([]string, 0, len(p.Groups))
		for _, id := range p.Groups {
			if name, ok := names[id]; ok {
				sectors = append(sectors, name)
				continue
			}
			sectors = append(sectors, id) // a watchlist deleted since the run
		}
		out = append(out, fmt.Sprintf("%s → %s",
			clipRunes(p.Title, exampleTitleRunes), strings.Join(sectors, ", ")))
	}
	return out
}

func clipRunes(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func namesRemembered(s *discover.Store) int {
	if s == nil {
		return 0
	}
	return s.Len()
}

// reportFailure tells the reader the brief did not arrive.
//
// Silence is the worst outcome: a missing brief is indistinguishable from a
// quiet news day, and the reader would find out only by noticing the absence
// days later. The message says what broke and when the next attempt is, so
// nothing has to be inferred.
//
// The failure is not retried. Whatever stopped it -- an outage, a bad key, a
// model refusal -- is unlikely to clear inside a few minutes, and a retry loop
// spends real money on the same error.
func (a *App) reportFailure(ctx context.Context, cause error) {
	chat := a.Prefs().ChatID
	if chat == 0 {
		return
	}

	// The error is the other route a credential can take out of the process, so
	// it is scrubbed exactly as the log is.
	clean := logging.Scrub(cause.Error(), a.Cfg.TelegramBotToken, a.Cfg.ClaudeToken)
	next := a.Cfg.NextRun(a.now()).In(a.Cfg.DisplayLocation)

	text := fmt.Sprintf(
		"<b>📊 Market Watch</b>\n\nToday's brief failed and was not sent.\n\n<i>%s</i>\n\nNothing is retried automatically. The next scheduled brief is %s; send /now to try again sooner.",
		escape(clean), escape(next.Format("Mon 2 Jan at 15:04 MST")))

	if err := a.Bot.SendMessage(ctx, chat, text); err != nil {
		// Nothing further to try: if Telegram is what broke, the logs are the
		// only channel left.
		a.Log.Error("could not report the failure either", "error", err)
	}
}
