// Package telegram delivers the daily brief over the Telegram Bot API.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the Bot API host. Overridden in tests.
const DefaultBaseURL = "https://api.telegram.org"

const (
	// defaultMaxRetries bounds the 429 backoff. The report is not urgent to the
	// minute, but it must not retry forever and hold the process open.
	defaultMaxRetries = 3

	// maxRetryAfter caps how long we will honour a server-supplied delay. A
	// pathological retry_after must not park delivery for an hour.
	maxRetryAfter = 2 * time.Minute
)

// Client talks to one bot.
type Client struct {
	Token   string
	HTTP    *http.Client
	BaseURL string

	MaxRetries int

	// Sleep is injected so retry tests do not actually wait.
	Sleep func(context.Context, time.Duration) error
}

// New builds a client for the given bot token.
func New(token string, httpClient *http.Client) *Client {
	return &Client{Token: token, HTTP: httpClient, BaseURL: DefaultBaseURL}
}

// apiError is a Bot API rejection, as opposed to a transport failure.
type apiError struct {
	Method      string
	Code        int
	Description string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

type sendMessageRequest struct {
	ChatID                int64  `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
	DisableNotification   bool   `json:"disable_notification,omitempty"`

	ReplyMarkup *inlineKeyboard `json:"reply_markup,omitempty"`
}

// inlineKeyboard is a row of buttons under a message; here, only ever one
// that opens a web page.
type inlineKeyboard struct {
	Rows [][]urlButton `json:"inline_keyboard"`
}

type urlButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// SendMessage delivers one HTML-formatted message.
//
// Link previews are suppressed: a brief carries many article links, and
// Telegram would otherwise expand the first one into a card that buries the
// prose the reader actually wants.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string) error {
	_, err := c.Send(ctx, chatID, html)
	return err
}

// Send delivers one message and returns its id, which is what a later delete
// needs.
func (c *Client) Send(ctx context.Context, chatID int64, html string) (int64, error) {
	return c.send(ctx, chatID, html, false)
}

// send is Send with the choice of arriving silently: shown in the chat, but
// without a sound or a banner.
func (c *Client) send(ctx context.Context, chatID int64, html string, silent bool) (int64, error) {
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	err := c.call(ctx, "sendMessage", sendMessageRequest{
		ChatID:                chatID,
		Text:                  html,
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
		DisableNotification:   silent,
	}, &sent)
	return sent.MessageID, err
}

// SendLinked delivers one message with a button under it that opens url:
// a summary, and the way to the whole thing. In a channel it can arrive
// silently, as the parts of a long post after the first do.
func (c *Client) SendLinked(ctx context.Context, chatID int64, html, label, url string, silent bool) (int64, error) {
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	err := c.call(ctx, "sendMessage", sendMessageRequest{
		ChatID:                chatID,
		Text:                  html,
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
		DisableNotification:   silent,
		ReplyMarkup:           &inlineKeyboard{Rows: [][]urlButton{{{Text: label, URL: url}}}},
	}, &sent)
	return sent.MessageID, err
}

// SendReport delivers the rendered brief in order and returns the ids of what
// arrived. It stops at the first failure so a half-sent report is not
// compounded by later parts arriving out of context -- and the ids of the parts
// that did land are still returned, so they can be cleaned up.
func (c *Client) SendReport(ctx context.Context, chatID int64, messages []string) ([]int64, error) {
	return c.sendAll(ctx, chatID, messages, false)
}

// Broadcast is SendReport for a channel: only the first part makes a sound.
// A brief is a dozen messages, and a dozen notifications a day for one brief
// is how a channel gets muted by the people it was meant for.
func (c *Client) Broadcast(ctx context.Context, chatID int64, messages []string) ([]int64, error) {
	return c.sendAll(ctx, chatID, messages, true)
}

func (c *Client) sendAll(ctx context.Context, chatID int64, messages []string, quietAfterFirst bool) ([]int64, error) {
	ids := make([]int64, 0, len(messages))
	for i, m := range messages {
		id, err := c.send(ctx, chatID, m, quietAfterFirst && i > 0)
		if err != nil {
			return ids, fmt.Errorf("send part %d of %d: %w", i+1, len(messages), err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

type deleteMessageRequest struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int64 `json:"message_id"`
}

// SweepMessages deletes a window of message ids ending at from and working
// backwards, for clearing briefs sent before their ids were being recorded.
//
// It is safe to attempt ids that are not ours. In a private chat a bot may only
// delete its own outgoing messages, so a request against the reader's message,
// an id that never existed, or anything past Telegram's 48-hour limit simply
// fails and is counted.
//
// Ids are per-chat and close to sequential, which is what makes walking
// backwards from a known id find the earlier briefs at all. It is best-effort
// by nature: this is a cleanup tool, not a guarantee.
func (c *Client) SweepMessages(ctx context.Context, chatID, from int64, window int) (deleted, failed int) {
	for id := from; id > from-int64(window) && id > 0; id-- {
		if ctx.Err() != nil {
			return deleted, failed
		}
		if err := c.call(ctx, "deleteMessage", deleteMessageRequest{ChatID: chatID, MessageID: id}, nil); err != nil {
			failed++
			continue
		}
		deleted++
	}
	return deleted, failed
}

// DeleteMessages removes messages the bot sent earlier.
//
// Failures are counted, not returned. Telegram only lets a bot delete its own
// messages for 48 hours, and a message the reader already deleted is gone
// anyway; neither is a reason to abandon delivery of the new brief.
func (c *Client) DeleteMessages(ctx context.Context, chatID int64, ids []int64) (deleted int, failed int) {
	for _, id := range ids {
		err := c.call(ctx, "deleteMessage", deleteMessageRequest{ChatID: chatID, MessageID: id}, nil)
		if err != nil {
			failed++
			continue
		}
		deleted++
	}
	return deleted, failed
}

// Me returns the bot's username, which is the cheapest way to prove the token
// and network path both work without sending anything to a chat.
func (c *Client) Me(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := c.call(ctx, "getMe", nil, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

type chatRequest struct {
	ChatID int64 `json:"chat_id"`
}

type chatMemberRequest struct {
	ChatID int64 `json:"chat_id"`
	UserID int64 `json:"user_id"`
}

// CanPost reports a channel's title and whether the bot may post there, which
// takes being its creator or an admin with the right to post. It sends
// nothing, so a check can find a missing permission before the evening brief
// does.
func (c *Client) CanPost(ctx context.Context, chatID int64) (title string, ok bool, err error) {
	var me struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, "getMe", nil, &me); err != nil {
		return "", false, err
	}
	var chat struct {
		Title string `json:"title"`
	}
	if err := c.call(ctx, "getChat", chatRequest{ChatID: chatID}, &chat); err != nil {
		return "", false, err
	}
	var member struct {
		Status          string `json:"status"`
		CanPostMessages bool   `json:"can_post_messages"`
	}
	if err := c.call(ctx, "getChatMember", chatMemberRequest{ChatID: chatID, UserID: me.ID}, &member); err != nil {
		return chat.Title, false, err
	}
	ok = member.Status == "creator" || (member.Status == "administrator" && member.CanPostMessages)
	return chat.Title, ok, nil
}

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries(); attempt++ {
		result, retryAfter, err := c.do(ctx, method, body)
		if err == nil {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(result, out); err != nil {
				return fmt.Errorf("telegram %s: decode result: %w", method, err)
			}
			return nil
		}

		lastErr = err
		if retryAfter <= 0 {
			return err // not retryable
		}
		if err := c.sleep(ctx, retryAfter); err != nil {
			return err
		}
	}
	return fmt.Errorf("telegram %s: giving up after %d retries: %w", method, c.maxRetries(), lastErr)
}

// do returns a positive retryAfter when the failure is worth another attempt.
func (c *Client) do(ctx context.Context, method string, body any) (json.RawMessage, time.Duration, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("telegram %s: encode request: %w", method, err)
		}
		payload = bytes.NewReader(encoded)
	}

	url := fmt.Sprintf("%s/bot%s/%s", c.baseURL(), c.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, 0, fmt.Errorf("telegram %s: build request: %w", method, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, c.redact(err) // shutting down, not a failure
		}
		// A dropped connection is worth another attempt. This is not
		// hypothetical: a reset here discarded a brief that had already cost a
		// full summarization, because delivery was the last step.
		return nil, time.Second, c.redact(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("telegram %s: read response: %w", method, err)
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("telegram %s: http %s: %w", method, resp.Status, err)
	}
	if parsed.OK {
		return parsed.Result, 0, nil
	}

	apiErr := &apiError{Method: method, Code: parsed.ErrorCode, Description: parsed.Description}

	// 429 carries the delay to honour; 5xx is Telegram's own trouble and is
	// worth one more try. Everything else -- a bad token, an unknown chat -- is
	// permanent, and retrying only delays the error the operator needs to see.
	switch {
	case parsed.ErrorCode == http.StatusTooManyRequests && parsed.Parameters != nil:
		wait := time.Duration(parsed.Parameters.RetryAfter) * time.Second
		if wait <= 0 {
			wait = time.Second
		}
		return nil, min(wait, maxRetryAfter), apiErr
	case parsed.ErrorCode >= 500:
		return nil, time.Second, apiErr
	default:
		return nil, 0, apiErr
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// redact strips the bot token out of an error.
//
// The token sits in the request path, and net/http puts the full URL into
// every transport error, so an ordinary connection reset prints a working
// credential into the logs. Anything that reaches an operator's terminal or a
// platform's log store has to have it removed first.
func (c *Client) redact(err error) error {
	if err == nil || c.Token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), c.Token, "[token]")
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) maxRetries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return defaultMaxRetries
}
