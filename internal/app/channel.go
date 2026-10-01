package app

import (
	"context"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// The channel is where other people read the brief. It is one-way: a channel's
// readers cannot send the bot anything that reaches HandleMessage, and the
// bot takes commands from the owner's chat alone, so sharing the brief shares
// nothing else.
//
// What reaches the channel is only ever something already written for the
// owner. Nobody else can ask for a brief or an analysis: every model call is
// the owner's own use of the owner's plan, and the channel is where the owner
// chooses to put the result.
//
// The daily brief goes there by itself, and since the closer look was opened
// to readers, so does that. /now and /analyse go to the owner first, and
// /share posts whichever arrived last, so a brief asked for to check
// something, or an analysis the owner has not read yet, stays private until
// the owner says otherwise.

// delivery is something sent to the owner that /share can pass on.
type delivery struct {
	what  string // how the replies name it: "the NVDA analysis"
	owner outgoing

	// channel is the copy for the channel where it differs from the owner's:
	// an analysis, whose verdict goes out under the warning a channel reader
	// needs. Nil posts the owner's.
	channel *outgoing

	shared  bool
	sharing bool // a post to the channel is under way
}

var (
	errNoChannel     = errors.New("no channel is set up")
	errAlreadyShared = errors.New("already in the channel")
)

// remember records what was just delivered to the owner, so /share can post
// it. Kept in memory: a restart forgets it, and the next brief or analysis
// would replace it anyway.
func (a *App) remember(what string, messages []string) *delivery {
	return a.rememberFor(what, messages, nil)
}

// rememberFor is remember with a different copy for the channel.
func (a *App) rememberFor(what string, messages, channel []string) *delivery {
	d := &delivery{what: what, owner: outgoing{messages: messages}}
	if channel != nil {
		d.channel = &outgoing{messages: channel}
	}
	return a.keep(d)
}

// rememberSent is rememberFor for what was sent as a summary and a page:
// the channel gets the same, from its own copy.
func (a *App) rememberSent(what string, owner outgoing, channel *outgoing) *delivery {
	return a.keep(&delivery{what: what, owner: owner, channel: channel})
}

func (a *App) keep(d *delivery) *delivery {
	a.lastMu.Lock()
	a.last = d
	a.lastMu.Unlock()
	return d
}

// latest is the most recent delivery to the owner, or nil.
func (a *App) latest() *delivery {
	a.lastMu.Lock()
	defer a.lastMu.Unlock()
	return a.last
}

// share posts a delivery to the channel, once. The scheduler and /share can
// reach the same brief at the same moment, so the check and the claim happen
// together under the lock, and a failed post gives the claim back.
func (a *App) share(ctx context.Context, d *delivery) error {
	channel := a.Cfg.TelegramChannelID
	if channel == 0 {
		return errNoChannel
	}

	a.lastMu.Lock()
	if d.shared || d.sharing {
		a.lastMu.Unlock()
		return errAlreadyShared
	}
	d.sharing = true
	a.lastMu.Unlock()

	out := d.owner
	if d.channel != nil {
		out = *d.channel
	}
	ids, err := a.send(ctx, channel, out, true)

	a.lastMu.Lock()
	d.sharing = false
	d.shared = err == nil
	a.lastMu.Unlock()

	if err != nil {
		return fmt.Errorf("post %s to the channel: %w", d.what, err)
	}
	a.Log.Info("posted to the channel", "what", d.what, "messages", len(ids), "channel", channel)
	return nil
}

// shareBrief is the scheduler's half: the daily brief goes to the channel as
// soon as the owner has it. A failure costs the channel one brief, never the
// owner's copy, so it is reported to the owner with the way to retry.
func (a *App) shareBrief(ctx context.Context, d *delivery) {
	if d == nil || a.Cfg.TelegramChannelID == 0 {
		return
	}
	err := a.share(ctx, d)
	if err == nil || errors.Is(err, errAlreadyShared) {
		return
	}

	a.Log.Error("could not post the brief to the channel", "error", err)
	owner := a.Prefs().ChatID
	if owner == 0 {
		return
	}
	clean := logging.Scrub(err.Error(), a.Cfg.Secrets()...)
	text := fmt.Sprintf(
		"Today's brief reached you but not the channel.\n\n<i>%s</i>\n\nSend /share to post it again. If part of it did arrive, that part will appear twice.",
		escape(clean))
	if err := a.Bot.SendMessage(ctx, owner, text); err != nil {
		a.Log.Error("could not report the channel failure either", "error", err)
	}
}

// shareIdeas posts the closer look to the channel, which the daily run does
// once the owner has it. It does not go through share and remember: those exist
// so /share can pass on the last thing delivered, and /share passes on briefs
// and analyses only. A verdict reaches the channel with the daily run or not at
// all.
//
// Like the brief, a failure here costs the channel and never the owner's copy.
func (a *App) shareIdeas(ctx context.Context, out outgoing) {
	channel := a.Cfg.TelegramChannelID
	if channel == 0 || len(out.messages) == 0 {
		return
	}

	ids, err := a.send(ctx, channel, out, true)
	if err != nil {
		a.Log.Error("could not post the closer look to the channel", "error", err)
		owner := a.Prefs().ChatID
		if owner == 0 {
			return
		}
		clean := logging.Scrub(err.Error(), a.Cfg.Secrets()...)
		text := fmt.Sprintf(
			"Today's closer look reached you but not the channel.\n\n<i>%s</i>\n\nThere is no command to post it again: it goes with the daily run or not at all.",
			escape(clean))
		if err := a.Bot.SendMessage(ctx, owner, text); err != nil {
			a.Log.Error("could not report the channel failure either", "error", err)
		}
		return
	}
	a.Log.Info("posted to the channel", "what", "the closer look", "messages", len(ids), "channel", channel)
}

// handleShare posts the latest brief or analysis to the channel.
func (a *App) handleShare(ctx context.Context, msg telegram.Message) error {
	if a.Cfg.TelegramChannelID == 0 {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"No channel is set up. Make the bot an admin of a channel, then set TELEGRAM_CHANNEL_ID to the channel's id.")
	}

	d := a.latest()
	if d == nil {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"Nothing to share: no brief or analysis has been sent since the bot last started. /share posts whichever of /now or /analyse arrived last.")
	}

	switch err := a.share(ctx, d); {
	case errors.Is(err, errAlreadyShared):
		return a.Bot.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("%s is already in the channel.", escape(capitalise(d.what))))
	case err != nil:
		return err
	}
	return a.Bot.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("Posted %s to the channel.", escape(d.what)))
}

func capitalise(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
