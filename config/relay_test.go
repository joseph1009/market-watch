package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The service as deployed: every call answered by Claude Code headless, and no
// API key asked for anywhere.
func TestRelayDefaultsToHeadlessClaudeWithNoAPIKey(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("RELAY_ANSWER", "")
	t.Setenv("RELAY_DIR", "")
	t.Setenv("CALL_TIMEOUT", "")
	t.Setenv("DATA_DIR", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RelayAnswer != AnswerClaude {
		t.Errorf("RelayAnswer = %q, want %q", cfg.RelayAnswer, AnswerClaude)
	}
	if want := filepath.Join(cfg.DataDir, "relay"); cfg.RelayDir != want {
		t.Errorf("RelayDir = %q, want %q beside the rest of the data", cfg.RelayDir, want)
	}
	if cfg.CallTimeout != 20*time.Minute {
		t.Errorf("CallTimeout = %s, want 20m for a headless call", cfg.CallTimeout)
	}
	if cfg.RelayConcurrency != DefaultRelayConcurrency {
		t.Errorf("RelayConcurrency = %d, want %d", cfg.RelayConcurrency, DefaultRelayConcurrency)
	}
}

// Answering by hand can take hours; the default has to allow for it.
func TestASessionRunWaitsLongerByDefault(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("RELAY_ANSWER", "Session")
	t.Setenv("CALL_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RelayAnswer != AnswerSession {
		t.Errorf("RelayAnswer = %q, want it read case-insensitively as %q", cfg.RelayAnswer, AnswerSession)
	}
	if cfg.CallTimeout != 3*time.Hour {
		t.Errorf("CallTimeout = %s, want 3h", cfg.CallTimeout)
	}
}

func TestRelayRejectsAnUnknownAnswerer(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("RELAY_ANSWER", "api")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "RELAY_ANSWER") {
		t.Errorf("err = %v, want RELAY_ANSWER named", err)
	}
}

// Only what is set is overridden; the relay supplies the rest.
func TestStageModelsCarryOnlyWhatWasSet(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("MODEL_TRIAGE", "sonnet")
	t.Setenv("MODEL_BRIEF", "")
	t.Setenv("MODEL_NAMES", "")
	t.Setenv("MODEL_ANALYSIS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.StageModels["triage"]; got != "sonnet" {
		t.Errorf("triage model = %q, want sonnet", got)
	}
	if _, set := cfg.StageModels["brief"]; set {
		t.Error("an unset stage was given a model, which would mask the relay's default")
	}
}
