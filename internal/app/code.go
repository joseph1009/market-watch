package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/internal/telegram"
)

// remoteReady is what Remote Control prints once it is connected and the
// Sprite shows in the Claude app.
const remoteReady = "Ready"

// openTheApp says where the session is found once Remote Control is on.
const openTheApp = "Open the Code tab in the Claude app and pick market-watch."

// handleCode answers /code: it starts Remote Control on the Sprite the code is
// worked on from, so a session can be started from the Claude app on a phone,
// and /code stop turns it off. A running Remote Control keeps the Sprite awake
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
		was, err := a.Code.Stop(ctx)
		if err != nil {
			return err
		}
		a.Log.Info("remote control stopped", "was_running", was)
		if !was {
			return a.Bot.SendMessage(ctx, chat, "Remote Control was already off.")
		}
		return a.Bot.SendMessage(ctx, chat, "Remote Control is off. The Sprite goes back to sleep in about 30 seconds.")
	default:
		return a.Bot.SendMessage(ctx, chat, "Send /code to turn on Remote Control on the Sprite, or /code stop to turn it off.")
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
	a.Log.Info("remote control started", "ready", ready)
	if !ready {
		return a.Bot.SendMessage(ctx, chat,
			"Remote Control started but hasn't said it's connected yet. Look in the Claude app in a minute, or send /code again to check.")
	}
	return a.Bot.SendMessage(ctx, chat, "Remote Control is on. "+openTheApp+
		"\n\nIt turns itself off after two quiet hours, or send /code stop.")
}
