// Package telegram delivers the daily brief over the Telegram Bot API.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

// SendMessage delivers one HTML-formatted message.
//
// Link previews are suppressed: a brief carries many article links, and
// Telegram would otherwise expand the first one into a card that buries the
// prose the reader actually wants.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string) error {
	return c.call(ctx, "sendMessage", sendMessageRequest{
		ChatID:                chatID,
		Text:                  html,
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
	}, nil)
}

// SendReport delivers the rendered brief in order, stopping at the first
// failure so a half-sent report is not compounded by later parts arriving out
// of context.
func (c *Client) SendReport(ctx context.Context, chatID int64, messages []string) error {
	for i, m := range messages {
		if err := c.SendMessage(ctx, chatID, m); err != nil {
			return fmt.Errorf("send part %d of %d: %w", i+1, len(messages), err)
		}
	}
	return nil
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
		return nil, 0, err // transport failures are not retried here; the caller reruns
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
