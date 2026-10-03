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

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/calendar"
	"github.com/joseph1009/market-watch/internal/consensus"
	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/industry"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/pages"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/runcache"
	"github.com/joseph1009/market-watch/internal/search"
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
	Levels *prices.FRED

	// Search finds news by searching rather than polling, beside the feeds.
	// Disabled without a Tavily key.
	Search *search.Client

	// Quotes reads share prices, so the brief can say how the market answered
	// the news. Disabled without a Finnhub key.
	Quotes *prices.Client

	// Market reads daily price history for the analysis, and Press what has
	// been written about a company lately. Both answer the half of the question
	// the filings cannot: what the share has been doing, and what has happened
	// since the last period closed. Either being nil costs one section.
	Market *prices.History
	Press  *prices.News

	// Consensus reads what analysts expect of a company and what its
	// insiders, short sellers and funds have done, and Nasdaq's list of every
	// US listing; Movers the whole US market's daily bars, which MarketStore
	// keeps two years of. The first informs every verdict and analysis; the
	// others are where the closer look's companies are found. Without them
	// there is no closer look.
	Consensus *consensus.Client
	Movers    *prices.Massive

	// Calendar reads the week's economic releases from ForexFactory, for
	// the brief's look ahead. It needs no key.
	Calendar *calendar.ForexFactory

	// Terms are config/glossary.yaml's jargon, linked to an explanation in
	// the brief.
	Terms []model.Term

	// Pages keeps the web pages that long messages are sent as, behind a
	// summary. Nil sends everything in full, as messages.
	Pages Publisher

	// pageStore is Pages when the service serves them itself, which Serve
	// starts.
	pageStore *pages.Store

	MarketStore *market.Store

	// Runs records what each brief cost and did, so the numbers that only ever
	// reached a log can be read back with /stats.
	Runs *history.Runs

	// Covered remembers which stories earlier briefs carried, so today's can say
	// what is new rather than repeating them. Nil disables the check.
	Covered *history.Store

	// Triage rates and places articles before the cap, and Review checks
	// where it put the ones that will reach the brief. Nil disables either.
	Triage *triage.Triager
	Review *triage.Reviewer

	// Finder proposes companies the news is about that no watchlist tracks, and
	// Names remembers them across days. Nil on either disables the section.
	Finder *discover.Finder
	Names  *discover.Store

	// Sorter, Scout and Researcher find the week's themes and the companies
	// in them, Judge gives each company a verdict, and Themes remembers the
	// weeks. Scorecard records every verdict so /scorecard can say how they
	// have done. Nil Judge disables the closer look; nil Sorter or Researcher
	// leaves it to the day's reactions.
	Sorter     *ideas.Sorter
	Scout      *ideas.Scout
	Researcher *ideas.Researcher
	Judge      *ideas.Judge
	Themes     *ideas.ThemeLog
	Scorecard  *ideas.Scorecard

	// Accounts and Analyzer answer /accounts: reported figures from EDGAR, and
	// the writing up of them. Nil on either disables the command.
	Accounts *fundamentals.Client
	Analyzer *fundamentals.Analyzer

	// Industry explains how an industry fits together, for /industry.
	Industry *industry.Explainer

	// Relay carries every model call: sorting, writing, spotting names, and
	// the analysis. Each brief and each analysis is a run of its own, a
	// directory of requests and replies with a ledger.
	Relay *relay.Relay

	// Cache keeps what the latest brief, analysis and closer look were made
	// from and what they produced, one folder each, for reading afterwards.
	// Nil keeps nothing.
	Cache *runcache.Cache

	mu    sync.RWMutex
	prefs *config.Prefs

	// running serializes report generation. A /now arriving while the daily
	// brief is being written must wait rather than start a second run, which
	// would draw twice on the plan and interleave two deliveries.
	running sync.Mutex

	// last is the most recent brief or analysis sent to the owner, which
	// /share posts to the channel. See channel.go.
	lastMu sync.Mutex
	last   *delivery

	// plan is the Claude plan's latest standing, for /usage. See usage.go.
	plan *planWatch

	// work is /analyse and /industry running in the background. See jobs.go.
	work jobs

	// sending keeps a report sent as several messages together, so two
	// reports finishing at once do not interleave in the chat.
	sending sync.Mutex

	// asked is the question each chat was last asked and not yet answered,
	// such as /analyse's "which company?". See ask.go.
	askMu sync.Mutex
	asked map[int64]question

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
	if prefs.ChatID == 0 && cfg.TelegramMasterChatID != 0 {
		prefs.ChatID = cfg.TelegramMasterChatID
		if err := prefs.Save(cfg.PrefsPath()); err != nil {
			return nil, err
		}
	}

	// Every call to Claude Code reports the plan's standing, kept for /usage.
	plan := &planWatch{}
	rel := newRelay(cfg, log, plan.note)

	a := &App{
		Cfg:   cfg,
		Log:   log,
		Relay: rel,
		plan:  plan,
		Cache: &runcache.Cache{
			Root:    filepath.Join(cfg.DataDir, "cache"),
			Secrets: cfg.Secrets(),
			Log: func(format string, args ...any) {
				log.Warn("run cache", "detail", fmt.Sprintf(format, args...))
			},
		},
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
		Levels: &prices.FRED{
			APIKey: cfg.FREDAPIKey,
			HTTP:   &http.Client{Timeout: 20 * time.Second},
		},
		Search: &search.Client{
			APIKey: cfg.TavilyAPIKey,
			HTTP:   &http.Client{Timeout: 30 * time.Second},
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
		Calendar: &calendar.ForexFactory{
			HTTP: &http.Client{Timeout: 20 * time.Second},
		},
		Movers: &prices.Massive{
			APIKey: cfg.MassiveAPIKey,
			HTTP:   &http.Client{Timeout: 60 * time.Second},
		},
		prefs: prefs,
	}
	terms, err := config.Glossary()
	if err != nil {
		return nil, err
	}
	a.Terms = terms
	if cfg.PagesURL != "" {
		a.pageStore = &pages.Store{Dir: filepath.Join(cfg.DataDir, "pages"), BaseURL: cfg.PagesURL}
		a.Pages = a.pageStore
	}
	if cfg.Consensus {
		a.Consensus = &consensus.Client{HTTP: &http.Client{Timeout: 20 * time.Second}}
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
	a.Industry = &industry.Explainer{
		Completer: rel.Plain(relay.Industry),
		Verifier:  &discover.FIGI{HTTP: &http.Client{Timeout: 30 * time.Second}},
		Now:       a.now,
	}
	if cfg.Triage {
		memory, err := triage.LoadMemory(filepath.Join(cfg.DataDir, "sorted.json"))
		if err != nil {
			return nil, err
		}
		a.Triage = &triage.Triager{
			Completer:   rel.Plain(relay.Triage),
			Concurrency: cfg.RelayConcurrency,
			// The pass as a whole gets as long as one call may take. A batch
			// that hangs costs its articles their ratings, never the brief.
			Timeout: cfg.CallTimeout,
			Memory:  memory,
			Now:     a.now,
		}
		if cfg.RelayAnswer == config.AnswerSession {
			// Each batch is a file someone has to answer, so fewer and larger
			// is kinder than many and small.
			a.Triage.BatchSize = 150
		}
		if cfg.Review {
			a.Review = &triage.Reviewer{
				Completer:   rel.Plain(relay.Review),
				Concurrency: cfg.RelayConcurrency,
				Timeout:     cfg.CallTimeout,
			}
			if cfg.RelayAnswer == config.AnswerSession {
				a.Review.BatchSize = 150
			}
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
	a.MarketStore = &market.Store{Dir: a.marketDir()}
	if cfg.Ideas {
		a.Sorter = &ideas.Sorter{Completer: rel.Plain(relay.Themes)}
		a.Scout = &ideas.Scout{Completer: rel.Plain(relay.Scout)}
		a.Researcher = &ideas.Researcher{
			Completer: rel.Plain(relay.Research),
			Verifier:  &discover.FIGI{HTTP: &http.Client{Timeout: 30 * time.Second}},
		}
		a.Judge = &ideas.Judge{Completer: rel.Plain(relay.Verdicts), Now: a.now}
		themes, err := ideas.LoadThemeLog(filepath.Join(cfg.DataDir, "themes.json"))
		if err != nil {
			return nil, err
		}
		a.Themes = themes
	}
	// Loaded whether or not new verdicts are being made, so /scorecard still
	// reads the old ones with the section turned off.
	scorecard, err := ideas.LoadScorecard(filepath.Join(cfg.DataDir, "scorecard.json"))
	if err != nil {
		return nil, err
	}
	a.Scorecard = scorecard
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

// SendReport runs the whole pipeline and delivers the result to the owner:
// the brief, and after it the companies worth a closer look.
func (a *App) SendReport(ctx context.Context) error {
	return a.brief(ctx, 0, false, false)
}

// SendReportTo writes a brief for a chat other than the owner's, a control
// chat that sent /now, and delivers it there alone. Nothing the owner has is
// touched: their copy of the day's brief, the channel, the closer look, and
// what tomorrow's brief counts as already covered all stay as they were.
func (a *App) SendReportTo(ctx context.Context, chat int64) error {
	return a.brief(ctx, chat, false, false)
}

// Publish sends the brief to the owner, then the closer look, then both to
// the channel if there is one, as one post. It is what -once -share does. A
// channel that refuses is reported to the owner rather than returned, since
// the owner's copy arrived.
func (a *App) Publish(ctx context.Context) error {
	return a.brief(ctx, 0, true, false)
}

// publishScheduled is the daily run: Publish, and on the week's first, the
// week's themes in the closer look.
func (a *App) publishScheduled(ctx context.Context) error {
	return a.brief(ctx, 0, true, true)
}

// brief is one whole run. The owner gets the brief at once and the closer
// look after its research; the channel gets the two together, as one post,
// when the closer look is done (until 2026-10-01 the brief went at once and
// the closer look twenty minutes after it, two posts of their own). A brief
// sent to another chat (to is neither 0 nor the owner's) ends once
// delivered.
func (a *App) brief(ctx context.Context, to int64, share, scheduled bool) (err error) {
	// One report at a time, whoever asked for it.
	a.running.Lock()
	defer a.running.Unlock()

	// And nothing else while it runs: the jobs already running finish first,
	// and the rest wait for the brief. See jobs.go.
	if err := a.work.hold(ctx); err != nil {
		return err
	}
	defer a.work.release()

	if a.Prefs().ChatID == 0 {
		return ErrNoChat
	}

	ctx, cached := a.Cache.Start(ctx, runcache.Brief, "")
	defer func() { cached.Finish(err) }()

	// Every model call this brief makes lands in one directory, numbered in
	// the order it was asked, so the whole run can be read afterwards.
	if a.Relay != nil {
		var run *relay.Run
		if ctx, run, err = a.Relay.Begin(ctx, "brief"); err != nil {
			return err
		}
		a.Log.Info("relay run", "dir", run.Dir)
	}

	done, err := a.sendReport(ctx, to)
	if err != nil {
		return err
	}
	if done == nil || done.elsewhere {
		return nil // a day with no news, or another chat's: nothing to share or look into
	}
	look := a.sendIdeas(ctx, lookFrom(done.rep, scheduled))
	if share {
		a.shareBrief(ctx, done.sent, look)
	}
	return nil
}

// collectedView is the collection as the run cache keeps it: what arrived,
// what was kept and what the cap cut, with the counts beside them.
func collectedView(c feed.Result) any {
	var failed []string
	for _, e := range c.Errors {
		failed = append(failed, e.SourceID+": "+e.Err.Error())
	}
	return struct {
		Fetched      int             `json:"fetched"`
		Deduped      int             `json:"deduped"`
		KeptCount    int             `json:"kept"`
		Dropped      int             `json:"dropped"`
		Matched      int             `json:"matched"`
		Rated        int             `json:"rated"`
		Trivial      int             `json:"trivial"`
		CutImportant int             `json:"cut_rated_4_plus"`
		Failed       []string        `json:"failed_sources,omitempty"`
		TriageError  []string        `json:"triage_error,omitempty"`
		Kept         []model.Article `json:"kept_articles"`
		Cut          []model.Article `json:"cut_articles"`
		Arrived      []model.Article `json:"arrived_articles"`
	}{
		c.Fetched, c.Deduped, len(c.Articles), c.Dropped, c.Matched, c.Rated, c.Trivial, c.CutImportant,
		failed, runcache.ErrText(c.TriageErr), c.Articles, c.Cut, c.Arrived,
	}
}

// joinMessages writes the messages of one delivery into one file, in order.
func joinMessages(messages []string) string {
	return strings.Join(messages, "\n\n<!-- next message -->\n\n") + "\n"
}

// briefDone is what a delivered brief leaves for the steps after it.
type briefDone struct {
	sent *delivery
	rep  model.Report

	// elsewhere is a brief another chat asked for, which went there alone.
	elsewhere bool
}

// sendReport writes the brief and delivers it to the owner, or to the chat to
// when that is another. It returns nil, with no error, on a day with no news.
// The caller holds the run lock.
func (a *App) sendReport(ctx context.Context, to int64) (*briefDone, error) {
	prefs := a.Prefs()
	if prefs.ChatID == 0 {
		return nil, ErrNoChat
	}
	elsewhere := to != 0 && to != prefs.ChatID
	if !elsewhere {
		to = prefs.ChatID
	}

	started := a.now()
	watched := watchedTickers(prefs.Groups)

	// Prices are read first and alongside the rest: the free tier's pace makes
	// them the slowest thing gathered, and the shares that moved furthest
	// decide what else is searched for.
	priced := make(chan []model.Quote, 1)
	go func() { priced <- a.collectPrices(ctx, watched) }()

	// What is due today and this week, for the look ahead: a handful of
	// requests, read beside the prices so the brief waits on neither.
	due := make(chan model.Calendar, 1)
	go func() { due <- a.collectCalendar(ctx, watched) }()

	// The biggest moves across the whole market, from its history, for the
	// line under the overview. Topping the history up may take minutes, and
	// nothing waits on it until the brief is written.
	since := a.searchSince()
	widely := make(chan []model.MarketMove, 1)
	go func() { widely <- a.marketMoves(ctx, since) }()

	// SEC filings are gathered before the feeds so they arrive on the same
	// footing: deduped, matched and scored with everything else rather than
	// bolted on afterwards.
	filings := a.collectFilings(ctx, prefs)
	// Search results join the same way, and for the same reason.
	found := a.collectSearch(ctx, prefs)

	// A share that moved well beyond the market gets a search of its own for
	// why, so the reason reaches the brief whether or not a feed carried it.
	quotes := <-priced
	moved := movers(quotes, watched, since)
	found = search.Merge(found, a.searchMovers(ctx, moved, companyNames(prefs.Groups)))

	cached := runcache.From(ctx)
	cached.Save("prices", quotes)
	cached.Save("movers", moved)
	cached.Save("filings", filings)
	cached.Save("search", struct {
		Credits  int             `json:"credits"`
		Errors   []string        `json:"errors,omitempty"`
		Articles []model.Article `json:"articles"`
	}{found.Credits, runcache.ErrText(found.Errors...), found.Articles})

	sources := append(prefs.EnabledSources(), SECSourceEntry())
	opts := feed.Options{
		Sources: append(sources, search.Sources()...),
		Groups:  prefs.Groups,
		Max:     a.Cfg.MaxArticles,
		Extra:   append(filings, found.Articles...),
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
	cached.Save("collected", collectedView(collected))
	// match_rate is the number a change to the companies followed moves most
	// directly, and the one that was invisible while the lists were tuned.
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
			"from_memory", a.Triage.Memory.Recalled(),
			"newly_matched", collected.NewlyMatched,
			"placements_added", collected.Placements,
			"trivial_removed", collected.Trivial,
			"cut_rated_4_plus", collected.CutImportant,
			"input_tokens", collected.TriageUsage.InputTokens,
			"output_tokens", collected.TriageUsage.OutputTokens)
	}

	if collected.AllFailed() {
		return nil, fmt.Errorf("every source failed (%d): %v", len(collected.Errors), collected.Errors[0])
	}

	// Stories earlier briefs already carried are marked rather than dropped: a
	// running story should be reported when it moves, and silently removing it
	// would leave the reader with a development and no thread to hang it on.
	// Marked before the review, so a thin section is not topped up with one.
	if a.Covered != nil {
		repeats := a.Covered.Seen(collected.Articles)
		collected.Articles = a.Covered.Mark(collected.Articles)
		a.Log.Info("previously covered", "articles", repeats, "remembered", a.Covered.Len())
	}

	// The review looks again at where everything that will reach the brief was
	// placed. Like the sorting, it improves the brief and is not needed for
	// one: where it fails, the sorting's placements stand.
	//
	// Before it, a section short of news is filled from what the sorting rated
	// a point too low to place, and the review keeps only the ones it
	// confirms. Only with the review: unchecked, that is the padding
	// MinPlacementRating was set to keep out.
	var (
		moves       []triage.Move
		reviewUsage model.Usage
		toppedUp    int
		keptTopUps  []feed.Placement
	)
	if a.Review != nil {
		collected.Articles, toppedUp = triage.TopUp(collected.Articles, prefs.Groups, triage.ThinSection)
		reviewed, moved, usage, err := a.Review.Review(ctx, collected.Articles, prefs.Groups)
		if err != nil {
			a.Log.Warn("review incomplete; the sorting's placements stand where it failed", "error", err)
		}
		collected.Articles, moves, reviewUsage = reviewed, moved, usage
		keptTopUps = topUps(collected.Articles)
		a.Log.Info("reviewed",
			"moved", len(moves),
			"topped_up", toppedUp,
			"top_ups_kept", len(keptTopUps),
			"input_tokens", usage.InputTokens,
			"output_tokens", usage.OutputTokens)
	}

	articles := collected.Articles
	cached.Save("review", moves)
	cached.Save("articles", articles)

	// Market levels and prices are context, not content: a failure here costs
	// the anchor numbers, never the brief.
	a.Generator.Levels = a.collectLevels(ctx)
	a.Generator.Quotes = quotes
	a.Generator.Trends = a.trendsFor(ctx, moved)
	a.Generator.MovesSince = since
	a.Generator.Calendar = <-due
	a.Generator.MarketMoves = <-widely
	cached.Save("calendar", a.Generator.Calendar)
	cached.Save("market_moves", a.Generator.MarketMoves)
	cached.Save("levels", a.Generator.Levels)
	cached.Save("trends", a.Generator.Trends)

	rep, err := a.Generator.Generate(ctx, articles, prefs.Groups)
	if errors.Is(err, report.ErrNoArticles) {
		// A genuinely empty day is worth saying out loud, rather than leaving
		// the reader wondering whether the service died.
		return nil, a.Bot.SendMessage(ctx, to,
			"<b>📊 Market Watch</b>\nNo news was collected today. The feeds returned nothing usable.")
	}
	if err != nil {
		return nil, err
	}
	rep.Triage = collected.TriageUsage
	rep.Triage.InputTokens += reviewUsage.InputTokens
	rep.Triage.OutputTokens += reviewUsage.OutputTokens

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

	layout := telegram.Options{
		Display: a.Cfg.DisplayLocation,
		Sources: sourceMode(a.Cfg.SourceLinks),
		Terms:   a.Terms,
	}
	messages := telegram.RenderWith(rep, layout)
	day := rep.GeneratedAt.In(a.Cfg.DisplayLocation).Format("Mon 2 Jan")
	doc := telegram.BriefDoc(rep, pageMarket(a.Generator.Levels, quotes), layout)
	out := outgoing{title: "Market Watch · " + day, messages: messages, summary: telegram.BriefSummary(rep, layout), doc: &doc}
	cached.Save("report", rep)
	cached.Text("messages.html", joinMessages(messages))
	cached.Text("summary.html", out.summary.Text)

	if elsewhere {
		if _, err := a.send(ctx, to, out, false); err != nil {
			return nil, err
		}
		a.Log.Info("delivered", "messages", len(messages), "chat", to)
		return &briefDone{rep: rep, elsewhere: true}, nil
	}

	// Clearing happens after generation, not before: a run that fails to
	// produce a brief must not also have thrown away the last one.
	if a.Cfg.ReplacePrevious && len(prefs.LastBrief) > 0 {
		deleted, failed := a.Bot.DeleteMessages(ctx, prefs.ChatID, prefs.LastBrief)
		a.Log.Info("cleared previous brief", "deleted", deleted, "unavailable", failed)
	}

	ids, err := a.send(ctx, prefs.ChatID, out, false)
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
		return nil, err
	}
	sent := a.rememberSent("the brief of "+day, out, nil)

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
		run.Moved = len(moves)
		run.MovedExamples = movedExamples(moves, prefs.Groups)
		run.ToppedUp, run.TopUpsKept = toppedUp, len(keptTopUps)
		run.TopUpExamples = placedExamples(keptTopUps[:min(len(keptTopUps), maxMovedExamples)], prefs.Groups)
		for _, e := range collected.Errors {
			run.Failed = append(run.Failed, e.SourceID)
		}
		if a.Search.Enabled() {
			recordSearch(&run, found, collected.Articles, rep.Referenced())
		}
		if err := a.Runs.Add(run); err != nil {
			a.Log.Warn("could not record the run", "error", err)
		}
	}

	a.Log.Info("delivered", "messages", len(messages), "chat", prefs.ChatID)
	return &briefDone{sent: sent, rep: rep}, nil
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

		// Only the scheduled brief goes to the channel by itself. One asked for
		// with /now is usually a check, and waits for /share.
		if err := a.publishScheduled(ctx); err != nil {
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

	errs := make(chan error, 5)
	var wg sync.WaitGroup
	wg.Add(3)
	if a.pageStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Log.Info("serving pages", "addr", a.Cfg.PagesAddr, "url", a.Cfg.PagesURL)
			errs <- a.pageStore.Serve(ctx, a.Cfg.PagesAddr)
		}()
	}

	go func() {
		defer wg.Done()
		errs <- a.RunScheduler(ctx)
	}()
	go func() {
		defer wg.Done()
		errs <- a.RunMarket(ctx)
	}()
	go func() {
		defer wg.Done()
		errs <- a.Bot.Poll(ctx, a.Log, a.HandleMessage)
	}()

	err := <-errs
	cancel()
	wg.Wait()
	a.work.wait() // the jobs end once the context is cancelled

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
// name. A followed company is not a discovery: its section already covers it.
func watchedNames(groups []model.Group) []string {
	var out []string
	for _, g := range groups {
		for _, c := range g.Companies {
			if c.Symbol != "" {
				out = append(out, c.Symbol)
			}
			out = append(out, c.Names()...)
		}
	}
	return out
}

// companyNames maps each followed ticker to the name headlines use for it.
func companyNames(groups []model.Group) map[string]string {
	out := map[string]string{}
	for _, g := range groups {
		for _, c := range g.Companies {
			if c.Symbol != "" {
				out[c.Symbol] = c.Name
			}
		}
	}
	return out
}

// newRelay builds the relay the service's model calls go through, answered as
// the configuration says: by Claude Code headless, or by a person.
func newRelay(cfg *config.Config, log *slog.Logger, onLimits func(relay.Limits)) *relay.Relay {
	note := func(format string, args ...any) {
		log.Info("relay", "detail", fmt.Sprintf(format, args...))
	}
	var answer relay.Answerer = relay.Claude{
		Bin:      cfg.ClaudeBin,
		Models:   cfg.StageModels,
		Timeout:  cfg.CallTimeout,
		OnLimits: onLimits,
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

// topUps are the articles a thin section was filled with and the review
// kept, as placements, for /stats.
func topUps(articles []model.Article) []feed.Placement {
	var out []feed.Placement
	for _, a := range articles {
		if a.ToppedUp {
			out = append(out, feed.Placement{Title: a.Title, Rating: a.Rating, Groups: a.GroupIDs})
		}
	}
	return out
}

// movedExamples writes a few of the review's moves as "headline: from → to",
// for /stats. Five at most: they are there to be read, to judge whether the
// review is catching misplacements or inventing them.
func movedExamples(moves []triage.Move, groups []model.Group) []string {
	names := make(map[string]string, len(groups))
	for _, g := range groups {
		names[g.ID] = g.Name
	}
	sectors := func(ids []string) string {
		if len(ids) == 0 {
			return "none"
		}
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = id
			if name, ok := names[id]; ok {
				out[i] = name
			}
		}
		return strings.Join(out, ", ")
	}
	var out []string
	for _, m := range moves {
		if len(out) == maxMovedExamples {
			break
		}
		out = append(out, fmt.Sprintf("%s: %s → %s",
			clipRunes(m.Title, exampleTitleRunes), sectors(m.From), sectors(m.To)))
	}
	return out
}

// maxMovedExamples is how many of the review's moves a run keeps.
const maxMovedExamples = 5

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
	clean := logging.Scrub(cause.Error(), a.Cfg.Secrets()...)
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
