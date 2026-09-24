package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestLoadDotEnvReadsAssignments(t *testing.T) {
	path := writeEnvFile(t, `
# a comment
TELEGRAM_BOT_TOKEN=12345:abcdef

export REPORT_AT=20:30

# USER_AGENT is a sentence: inner spaces must survive unquoted.
USER_AGENT=Market Watch someone@example.com
QUOTED="  padded  "
`)

	for _, k := range []string{"TELEGRAM_BOT_TOKEN", "REPORT_AT", "USER_AGENT", "QUOTED"} {
		t.Setenv(k, "") // registered for cleanup, then cleared so the file wins
		os.Unsetenv(k)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	tests := map[string]string{
		"TELEGRAM_BOT_TOKEN": "12345:abcdef",
		"REPORT_AT":          "20:30", // the value's own colon is not a separator
		"USER_AGENT":         "Market Watch someone@example.com",
		"QUOTED":             "  padded  ", // quotes preserve the padding
	}
	for k, want := range tests {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// The production case: secrets come from the platform, and a stale .env baked
// into an image must never quietly replace them.
func TestLoadDotEnvDoesNotOverrideTheRealEnvironment(t *testing.T) {
	path := writeEnvFile(t, "FRED_API_KEY=from-the-file\n")

	t.Setenv("FRED_API_KEY", "from-the-platform")
	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("FRED_API_KEY"); got != "from-the-platform" {
		t.Errorf("FRED_API_KEY = %q, want the platform value to win", got)
	}
}

// Production has no .env at all; that is normal, not a failure.
func TestLoadDotEnvIgnoresAMissingFile(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Errorf("LoadDotEnv on a missing file returned %v, want nil", err)
	}
}

func TestLoadDotEnvReportsMalformedLines(t *testing.T) {
	path := writeEnvFile(t, "GOOD=1\nthis is not an assignment\n")
	if err := LoadDotEnv(path); err == nil {
		t.Error("LoadDotEnv accepted a malformed line, want an error naming the line")
	}
}

func TestParseEnvLineStripsTrailingComments(t *testing.T) {
	tests := []struct {
		in        string
		key, want string
	}{
		{"MAX_ARTICLES=250 # per run", "MAX_ARTICLES", "250"},
		{`TOKEN="secret # not a comment"`, "TOKEN", "secret # not a comment"},
		{"LOG_LEVEL=info", "LOG_LEVEL", "info"},
		{"   ", "", ""},
		{"# whole line", "", ""},
	}
	for _, tt := range tests {
		key, value, err := parseEnvLine(tt.in)
		if err != nil {
			t.Errorf("parseEnvLine(%q): %v", tt.in, err)
			continue
		}
		if key != tt.key || value != tt.want {
			t.Errorf("parseEnvLine(%q) = %q, %q; want %q, %q", tt.in, key, value, tt.key, tt.want)
		}
	}
}
