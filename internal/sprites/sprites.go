// Package sprites starts and stops a service on a Fly Sprite, for /code.
//
// The code is worked on from a Sprite of its own, market-watch-dev, apart from
// this machine. Claude Code's Remote Control server runs there as a service,
// so a session can be started and steered from the Claude app on a phone. A
// running service keeps a Sprite awake, and billed, so it is off until /code
// starts it, and it stops itself after a quiet spell (scripts/remote-control.sh).
//
// Only three calls of the Sprites API are used: a service's state, start and
// stop. Starting wakes the Sprite if it is asleep. See "The Sprite" in
// docs/RUNBOOK.md.
package sprites

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL = "https://api.sprites.dev"

	// readyWait is how long a start may take to say Remote Control is
	// connected. A sleeping Sprite wakes in a second or two, and Remote
	// Control connects a second after that.
	readyWait = 60 * time.Second
)

// Client reaches one service on one Sprite. An empty Token disables it.
type Client struct {
	Token   string
	Sprite  string
	Service string

	BaseURL string // the API's address; empty means api.sprites.dev
	HTTP    *http.Client
}

// Enabled reports whether there is a token to call the API with.
func (c *Client) Enabled() bool { return c != nil && c.Token != "" }

// State is a service's standing.
type State struct {
	Running bool
	Since   time.Time // when it started, if it is running
}

// State reads whether the service is running.
func (c *Client) State(ctx context.Context) (State, error) {
	resp, err := c.call(ctx, http.MethodGet, "", nil)
	if err != nil {
		return State{}, err
	}
	defer resp.Body.Close()
	var body struct {
		State struct {
			Status    string    `json:"status"`
			StartedAt time.Time `json:"started_at"`
		} `json:"state"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return State{}, fmt.Errorf("sprites: read the service's state: %w", err)
	}
	s := State{Running: body.State.Status == "running"}
	if s.Running {
		s.Since = body.State.StartedAt
	}
	return s, nil
}

// Start starts the service, waking the Sprite if it sleeps, and waits until
// its output says ready, which Remote Control prints once it is connected.
// It reports whether that was seen before the output ended. A service that
// is already running is left as it is, and says nothing.
func (c *Client) Start(ctx context.Context, ready string) (bool, error) {
	q := url.Values{"duration": {fmt.Sprint(int(readyWait.Seconds()))}}
	resp, err := c.call(ctx, http.MethodPost, "/start", q)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// The reply is a stream of events, one JSON object a line, for as long
	// as the duration asked for. It is read only until the word is seen.
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 64<<10), 1<<20)
	for lines.Scan() {
		var ev struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			Error    string `json:"error"`
			Message  string `json:"message"`
			ExitCode *int   `json:"exit_code"`
		}
		if json.Unmarshal(lines.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "stdout", "stderr":
			if strings.Contains(ev.Data, ready) {
				return true, nil
			}
		case "error":
			return false, fmt.Errorf("sprites: the service did not start: %s", firstOf(ev.Error, ev.Message, "no reason given"))
		case "exit", "stopped":
			code := -1
			if ev.ExitCode != nil {
				code = *ev.ExitCode
			}
			return false, fmt.Errorf("sprites: the service exited with status %d as it started", code)
		case "complete":
			return false, nil
		}
	}
	return false, lines.Err()
}

// Stop stops the service, after which the Sprite goes back to sleep. It
// reports whether the service was running.
func (c *Client) Stop(ctx context.Context) (bool, error) {
	resp, err := c.call(ctx, http.MethodPost, "/stop", nil)
	var status *statusError
	if errors.As(err, &status) && status.code == http.StatusConflict {
		return false, nil // "service is not running"
	}
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // until it has stopped
	return true, nil
}

// statusError is a reply other than success.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("sprites: http %d: %s", e.code, e.body)
}

// call sends one request about the service. A reply other than success is
// returned as an error, with the start of its body.
func (c *Client) call(ctx context.Context, method, action string, query url.Values) (*http.Response, error) {
	if !c.Enabled() {
		return nil, errors.New("sprites: no SPRITES_TOKEN")
	}
	base := c.BaseURL
	if base == "" {
		base = baseURL
	}
	addr := fmt.Sprintf("%s/v1/sprites/%s/services/%s%s",
		strings.TrimRight(base, "/"), url.PathEscape(c.Sprite), url.PathEscape(c.Service), action)
	if len(query) > 0 {
		addr += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, addr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, &statusError{code: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}
	return resp, nil
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
