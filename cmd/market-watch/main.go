// Command market-watch runs the service: a daily market brief pushed over
// Telegram, plus a bot that answers commands in between.
//
// It is a long-running process, not a scheduled job. The bot needs long
// polling, which means holding a connection open, so there is no request to
// schedule and nothing for a cron runner to invoke.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/app"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/relay"
)

func main() {
	once := flag.Bool("once", false, "send one brief immediately and exit, instead of running the schedule")
	share := flag.Bool("share", false, "with -once, also post the brief to the channel, as the daily one is")
	check := flag.Bool("check", false, "verify configuration and credentials, then exit without sending anything")
	clear := flag.Bool("clear", false, "delete the bot's earlier messages from the chat, then exit")
	fold := flag.Bool("fold", false, "write the watchlist and feed changes made from Telegram, as synced into DATA_DIR, into config/, then exit")
	flag.Parse()

	if *fold {
		if err := runFold(); err != nil {
			fmt.Fprintln(os.Stderr, "market-watch:", err)
			os.Exit(1)
		}
		return
	}

	if *share && !*once {
		fmt.Fprintln(os.Stderr, "market-watch: -share only goes with -once")
		os.Exit(2)
	}

	if err := run(*once, *share, *check, *clear); err != nil {
		// The logger may not exist yet when configuration is what failed, so
		// this path scrubs from the environment directly rather than relying on
		// the handler.
		fmt.Fprintln(os.Stderr, "market-watch:", logging.Scrub(err.Error(),
			os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")))
		os.Exit(1)
	}
}

func run(once, share, check, clear bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// A prompts file that has lost a section stops the process here. The
	// alternative is a brief that reads perfectly and cannot be split into
	// messages, hours later.
	if err := config.LoadPrompts(); err != nil {
		return err
	}

	// Every log line passes through the scrubber. Redacting at each call site
	// would mean finding every call site, and the leak that prompted this was
	// one nobody had thought of: net/http puts the request URL into connection
	// errors, and the bot token lives in that URL.
	secrets := []string{cfg.TelegramBotToken, cfg.ClaudeToken}
	log := slog.New(logging.New(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}),
		secrets...,
	))
	slog.SetDefault(log)

	service, err := app.New(cfg, log)
	if err != nil {
		return err
	}

	// SIGTERM is how a container asks to stop. Cancelling the context unwinds
	// the poll and the scheduler, so a report in flight finishes its delivery
	// rather than being cut mid-way.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case check:
		return runCheck(ctx, service, cfg, log)
	case clear:
		return runClear(ctx, service, log)
	case once && share:
		if cfg.TelegramChannelID == 0 {
			return fmt.Errorf("-share: no channel is set; set TELEGRAM_CHANNEL_ID")
		}
		log.Info("sending one brief to you and the channel, then exiting")
		return service.Publish(ctx)
	case once:
		log.Info("sending one brief and exiting")
		return service.SendReport(ctx)
	}

	log.Info("market-watch starting",
		"answer", cfg.RelayAnswer,
		"models", stageModels(cfg),
		"relay_dir", cfg.RelayDir,
		"schedule", cfg.ReportAt.String()+" "+cfg.ScheduleLocation.String(),
		"display_tz", cfg.DisplayLocation.String(),
		"data_dir", cfg.DataDir,
		"max_articles", cfg.MaxArticles,
		"channel", cfg.TelegramChannelID)

	if err := service.Serve(ctx); err != nil {
		return err
	}
	log.Info("market-watch stopped")
	return nil
}

// runFold writes the changes made from Telegram -- companies added or removed
// with /watchlist, feeds switched with /sources -- into the files in config/,
// so the files say them and nobody has to copy them over by hand.
//
// It reads them from the local data directory, which scripts/sync-from-fly.sh
// has just filled from the server. It needs no credentials and sends nothing,
// so it runs before configuration is loaded: the files it writes are the
// working copies in this checkout, to be read over, committed and deployed.
// After the deploy, the service finds the files saying what its edits said and
// drops the edits.
func runFold() error {
	if err := config.LoadDotEnv(config.DefaultEnvFile); err != nil {
		return err
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = config.DefaultDataDir
	}
	prefs, err := config.LoadPrefs(filepath.Join(dataDir, "prefs.yaml"))
	if err != nil {
		return err
	}
	changes, err := config.Fold("config", *prefs)
	for _, c := range changes {
		fmt.Println(c)
	}
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Println("Nothing to write: config/ already says everything changed from Telegram.")
	}
	return nil
}

// runClear removes the bot's earlier messages without needing the service to
// be running, which is the state it is usually in while iterating.
func runClear(ctx context.Context, service *app.App, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	prefs := service.Prefs()
	if prefs.ChatID == 0 {
		return fmt.Errorf("no chat registered; send /start to the bot first")
	}
	return service.ClearChat(ctx, prefs.ChatID)
}

// stageModels reads which model answers each stage, for the startup line: the
// one place that says so before the first brief does.
func stageModels(cfg *config.Config) string {
	c := relay.Claude{Models: cfg.StageModels}
	stages := []string{relay.Triage, relay.Brief, relay.Names, relay.Analysis}
	if cfg.Ideas {
		stages = append(stages, relay.Ideas, relay.Verdicts)
	}
	var parts []string
	for _, stage := range stages {
		parts = append(parts, stage+"="+c.ModelFor(stage))
	}
	return strings.Join(parts, " ")
}

// runCheck proves the process could do its job without spending anything: it
// confirms the bot token, the delivery target, the feeds, the search key and
// that Claude Code is there to answer, but never asks a model anything or
// runs a search, the two parts that draw on an allowance.
func runCheck(ctx context.Context, service *app.App, cfg *config.Config, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	username, err := service.Bot.Me(ctx)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	log.Info("telegram ok", "bot", "@"+username)

	prefs := service.Prefs()
	if prefs.ChatID == 0 {
		log.Warn("no chat registered yet; send /start to the bot")
	} else {
		log.Info("delivery target", "chat", prefs.ChatID)
	}

	// The channel is optional, so a problem with it is a warning rather than a
	// failed check: the owner's brief does not depend on it.
	if channel := cfg.TelegramChannelID; channel != 0 {
		title, ok, err := service.Bot.CanPost(ctx, channel)
		switch {
		case err != nil:
			log.Warn("channel unreachable; is the bot a member, and is TELEGRAM_CHANNEL_ID right?", "channel", channel, "error", err)
		case !ok:
			log.Warn("the bot cannot post to the channel; make it an admin with the right to post", "channel", channel, "title", title)
		default:
			log.Info("channel ok", "channel", channel, "title", title)
		}
	}

	sources := prefs.EnabledSources()
	articles, errs := service.Fetcher.Fetch(ctx, sources)
	for _, e := range errs {
		log.Error("source failed", "source", e.SourceID, "error", e.Err)
	}
	log.Info("feeds ok",
		"sources", len(sources),
		"failed", len(errs),
		"articles", len(articles))

	// Search is optional, so a problem with it is a warning. The usage lookup
	// proves the key without spending a credit. Tavily's count of credits used
	// runs late; /stats has the count from each search's own reply.
	if service.Search.Enabled() {
		usage, err := service.Search.Usage(ctx)
		if err != nil {
			log.Warn("news search unavailable; the brief will use the feeds alone", "error", err)
		} else {
			log.Info("search ok", "plan", usage.Plan, "credits_used", usage.Used, "credits_limit", usage.Limit)
		}
	}

	log.Info("next brief due", "at", cfg.NextRun(time.Now()).In(cfg.DisplayLocation).Format(time.RFC1123))

	if cfg.RelayAnswer == config.AnswerClaude {
		version, err := relay.Claude{Bin: cfg.ClaudeBin}.Version(ctx)
		if err != nil {
			return err
		}
		// Whether it can also log in is only known once it is asked something,
		// which is the one thing a check does not do. What can be said is which
		// credential it will reach for.
		login := "the login saved by /login on this machine"
		if cfg.ClaudeToken != "" {
			login = "CLAUDE_CODE_OAUTH_TOKEN"
		}
		log.Info("claude ok", "version", version, "credential", login, "models", stageModels(cfg))
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			log.Warn("ANTHROPIC_API_KEY is set; it is withheld from Claude Code so every call stays on the subscription, and can be removed")
		}
	}

	if len(errs) == len(sources) {
		return fmt.Errorf("every source failed")
	}
	return nil
}
