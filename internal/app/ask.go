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
	message int64       // the question's id in the chat
	expire  *time.Timer // takes the question down when the wait is over
}

// ask sends a question for command and waits for the answer from chat. The
// question opens the reader's reply, which also lets the answer through in a
// group, where a bot sees only commands and replies to it.
func (a *App) ask(ctx context.Context, chat int64, command, text, placeholder string) error {
	id, err := a.Bot.Ask(ctx, chat, text, placeholder)
	if err != nil {
		return err
	}
	q := question{command: command, until: a.now().Add(answerWithin), message: id}
	q.expire = time.AfterFunc(answerWithin, func() { a.expire(chat, id) })
	a.askMu.Lock()
	if a.asked == nil {
		a.asked = map[int64]question{}
	}
	old, waiting := a.asked[chat]
	a.asked[chat] = q
	a.askMu.Unlock()
	if waiting {
		a.withdraw(ctx, chat, old)
	}
	return nil
}

// answering takes the question waiting on chat, if there is one still in
// time, and returns its command. A question is answered once, or dropped
// when a command comes instead.
func (a *App) answering(ctx context.Context, chat int64) string {
	q, ok := a.take(chat)
	if !ok {
		return ""
	}
	q.expire.Stop()
	if a.now().After(q.until) {
		a.withdraw(ctx, chat, q)
		return ""
	}
	return q.command // answered: the reply quotes the question, so it stays
}

// forget drops the question waiting on chat, as a command came instead of
// its answer.
func (a *App) forget(ctx context.Context, chat int64) {
	if q, ok := a.take(chat); ok {
		a.withdraw(ctx, chat, q)
	}
}

// expire drops a question nobody answered in time, unless another has taken
// its place.
func (a *App) expire(chat, message int64) {
	a.askMu.Lock()
	q, ok := a.asked[chat]
	if !ok || q.message != message {
		a.askMu.Unlock()
		return
	}
	delete(a.asked, chat)
	a.askMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a.withdraw(ctx, chat, q)
}

func (a *App) take(chat int64) (question, bool) {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	q, ok := a.asked[chat]
	delete(a.asked, chat)
	return q, ok
}

// withdraw deletes a question the bot no longer waits on. Left in the chat,
// it opens a reply to itself every time the chat is opened, asking for an
// answer that would now be taken as chatter (2026-10-02).
func (a *App) withdraw(ctx context.Context, chat int64, q question) {
	q.expire.Stop()
	if q.message == 0 {
		return
	}
	if _, failed := a.Bot.DeleteMessages(ctx, chat, []int64{q.message}); failed > 0 {
		a.Log.Warn("could not take down an unanswered question", "chat", chat)
	}
}

// tickerShape is what a ticker can look like: BRK.B, RDS-A, 7203.
var tickerShape = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.\-]{0,9}$`)

// asTicker reads a ticker as people type it: any case, perhaps with a "$".
func asTicker(s string) (string, bool) {
	t := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	return t, tickerShape.MatchString(t)
}
