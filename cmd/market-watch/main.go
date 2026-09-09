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
	"syscall"
	"time"

	"github.com/joseph1009/market-watch/internal/app"
	"github.com/joseph1009/market-watch/internal/config"
)

func main() {
	once := flag.Bool("once", false, "send one brief immediately and exit, instead of running the schedule")
	check := flag.Bool("check", false, "verify configuration and credentials, then exit without sending anything")
	flag.Parse()

	if err := run(*once, *check); err != nil {
		// The logger may not exist yet when configuration is what failed.
		fmt.Fprintln(os.Stderr, "market-watch:", err)
		os.Exit(1)
	}
}

func run(once, check bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
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
	case once:
		log.Info("sending one brief and exiting")
		return service.SendReport(ctx)
	}

	log.Info("market-watch starting",
		"model", cfg.Model,
		"schedule", cfg.ReportAt.String()+" "+cfg.ScheduleLocation.String(),
		"display_tz", cfg.DisplayLocation.String(),
		"data_dir", cfg.DataDir,
		"max_articles", cfg.MaxArticles)

	if err := service.Serve(ctx); err != nil {
		return err
	}
	log.Info("market-watch stopped")
	return nil
}

// runCheck proves the process could do its job without spending anything: it
// confirms the bot token, the delivery target and the feeds, but never calls
// the summarizer, which is the only part that costs money.
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

	sources := prefs.EnabledSources()
	articles, errs := service.Fetcher.Fetch(ctx, sources)
	for _, e := range errs {
		log.Error("source failed", "source", e.SourceID, "error", e.Err)
	}
	log.Info("feeds ok",
		"sources", len(sources),
		"failed", len(errs),
		"articles", len(articles))

	log.Info("next brief due", "at", cfg.NextRun(time.Now()).In(cfg.DisplayLocation).Format(time.RFC1123))

	if len(errs) == len(sources) {
		return fmt.Errorf("every source failed")
	}
	return nil
}
