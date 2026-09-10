// Package logging keeps credentials out of the log stream.
package logging

import (
	"context"
	"log/slog"
	"strings"
)

// Placeholder is what a secret is replaced with. It names the variable rather
// than blanking the text, so a log line still says which credential was in play.
const Placeholder = "[redacted]"

// minSecretLength guards against scrubbing something harmless. An empty or very
// short value would match everywhere and turn the logs to noise -- and a
// credential that short is not a credential.
const minSecretLength = 8

// Handler wraps another slog.Handler and removes known secrets from everything
// passing through it.
//
// This exists because a credential reaches the logs by routes nobody plans for.
// The bot token sits in the Telegram request path, so net/http put a working
// token into the text of every connection error -- an ordinary dropped
// connection printed it to the terminal. Redacting at each call site means
// finding every call site; redacting at the handler means the next unplanned
// route is covered too.
type Handler struct {
	inner   slog.Handler
	secrets []string
}

// New wraps inner so that any of secrets appearing in a message or attribute is
// replaced. Values too short to be credentials are ignored.
func New(inner slog.Handler, secrets ...string) *Handler {
	kept := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if len(s) >= minSecretLength {
			kept = append(kept, s)
		}
	}
	return &Handler{inner: inner, secrets: kept}
}

// Scrub replaces every known secret in a string. Exported for the paths that
// write to stderr directly, before a logger exists.
func Scrub(s string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) < minSecretLength {
			continue
		}
		s = strings.ReplaceAll(s, secret, Placeholder)
	}
	return s
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, h.scrub(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(h.scrubAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		cleaned[i] = h.scrubAttr(a)
	}
	return &Handler{inner: h.inner.WithAttrs(cleaned), secrets: h.secrets}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), secrets: h.secrets}
}

// scrubAttr cleans a value by kind. Strings and errors are the ones that carry
// a leaked credential; groups are walked because an attribute may nest.
func (h *Handler) scrubAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.scrub(a.Value.String()))
	case slog.KindGroup:
		inner := a.Value.Group()
		cleaned := make([]slog.Attr, len(inner))
		for i, g := range inner {
			cleaned[i] = h.scrubAttr(g)
		}
		a.Value = slog.GroupValue(cleaned...)
	case slog.KindAny:
		// An error's text is the most likely carrier of all: it is where a URL
		// with an embedded token ends up.
		if err, ok := a.Value.Any().(error); ok && err != nil {
			a.Value = slog.StringValue(h.scrub(err.Error()))
			return a
		}
		if s, ok := a.Value.Any().(string); ok {
			a.Value = slog.StringValue(h.scrub(s))
		}
	}
	return a
}

func (h *Handler) scrub(s string) string { return Scrub(s, h.secrets...) }
