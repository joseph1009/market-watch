package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	// pollSeconds is how long Telegram holds an empty getUpdates open before
	// answering. Long polling is why this has to be an always-on process: there
	// is no request to schedule, only a connection to keep.
	pollSeconds = 30

	// pollBackoff is the pause after a failed poll, so a network outage or a
	// revoked token cannot spin the loop.
	pollBackoff = 5 * time.Second
)

// Update is one event from Telegram. Only messages are requested, so anything
// else arrives with a nil Message and is skipped.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type getUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

// GetUpdates fetches pending updates, blocking until one arrives or the poll
// window closes. Passing the previous update's id plus one acknowledges
// everything before it, which is what stops Telegram redelivering.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", getUpdatesRequest{
		Offset:         offset,
		Timeout:        timeoutSeconds,
		AllowedUpdates: []string{"message"},
	}, &updates)
	if err != nil {
		return nil, err
	}
	return updates, nil
}

// Poll runs the long-polling loop until the context is cancelled, handing each
// message to handle. A failed poll is logged and retried rather than returned:
// the bot going quiet for a minute is recoverable, the process exiting is not.
//
// The HTTP client must allow longer than the poll window, or every poll ends in
// a client timeout.
func (c *Client) Poll(ctx context.Context, log *slog.Logger, handle func(context.Context, Message)) error {
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		updates, err := c.GetUpdates(ctx, offset, pollSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // shutting down, not a failure
			}
			log.Warn("telegram poll failed", "error", err)
			select {
			case <-time.After(pollBackoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		for _, u := range updates {
			// Advance the offset before handling: a message that makes the
			// handler fail must not be redelivered forever.
			offset = u.UpdateID + 1
			if u.Message == nil || u.Message.Text == "" {
				continue
			}
			handle(ctx, *u.Message)
		}
	}
}

// Command is one entry in the bot's command menu.
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type setMyCommandsRequest struct {
	Commands []Command `json:"commands"`
}

// SetMyCommands publishes the command menu.
//
// Without this a bot's commands are invisible: typing "/" offers nothing, and
// the only way to discover them is to be told. Telegram keeps the list until it
// is replaced, so publishing it on every start also corrects it after a change.
func (c *Client) SetMyCommands(ctx context.Context, commands []Command) error {
	return c.call(ctx, "setMyCommands", setMyCommandsRequest{Commands: commands}, nil)
}

// DrainUpdates acknowledges everything pending without acting on it, so a
// restart does not replay commands sent while the process was down.
func (c *Client) DrainUpdates(ctx context.Context) (int, error) {
	updates, err := c.GetUpdates(ctx, 0, 0)
	if err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}

	last := updates[len(updates)-1].UpdateID
	if _, err := c.GetUpdates(ctx, last+1, 0); err != nil {
		return 0, fmt.Errorf("acknowledge %d pending update(s): %w", len(updates), err)
	}
	return len(updates), nil
}
