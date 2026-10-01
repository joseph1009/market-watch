package app

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// A command can ask for what it needs rather than take it on the same line:
// /analyse on its own asks which company, /industry which industry, and the
// next message from that chat is the answer. The owner found "/analyse NVDA"
// unnatural to type (2026-10-01). The one-line form still works, which suits
// a bot sending commands.

// answerWithin is how long a question waits for its answer. A ticker sent
// an hour later is more likely chatter than a reply.
const answerWithin = 10 * time.Minute

// question is a command waiting for the rest of itself.
type question struct {
	command string
	until   time.Time
}

// ask sends a question for command and waits for the answer from chat. The
// question opens the reader's reply, which also lets the answer through in a
// group, where a bot sees only commands and replies to it.
func (a *App) ask(ctx context.Context, chat int64, command, text, placeholder string) error {
	a.askMu.Lock()
	if a.asked == nil {
		a.asked = map[int64]question{}
	}
	a.asked[chat] = question{command: command, until: a.now().Add(answerWithin)}
	a.askMu.Unlock()
	return a.Bot.Ask(ctx, chat, text, placeholder)
}

// answering takes the question waiting on chat, if there is one still in
// time, and returns its command. A question is answered once, or dropped
// when a command comes instead.
func (a *App) answering(chat int64) string {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	q, ok := a.asked[chat]
	delete(a.asked, chat)
	if !ok || a.now().After(q.until) {
		return ""
	}
	return q.command
}

// forget drops the question waiting on chat.
func (a *App) forget(chat int64) {
	a.askMu.Lock()
	delete(a.asked, chat)
	a.askMu.Unlock()
}

// tickerShape is what a ticker can look like: BRK.B, RDS-A, 7203.
var tickerShape = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.\-]{0,9}$`)

// asTicker reads a ticker as people type it: any case, perhaps with a "$".
func asTicker(s string) (string, bool) {
	t := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	return t, tickerShape.MatchString(t)
}
