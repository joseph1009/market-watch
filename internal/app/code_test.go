package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/sprites"
)

// sprite stands in for the Sprites API: the remote-control service, off
// until started.
func sprite(t *testing.T, a *App) *[]string {
	t.Helper()
	var (
		mu      sync.Mutex
		running bool
		asked   []string
		version = "2.1.296"
	)
	const service = "/v1/sprites/dev/services/remote-control"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.Method+" "+strings.TrimPrefix(r.URL.Path, service))
		switch r.Method + " " + r.URL.Path {
		case "GET " + service:
			status := "stopped"
			if running {
				status = "running"
			}
			_, _ = w.Write([]byte(`{"state":{"status":"` + status + `","started_at":"2026-09-10T11:30:00Z"}}`))
		case "POST " + service + "/start":
			running = true
			_, _ = w.Write([]byte(`{"type":"started"}` + "\n" + `{"type":"stdout","data":"·✔︎· Connected · market-watch · main\n"}` + "\n"))
		case "POST /v1/sprites/dev/exec":
			// Claude Code, 2.1.296 until updated.
			cmd := strings.Join(r.URL.Query()["cmd"], " ")
			switch cmd {
			case "bash -lc claude --version":
				_, _ = w.Write([]byte("\x01" + version + " (Claude Code)\n\x03\x00"))
			case "bash -lc claude update":
				version = "2.1.300"
				_, _ = w.Write([]byte("\x01Successfully updated\n\x03\x00"))
			default:
				http.Error(w, "unexpected command "+cmd, http.StatusBadRequest)
			}
		case "POST " + service + "/stop":
			if !running {
				http.Error(w, "service is not running", http.StatusConflict)
				return
			}
			running = false
			_, _ = w.Write([]byte(`{"type":"stopped","exit_code":0}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a.Code = &sprites.Client{Token: "tok", Sprite: "dev", Service: "remote-control", BaseURL: srv.URL, HTTP: srv.Client()}
	return &asked
}

func owned(t *testing.T, a *App) {
	t.Helper()
	if err := a.UpdatePrefs(func(p *config.Prefs) error { p.ChatID = 4242; return nil }); err != nil {
		t.Fatal(err)
	}
}

func lastText(sent *[]sentMessage) string {
	if len(*sent) == 0 {
		return ""
	}
	return (*sent)[len(*sent)-1].Text
}

func TestCodeTurnsRemoteControlOnAndOff(t *testing.T) {
	a, sent := newTestApp(t)
	owned(t, a)
	asked := sprite(t, a)

	handle(a, message("/code"))
	if got := lastText(sent); !strings.Contains(got, "Remote Control is on") || !strings.Contains(got, "market-watch") {
		t.Errorf("after /code: %q", got)
	}

	handle(a, message("/code"))
	if got := lastText(sent); !strings.Contains(got, "already on, since 19:30") {
		t.Errorf("after a second /code: %q, want it already on since 19:30 Singapore time", got)
	}

	handle(a, message("/code stop"))
	if got := lastText(sent); !strings.Contains(got, "Remote Control is off") {
		t.Errorf("after /code stop: %q", got)
	}
	handle(a, message("/code stop"))
	if got := lastText(sent); !strings.Contains(got, "already off") {
		t.Errorf("after a second /code stop: %q", got)
	}

	want := []string{"GET ", "POST /start", "GET ", "POST /stop", "POST /stop"}
	if strings.Join(*asked, ",") != strings.Join(want, ",") {
		t.Errorf("asked the API %q, want %q", *asked, want)
	}
}

// A session on the Sprite can change the code and deploy it, so /code is the
// owner's alone, even for a chat allowed to control the brief.
func TestCodeIsTheOwnersOnly(t *testing.T) {
	a, sent := newTestApp(t)
	if err := a.UpdatePrefs(func(p *config.Prefs) error { p.ChatID = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	a.Cfg.TelegramControlChats = []int64{4242}
	asked := sprite(t, a)

	handle(a, message("/code"))
	if len(*asked) != 0 {
		t.Errorf("a control chat reached the Sprite: %v", *asked)
	}
	if got := lastText(sent); !strings.Contains(got, "owner's chat only") {
		t.Errorf("reply %q, want a refusal", got)
	}
}

func TestCodeWithoutATokenSaysWhatItNeeds(t *testing.T) {
	a, sent := newTestApp(t)
	owned(t, a)

	handle(a, message("/code"))
	if got := lastText(sent); !strings.Contains(got, "SPRITES_TOKEN") {
		t.Errorf("reply %q, want it to name SPRITES_TOKEN", got)
	}
}

// Remote Control's word for connected changed between versions, and /code
// waits for it, so both are recognised, and connecting is not. The lines are
// as the Sprite's service log has them.
func TestRemoteReadyMatchesEitherVersion(t *testing.T) {
	for line, want := range map[string]bool{
		"·✔︎· Ready · market-watch · main":     true,
		"·✔︎· Connected · market-watch · main": true,
		"·|· Connecting · market-watch · main": false,
	} {
		if got := strings.Contains(line, remoteReady); got != want {
			t.Errorf("%q counts as ready: %v, want %v", line, got, want)
		}
	}
}

// /code update says when it is done, from which version to which, and that a
// running Remote Control needs a restart to use it.
func TestCodeUpdateSaysWhatChanged(t *testing.T) {
	a, sent := newTestApp(t)
	owned(t, a)
	sprite(t, a)

	handle(a, message("/code"))
	handle(a, message("/code update"))
	got := lastText(sent)
	for _, want := range []string{"from 2.1.296 to 2.1.300", "still running 2.1.296", "/code stop and then /code"} {
		if !strings.Contains(got, want) {
			t.Errorf("after /code update: %q, want %q in it", got, want)
		}
	}

	handle(a, message("/code update"))
	if got := lastText(sent); !strings.Contains(got, "already the newest version, 2.1.300") {
		t.Errorf("after a second /code update: %q", got)
	}
}
