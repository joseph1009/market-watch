package config

import "testing"

func withRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
}

// The daily brief keeps the short source list unless asked otherwise.
func TestSourceLinksDefaultsToShort(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SOURCE_LINKS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FullSources {
		t.Error("FullSources is on without SOURCE_LINKS=full")
	}
}

func TestSourceLinksFullIsCaseInsensitive(t *testing.T) {
	for _, v := range []string{"full", "FULL", "Full"} {
		withRequiredEnv(t)
		t.Setenv("SOURCE_LINKS", v)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load with %q: %v", v, err)
		}
		if !cfg.FullSources {
			t.Errorf("SOURCE_LINKS=%q did not turn on full sources", v)
		}
	}
}

// A typo should stop the process with a clear message, not silently fall back
// to the short list while you think you are cross-checking the full one.
func TestSourceLinksRejectsUnknownValues(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SOURCE_LINKS", "everything")

	if _, err := Load(); err == nil {
		t.Error("an unknown SOURCE_LINKS value was accepted")
	}
}
