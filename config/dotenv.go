package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// DefaultEnvFile is read by Load for local runs. In production the platform
// injects real environment variables and no such file exists.
const DefaultEnvFile = ".env"

// LoadDotEnv reads KEY=VALUE lines into the environment.
//
// Variables already set in the environment always win: production secrets are
// injected by the platform, and a stale .env left in an image must never
// silently override them. A missing file is not an error -- that is the normal
// production case.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		key, value, err := parseEnvLine(scanner.Text())
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if key == "" {
			continue // blank or comment
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, line, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// parseEnvLine returns an empty key for lines that carry no assignment.
func parseEnvLine(raw string) (key, value string, err error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", nil
	}
	// "export FOO=bar" is common in hand-written files.
	line = strings.TrimPrefix(line, "export ")

	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", "", fmt.Errorf("want KEY=VALUE, got %q", raw)
	}

	key = strings.TrimSpace(line[:eq])
	if key == "" {
		return "", "", fmt.Errorf("empty key in %q", raw)
	}
	value = strings.TrimSpace(line[eq+1:])

	// Quotes are stripped when they wrap the whole value, which is how a value
	// with trailing spaces or a leading '#' gets written.
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return key, value[1 : len(value)-1], nil
	}
	// An unquoted value keeps inner spaces -- USER_AGENT is a sentence -- but
	// loses a trailing comment, which quoting is the way to opt out of.
	if hash := strings.Index(value, " #"); hash >= 0 {
		value = strings.TrimSpace(value[:hash])
	}
	return key, value, nil
}
