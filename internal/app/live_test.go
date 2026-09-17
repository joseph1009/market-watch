package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// TestLiveAnalysisSample sends an analysis written elsewhere through the real
// /analyse delivery: the same filings, price, trading history and news, the
// same rendering and ticker checks, the same chat. Only the model's reply is
// read from a file, so it shows what an analysis looks like on a phone without
// spending anything on the API.
//
//	MARKET_WATCH_SAMPLE=analysis.txt FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestLiveAnalysisSample -v
//
// The file is the model's reply as the bot would receive it: plain text,
// capitalised section headings, "- " bullets, and the related companies as
// name|ticker|exchange|why lines under COMPANIES TO READ NEXT TO IT.
func TestLiveAnalysisSample(t *testing.T) {
	path := os.Getenv("MARKET_WATCH_SAMPLE")
	if path == "" {
		t.Skip("set MARKET_WATCH_SAMPLE to a written analysis to send it to the chat")
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	// The test runs from this package's directory, where the default data
	// directory would be a new and empty one with no chat registered.
	if os.Getenv("DATA_DIR") == "" {
		data, _ := filepath.Abs("../../data")
		t.Setenv("DATA_DIR", data)
	}
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "MU"
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	log := slog.New(logging.New(slog.NewTextHandler(os.Stderr, nil), cfg.TelegramBotToken, cfg.AnthropicAPIKey))
	service, err := New(cfg, log)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	service.Agent = nil
	service.Analyzer = &fundamentals.Analyzer{Completer: written(text)}

	chat := service.Prefs().ChatID
	if chat == 0 {
		t.Fatal("no chat registered; send /start to the bot first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := service.handleAnalyse(ctx, telegram.Message{Chat: telegram.Chat{ID: chat}}, []string{ticker}); err != nil {
		t.Fatalf("analyse: %v", err)
	}
}

// written stands in for the model with a reply that already exists. It names
// no model, so the cost shown with the analysis is nothing, which is true.
type written []byte

func (w written) Complete(context.Context, string, string) (report.Completion, error) {
	return report.Completion{Text: string(w)}, nil
}
