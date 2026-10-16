package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/telegram"
)

// remoteReady is the tick Remote Control prints once it is connected and the
// Sprite shows in the Claude app: "·✔︎· Ready" until version 2.1.251 at least,
// and "·✔︎· Connected" by 2.1.296. The word changed, so the tick is what is
// matched.
const remoteReady = "✔"

// claudeUpdateWait is how long /code update may take. A new version is a
// download of about 250 MB.
const claudeUpdateWait = 4 * time.Minute

// openTheApp says how to start a session once Remote Control is on. No
// session is made in advance (scripts/remote-control.sh): the one Remote
// Control made by itself took no messages.
const openTheApp = "Open the Code tab in the Claude app, pick market-watch, and start a new session there."

// handleCode answers /code: it starts Remote Control on the Sprite the code is
// worked on from, so a session can be started from the Claude app on a phone,
// /code stop turns it off, and /code update updates Claude Code there. A running Remote Control keeps the Sprite awake
// and billed, so it is off until asked for, and it turns itself off after two
// quiet hours (scripts/remote-control.sh). The owner's chat only: a session
// there can change the code and deploy it.
func (a *App) handleCode(ctx context.Context, msg telegram.Message, args []string) error {
	chat := msg.Chat.ID
	if !a.Code.Enabled() {
		return a.Bot.SendMessage(ctx, chat,
			"/code needs SPRITES_TOKEN, a token from sprites.dev, set on Fly. See \"The Sprite\" in docs/RUNBOOK.md.")
	}

	switch strings.ToLower(strings.Join(args, " ")) {
	case "":
	case "stop", "off":
		a.stopRemoteCleanly(ctx)
		was, err := a.Code.Stop(ctx)
		if err != nil {
			return err
		}
		a.Log.Info("remote control stopped", "was_running", was)
		if !was {
			return a.Bot.SendMessage(ctx, chat, "Remote Control was already off.")
		}
		return a.Bot.SendMessage(ctx, chat, "Remote Control is off. The Sprite goes back to sleep in about 30 seconds.")
	case "update":
		return a.updateClaude(ctx, chat)
	default:
		return a.Bot.SendMessage(ctx, chat, "Send /code to turn on Remote Control on the Sprite, /code stop to turn it off, or /code update to update Claude Code there.")
	}

	state, err := a.Code.State(ctx)
	if err != nil {
		return err
	}
	if state.Running {
		since := state.Since.In(a.Cfg.DisplayLocation).Format("15:04")
		return a.Bot.SendMessage(ctx, chat, fmt.Sprintf(
			"Remote Control is already on, since %s. %s\n\nSend /code stop to turn it off.", since, openTheApp))
	}

	if err := a.Bot.SendMessage(ctx, chat, "Waking the Sprite and starting Remote Control…"); err != nil {
		return err
	}
	ready, err := a.Code.Start(ctx, remoteReady)
	if err != nil {
		return err
	}
	if !ready {
		ready = a.connectedSince(ctx)
	}
	a.Log.Info("remote control started", "ready", ready)
	if !ready {
		return a.Bot.SendMessage(ctx, chat,
			"Remote Control started but hasn't said it's connected yet. Look in the Claude app in a minute, or send /code again to check.")
	}
	return a.Bot.SendMessage(ctx, chat, "Remote Control is on. "+openTheApp+
		"\n\nIt turns itself off after two quiet hours, or send /code stop.")
}

// updateClaude updates Claude Code on the Sprite and says when it is done,
// from which version to which. A Remote Control already running keeps the
// version it started with, so the reply says when it needs a restart. It is
// not restarted here: that would end a session that may be mid-task.
func (a *App) updateClaude(ctx context.Context, chat int64) error {
	if err := a.Bot.SendMessage(ctx, chat, "Updating Claude Code on the Sprite. I'll tell you when it's done."); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, claudeUpdateWait)
	defer cancel()

	before, err := a.claudeVersion(ctx)
	if err != nil {
		return err
	}
	out, code, err := a.Code.Exec(ctx, "bash", "-lc", "claude update")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("claude update exited with status %d: %s", code, lastLine(out))
	}
	after, err := a.claudeVersion(ctx)
	if err != nil {
		return err
	}
	a.Log.Info("claude code updated", "from", before, "to", after)
	if after == before {
		return a.Bot.SendMessage(ctx, chat, "Claude Code on the Sprite is already the newest version, "+after+".")
	}

	reply := fmt.Sprintf("Claude Code on the Sprite is updated, from %s to %s.", before, after)
	state, err := a.Code.State(ctx)
	switch {
	case err != nil:
		reply += " If Remote Control is on, send /code stop and then /code to start it on the new version."
	case state.Running:
		reply += fmt.Sprintf(" Remote Control is still running %s. Send /code stop and then /code to start it on %s. That ends any session open in the app.", before, after)
	default:
		reply += " The next /code starts on it."
	}
	return a.Bot.SendMessage(ctx, chat, reply)
}

// remoteLog is the remote-control service's output on the Sprite.
const remoteLog = "/.sprite/logs/services/remote-control.log"

// connectedWait is how much longer /code looks for the tick once the start's
// own output has ended. The Sprites API can end it a few seconds after the
// service starts, before Remote Control has connected: on 16 Oct 2026 it
// ended at 4 seconds, and the tick came 2 seconds later, so the owner was
// told it had not connected when it had.
var connectedWait = 45 * time.Second

// connectedSince reports whether Remote Control has printed its tick since
// the service last started, reading the service's log every few seconds for
// up to connectedWait.
func (a *App) connectedSince(ctx context.Context) bool {
	// The log keeps every run: only the lines after the newest start count.
	cmd := "tac " + remoteLog + " | sed '/remote-control: started/q' | grep -c '" + remoteReady + "'"
	deadline := time.Now().Add(connectedWait)
	for {
		out, _, err := a.Code.Exec(ctx, "bash", "-lc", cmd)
		if err == nil && strings.TrimSpace(out) != "0" && strings.TrimSpace(out) != "" {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(3 * time.Second):
		}
	}
}

// remoteScript is the service's script on the Sprite.
const remoteScript = "~/market-watch/scripts/remote-control.sh"

// stopWait bounds the clean stop: the script gives Remote Control 30 seconds.
const stopWait = 45 * time.Second

// stopRemoteCleanly asks Remote Control to stop and waits for it, before the
// service is stopped. The service manager kills it about a second after its
// own stop, before it has taken itself off the Claude app's list, so every
// run left a "market-watch" entry behind there (16 Oct 2026). A Sprite that
// is not running it is not woken for this.
func (a *App) stopRemoteCleanly(ctx context.Context) {
	state, err := a.Code.State(ctx)
	if err != nil || !state.Running {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, stopWait)
	defer cancel()
	if out, code, err := a.Code.Exec(ctx, "bash", "-lc", "bash "+remoteScript+" stop"); err != nil || code != 0 {
		a.Log.Warn("remote control: the clean stop failed; stopping the service anyway", "error", err, "status", code, "output", lastLine(out))
	}
}

// claudeVersion is the version of Claude Code on the Sprite, as "2.1.296".
func (a *App) claudeVersion(ctx context.Context) (string, error) {
	out, code, err := a.Code.Exec(ctx, "bash", "-lc", "claude --version")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("claude --version exited with status %d: %s", code, lastLine(out))
	}
	v, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	return v, nil
}

// lastLine is the last line of a command's output that says anything.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
