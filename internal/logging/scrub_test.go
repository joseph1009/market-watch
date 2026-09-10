package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const (
	botToken   = "8955769785:AAGxNZFkcYWuE66JOuc1t-K0PYMX58JJiQw"
	apiKey     = "sk-ant-api03-notarealkeybutthesamelength-000000000000"
	shortNoise = "abc"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(New(inner, botToken, apiKey)), &buf
}

// The leak that prompted this: net/http puts the request URL into connection
// errors, and the bot token lives in that URL.
func TestHandlerScrubsATokenInsideAnError(t *testing.T) {
	log, buf := newTestLogger()

	err := fmt.Errorf(`Post "https://api.telegram.org/bot%s/sendMessage": read tcp: connection reset`, botToken)
	log.Error("delivery failed", "error", err)

	out := buf.String()
	if strings.Contains(out, botToken) {
		t.Errorf("the token survived into the log:\n%s", out)
	}
	if !strings.Contains(out, Placeholder) {
		t.Errorf("nothing was redacted:\n%s", out)
	}
	// The rest of the message has to survive, or the log stops being useful.
	if !strings.Contains(out, "connection reset") {
		t.Errorf("the error text was lost:\n%s", out)
	}
}

func TestHandlerScrubsTheMessageItself(t *testing.T) {
	log, buf := newTestLogger()
	log.Info("calling https://api.telegram.org/bot" + botToken + "/getMe")

	if strings.Contains(buf.String(), botToken) {
		t.Errorf("the token survived in the message:\n%s", buf.String())
	}
}

func TestHandlerScrubsStringAttributes(t *testing.T) {
	log, buf := newTestLogger()
	log.Info("configured", "key", apiKey)

	if strings.Contains(buf.String(), apiKey) {
		t.Errorf("the API key survived:\n%s", buf.String())
	}
}

// Attributes attached with With() are rendered on every later line, so they
// have to be cleaned when they are attached.
func TestHandlerScrubsPreAttachedAttributes(t *testing.T) {
	log, buf := newTestLogger()
	log.With("token", botToken).Info("starting")

	if strings.Contains(buf.String(), botToken) {
		t.Errorf("a With() attribute leaked the token:\n%s", buf.String())
	}
}

func TestHandlerScrubsInsideGroups(t *testing.T) {
	log, buf := newTestLogger()
	log.Info("request", slog.Group("http", slog.String("url", "https://x/bot"+botToken+"/send")))

	if strings.Contains(buf.String(), botToken) {
		t.Errorf("a grouped attribute leaked the token:\n%s", buf.String())
	}
}

// A short or empty secret would match everywhere and turn the log to noise.
func TestShortValuesAreNotTreatedAsSecrets(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(New(slog.NewTextHandler(&buf, nil), shortNoise, ""))
	log.Info("the abc of it", "note", "abcdef")

	out := buf.String()
	if strings.Contains(out, Placeholder) {
		t.Errorf("a short value was scrubbed as if it were a secret:\n%s", out)
	}
	if !strings.Contains(out, "abcdef") {
		t.Errorf("ordinary text was damaged:\n%s", out)
	}
}

func TestScrubHandlesTheDirectPath(t *testing.T) {
	in := "failed to reach https://api.telegram.org/bot" + botToken + "/getMe"
	got := Scrub(in, botToken, apiKey)

	if strings.Contains(got, botToken) {
		t.Errorf("Scrub left the token in: %q", got)
	}
	if !strings.Contains(got, "failed to reach") {
		t.Errorf("Scrub damaged the message: %q", got)
	}
}

func TestScrubIsANoOpWithoutSecrets(t *testing.T) {
	const msg = "nothing sensitive here"
	if got := Scrub(msg); got != msg {
		t.Errorf("Scrub(%q) = %q", msg, got)
	}
}

func TestHandlerLeavesOrdinaryLoggingIntact(t *testing.T) {
	log, buf := newTestLogger()
	log.Info("collected", "fetched", 643, "kept", 269)

	out := buf.String()
	for _, want := range []string{"collected", "fetched=643", "kept=269"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHandlerRespectsTheInnerLevel(t *testing.T) {
	var buf bytes.Buffer
	h := New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}), botToken)

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled ignored the inner handler's level")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled suppressed a level the inner handler allows")
	}
}

func TestHandlerScrubsAWrappedError(t *testing.T) {
	log, buf := newTestLogger()
	wrapped := fmt.Errorf("send part 1 of 4: %w", errors.New("dial https://x/bot"+botToken))
	log.Error("failed", "error", wrapped)

	if strings.Contains(buf.String(), botToken) {
		t.Errorf("a wrapped error leaked the token:\n%s", buf.String())
	}
}
