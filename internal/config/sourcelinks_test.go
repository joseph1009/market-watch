package config

import (
	"strings"
	"testing"
)

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
	if cfg.SourceLinks != SourceLinksShort {
		t.Errorf("SourceLinks = %q, want %q", cfg.SourceLinks, SourceLinksShort)
	}
}

func TestSourceLinksAcceptsEveryModeCaseInsensitively(t *testing.T) {
	tests := map[string]string{
		"full":  SourceLinksFull,
		"FULL":  SourceLinksFull,
		"Off":   SourceLinksOff,
		"off":   SourceLinksOff,
		"SHORT": SourceLinksShort,
	}
	for set, want := range tests {
		withRequiredEnv(t)
		t.Setenv("SOURCE_LINKS", set)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load with %q: %v", set, err)
		}
		if cfg.SourceLinks != want {
			t.Errorf("SOURCE_LINKS=%q gave %q, want %q", set, cfg.SourceLinks, want)
		}
	}
}

// A typo should stop the process with a clear message, not silently fall back
// to a mode you did not ask for.
func TestSourceLinksRejectsUnknownValues(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SOURCE_LINKS", "everything")

	_, err := Load()
	if err == nil {
		t.Fatal("an unknown SOURCE_LINKS value was accepted")
	}
	if !strings.Contains(err.Error(), "off, short or full") {
		t.Errorf("error %q does not name the valid values", err)
	}
}
