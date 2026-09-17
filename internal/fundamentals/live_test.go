package fundamentals

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/sec"
)

// TestLiveFundamentals reads one company's real filings.
//
//	MARKET_WATCH_LIVE=1 FUNDAMENTALS_TICKER=NVDA go test ./internal/fundamentals -run TestLiveFundamentals -v
//
// With MARKET_WATCH_LIVE_LLM=1 it also writes the analysis, which costs money.
//
// With PROMPT_OUT=<dir> it saves what /analyse would send the model -- the
// instructions and the opening message -- without sending it, so the input
// can be read, or handed to a model by some other route, at no cost.
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
	filings := &sec.Client{HTTP: &http.Client{Timeout: 30 * time.Second}, UserAgent: agent}
	for _, problem := range AddBusiness(ctx, filings, &snap, time.Now()) {
		t.Logf("business description: %v", problem)
	}
	if key := os.Getenv("FINNHUB_API_KEY"); key != "" {
		quotes, _ := (&prices.Client{APIKey: key, HTTP: &http.Client{Timeout: 20 * time.Second}}).
			Fetch(ctx, []string{ticker})
		if len(quotes) > 0 {
			snap.Price = &quotes[0]
		}
	}
	addMarket(ctx, t, &snap, ticker)
	t.Logf("read %s in %s", ticker, time.Since(started).Round(time.Millisecond))
	t.Logf("\n%s", snap.Table())

	if dir := os.Getenv("PROMPT_OUT"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("PROMPT_OUT: %v", err)
		}
		files := map[string]string{
			"system.txt": agentSystemPrompt,
			"prompt.txt": (&Agent{}).prompt(snap),
		}
		for name, text := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
				t.Fatalf("PROMPT_OUT: %v", err)
			}
		}
		t.Logf("saved the instructions and opening message to %s", dir)
	}

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

	// The same context the bot command supplies: what the company does, what it
	// has announced, and what its shares cost.
	filings := &sec.Client{HTTP: &http.Client{Timeout: 60 * time.Second}, UserAgent: agentUA}
	for _, problem := range AddBusiness(ctx, filings, &snap, time.Now().UTC()) {
		t.Logf("context: %v", problem)
	}
	if key := os.Getenv("FINNHUB_API_KEY"); key != "" {
		quotes, _ := (&prices.Client{APIKey: key, HTTP: &http.Client{Timeout: 20 * time.Second}}).
			Fetch(ctx, []string{ticker})
		if len(quotes) > 0 {
			snap.Price = &quotes[0]
		}
	}
	addMarket(ctx, t, &snap, ticker)
	t.Logf("business description: %d characters, events: %d", len(snap.Business), len(snap.Events))

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
	prose, related := SplitRelated(analysis.Text)
	t.Logf("%s", prose)

	verified := VerifyRelated(ctx, &discover.FIGI{HTTP: &http.Client{Timeout: 30 * time.Second}}, related)
	t.Logf("related companies: %d proposed, %d verified", len(related), len(verified))
	for _, r := range verified {
		t.Logf("  %-12s %s - %s", r.Symbol(), r.Name, r.Why)
	}
}

// addMarket attaches the two things that do not come from EDGAR: what the
// share has been doing, and what has been written about the company. Both are
// best-effort in the bot and best-effort here, so a live run still exercises
// the filings when one of them is unavailable.
func addMarket(ctx context.Context, t *testing.T, snap *Snapshot, ticker string) {
	t.Helper()
	now := time.Now().UTC()

	history := &prices.History{HTTP: &http.Client{Timeout: 25 * time.Second}}
	if series, err := history.Fetch(ctx, ticker); err != nil {
		t.Logf("price history: %v", err)
	} else {
		trading := prices.Summarise(series, now)
		snap.Trading = &trading
		t.Logf("price history: %d sessions to %s, last %.2f %s",
			trading.Days, trading.AsOf.Format(time.DateOnly), trading.Last, trading.Currency)
	}

	key := os.Getenv("FINNHUB_API_KEY")
	if key == "" {
		return
	}
	press := &prices.News{APIKey: key, HTTP: &http.Client{Timeout: 25 * time.Second}}
	if err := AddNews(ctx, press, snap, now); err != nil {
		t.Logf("company news: %v", err)
		return
	}
	t.Logf("company news: %d headlines kept", len(snap.News))
	for _, a := range snap.News {
		t.Logf("  %s — %s, %s", a.Title, a.SourceName, a.Published.Format(time.DateOnly))
	}
}
