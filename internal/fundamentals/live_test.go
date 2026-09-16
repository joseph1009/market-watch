package fundamentals

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/sec"
)

// TestLiveFundamentals reads one company's real filings.
//
//	MARKET_WATCH_LIVE=1 FUNDAMENTALS_TICKER=NVDA go test ./internal/fundamentals -run TestLiveFundamentals -v
//
// With MARKET_WATCH_LIVE_LLM=1 it also writes the analysis, which costs money.
func TestLiveFundamentals(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" && os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to read real filings")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "NVDA"
	}
	agent := os.Getenv("USER_AGENT")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := &Client{
		Lookup:    &sec.Client{HTTP: &http.Client{Timeout: 30 * time.Second}, UserAgent: agent},
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: agent,
	}

	started := time.Now()
	snap, err := client.Fetch(ctx, ticker, 5)
	if err != nil {
		t.Fatalf("Fetch %s: %v", ticker, err)
	}
	t.Logf("read %s in %s", ticker, time.Since(started).Round(time.Millisecond))
	t.Logf("\n%s", snap.Table())

	if len(snap.Years) == 0 {
		t.Error("no fiscal years came back")
	}
	if snap.Balance.AsOf.IsZero() {
		t.Error("no balance sheet date came back")
	}

	if os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Log("set MARKET_WATCH_LIVE_LLM=1 to also write the analysis")
		return
	}

	analyzer := &Analyzer{Completer: report.NewClaude(os.Getenv("ANTHROPIC_API_KEY"), config.DefaultModel)}
	started = time.Now()
	analysis, err := analyzer.Analyze(ctx, snap)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	t.Logf("analysed in %s: %d in, %d out, estimated $%.4f",
		time.Since(started).Round(time.Second),
		analysis.Usage.InputTokens, analysis.Usage.OutputTokens, analysis.Usage.EstimatedUSD)
	t.Logf("\n%s", analysis.Text)
}

// TestLiveAgent is the agentic path: the model chooses what to look up.
//
//	MARKET_WATCH_LIVE_LLM=1 FUNDAMENTALS_TICKER=NVDA go test ./internal/fundamentals -run TestLiveAgent -v -timeout 15m
func TestLiveAgent(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE_LLM") == "" {
		t.Skip("set MARKET_WATCH_LIVE_LLM=1 to run the agent (this calls the paid API)")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	ticker := os.Getenv("FUNDAMENTALS_TICKER")
	if ticker == "" {
		ticker = "NVDA"
	}
	agentUA := os.Getenv("USER_AGENT")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	reader := &Client{
		Lookup:    &sec.Client{HTTP: &http.Client{Timeout: 60 * time.Second}, UserAgent: agentUA},
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		UserAgent: agentUA,
	}
	snap, err := reader.Fetch(ctx, ticker, 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	agent := NewAgent(os.Getenv("ANTHROPIC_API_KEY"), config.DefaultModel, reader)
	agent.Log = func(format string, args ...any) { t.Logf("  lookup: "+format, args...) }

	started := time.Now()
	analysis, err := agent.Analyze(ctx, snap)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	t.Logf("wrote %s in %s: %d in, %d out, estimated $%.4f",
		ticker, time.Since(started).Round(time.Second),
		analysis.Usage.InputTokens, analysis.Usage.OutputTokens, analysis.Usage.EstimatedUSD)
	t.Logf("\n%s", analysis.Text)
}
