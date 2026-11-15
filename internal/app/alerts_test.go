package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/alerts"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
)

// alertModel answers each check with the reply it holds, or fails, and
// counts the checks that reached it.
type alertModel struct {
	mu    sync.Mutex
	reply string
	fail  bool
	asked int
}

func (m *alertModel) Complete(_ context.Context, _, _ string) (string, model.Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked++
	if m.fail {
		return "", model.Usage{}, errors.New("plan limit reached")
	}
	return m.reply, model.Usage{}, nil
}

// alertFeed serves an RSS feed whose items can be changed between checks.
func alertFeed(t *testing.T) (*httptest.Server, func(items ...string)) {
	t.Helper()
	var (
		mu   sync.Mutex
		body string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Wire</title>` + body + `</channel></rss>`))
	}))
	t.Cleanup(srv.Close)
	set := func(items ...string) {
		mu.Lock()
		defer mu.Unlock()
		body = strings.Join(items, "")
	}
	return srv, set
}

func rssItem(title, link, published string) string {
	return "<item><title>" + title + "</title><link>" + link + "</link><description>" + title +
		"</description><pubDate>" + published + "</pubDate></item>"
}

// The first look takes note of what is already in the feeds. After that, a
// new story the model picks reaches the owner once, with its source; the
// same story is not looked at again; a check that fails looks again next
// time; and paused alerts look at nothing.
func TestAlertsSendBigNewsOnce(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	// Monday 16 November 2026, 13:00 UTC: 21:00 in Singapore.
	now := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }

	srv, set := alertFeed(t)
	a.prefs.Sources = []model.Source{{ID: "wire", Name: "Wire", URL: srv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: srv.Client(), Now: a.Now}
	log, err := alerts.Load(filepath.Join(t.TempDir(), "alerts.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.AlertLog = log
	m := &alertModel{reply: "ALERT: 1\nHEADLINE: The Fed cut rates by half a point in an emergency move\nWHAT: Borrowing gets cheaper at once."}
	a.Alerts = &alerts.Picker{Completer: m}
	ctx := context.Background()

	old := rssItem("Markets drift ahead of data", "https://wire.example/drift", "Mon, 16 Nov 2026 12:40:00 GMT")
	set(old)
	a.checkAlerts(ctx)
	if m.asked != 0 || len(*sent) != 0 {
		t.Fatalf("the first look asked the model %d times and sent %d messages, want neither", m.asked, len(*sent))
	}

	fed := rssItem("Fed cuts rates in emergency move", "https://wire.example/fed", "Mon, 16 Nov 2026 12:58:00 GMT")
	set(old, fed)
	m.fail = true
	a.checkAlerts(ctx)
	if m.asked != 1 || len(*sent) != 0 {
		t.Fatalf("a failed check: asked %d, sent %d", m.asked, len(*sent))
	}

	// While a brief is being written, the check waits for the next one.
	m.fail = false
	a.running.Lock()
	a.checkAlerts(ctx)
	a.running.Unlock()
	if m.asked != 1 || len(*sent) != 0 {
		t.Fatalf("a check during a brief: asked %d, sent %d; want it left for the next", m.asked, len(*sent))
	}

	a.checkAlerts(ctx)
	if m.asked != 2 || len(*sent) != 1 {
		t.Fatalf("the check after a failure: asked %d, sent %d; want the story looked at again and sent", m.asked, len(*sent))
	}
	got := lastText(sent)
	for _, want := range []string{"🚨 <b>The Fed cut rates", "Borrowing gets cheaper", `<a href="https://wire.example/fed">Wire</a>`, "20:58 Singapore time"} {
		if !strings.Contains(got, want) {
			t.Errorf("the alert is missing %q:\n%s", want, got)
		}
	}

	a.checkAlerts(ctx)
	if m.asked != 2 || len(*sent) != 1 {
		t.Errorf("the same stories were looked at again: asked %d, sent %d", m.asked, len(*sent))
	}

	handle(a, message("/alerts"))
	if got := lastText(sent); !strings.Contains(got, "Today's, 1:") || !strings.Contains(got, "21:00 The Fed cut rates") {
		t.Errorf("/alerts: %q", got)
	}
	handle(a, message("/alerts off"))
	set(old, fed, rssItem("Nvidia agrees to buy Arm", "https://wire.example/arm", "Mon, 16 Nov 2026 12:59:00 GMT"))
	before := len(*sent)
	a.checkAlerts(ctx)
	if m.asked != 2 || len(*sent) != before {
		t.Errorf("paused alerts still checked: asked %d", m.asked)
	}
}

// Once the day's allowance is sent, nothing more goes out that day.
func TestAlertsStopAtTheDaysAllowance(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	now := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }
	srv, set := alertFeed(t)
	a.prefs.Sources = []model.Source{{ID: "wire", Name: "Wire", URL: srv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: srv.Client(), Now: a.Now}
	log, err := alerts.Load(filepath.Join(t.TempDir(), "alerts.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.AlertLog = log
	_ = log.MarkSeen(nil, now.Add(-time.Hour))
	for i := 0; i < alerts.PerDay-1; i++ {
		_ = log.Record(alerts.Sent{At: now.Add(-time.Duration(i+1) * time.Minute), Headline: "earlier"})
	}
	m := &alertModel{reply: "ALERT: 1\nHEADLINE: One\nWHAT: x\n\nALERT: 2\nHEADLINE: Two\nWHAT: y"}
	a.Alerts = &alerts.Picker{Completer: m}

	set(rssItem("One", "https://wire.example/1", "Mon, 16 Nov 2026 12:58:00 GMT"),
		rssItem("Two", "https://wire.example/2", "Mon, 16 Nov 2026 12:57:00 GMT"))
	a.checkAlerts(context.Background())
	if len(*sent) != 1 {
		t.Errorf("sent %d with one left of the day's allowance, want 1", len(*sent))
	}
	set(rssItem("Three", "https://wire.example/3", "Mon, 16 Nov 2026 12:59:00 GMT"))
	a.checkAlerts(context.Background())
	if m.asked != 1 || len(*sent) != 1 {
		t.Errorf("with the allowance used: asked %d, sent %d; want no check and nothing sent", m.asked, len(*sent))
	}
}
