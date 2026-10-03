package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/relay"
)

// analysisTools offers /analyse the account tools: this same program, started
// by Claude Code with -mcp-accounts for the analysis call alone
// (fundamentals.AccountTools). Nil where they cannot be offered: switched
// off, answered by hand rather than by Claude Code, or run from a test
// binary, which has no such mode.
func analysisTools(cfg *config.Config, log *slog.Logger) func(context.Context, int) context.Context {
	if !cfg.AnalysisTools || cfg.RelayAnswer != config.AnswerClaude {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		log.Warn("analysis tools off: this program's path could not be read", "error", err)
		return nil
	}
	if strings.HasSuffix(strings.TrimSuffix(filepath.Base(exe), ".exe"), ".test") {
		return nil
	}
	return func(ctx context.Context, cik int) context.Context {
		return relay.WithTools(ctx, relay.Tools{
			Name:    "accounts",
			Command: exe,
			Args:    []string{"-mcp-accounts", strconv.Itoa(cik)},
			// The SEC asks to be told who is reading. In the server's
			// configuration file, not on its command line.
			Env:     map[string]string{"USER_AGENT": cfg.UserAgent},
			Allowed: []string{"find_concepts", "read_concept", "compute"},
			LogFlag: "-tools-log",
		})
	}
}
