package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func testReport() model.Report {
	return model.Report{
		GeneratedAt:  time.Date(2026, 9, 9, 20, 30, 0, 0, time.UTC),
		Overview:     "Chips led the tape.\n\nBreadth was narrow.",
		ArticleCount: 42,
		SourceCount:  7,
		Sections: []model.Section{
			{
				GroupID: "semis-ai", GroupName: "Semiconductors & AI", Body: "NVDA carried the group.",
				Articles: []model.Article{
					{Title: "NVDA beats", URL: "https://example.com/a?x=1&y=2", SourceName: "CNBC"},
				},
			},
		},
	}
}

func TestRenderEscapesMarkupInEveryField(t *testing.T) {
	rep := testReport()
	rep.Overview = "Risk <off> & cautious"
	rep.Sections[0].Articles[0].Title = `AMD "beats" & raises`

	out := strings.Join(Render(rep, time.UTC), "\n")

	if strings.Contains(out, "<off>") {
		t.Error("raw angle brackets from model prose reached the message")
	}
	for _, want := range []string{
		"Risk &lt;off&gt; &amp; cautious",
		"AMD &quot;beats&quot; &amp; raises",
		"https://example.com/a?x=1&amp;y=2", // the href needs escaping too
		"Semiconductors &amp; AI",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderIncludesProseSectionsAndFooter(t *testing.T) {
	out := strings.Join(Render(testReport(), time.UTC), "\n")

	for _, want := range []string{
		"Market Watch",
		"Chips led the tape.",
		"Breadth was narrow.",
		"<b>Semiconductors &amp; AI</b>",
		"NVDA carried the group.",
		`<a href="https://example.com/a?x=1&amp;y=2">NVDA beats</a>`,
		"<i>42 articles from 7 sources</i>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderUsesTheDisplayTimezone(t *testing.T) {
	sgt, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	// 20:30 UTC on the 9th is the morning of the 10th in Singapore, which is
	// when the reader actually sees it.
	if out := strings.Join(Render(testReport(), sgt), "\n"); !strings.Contains(out, "Thursday, 10 September 2026") {
		t.Errorf("wrong local date in:\n%s", out)
	}
}

func TestRenderCapsLinksPerSection(t *testing.T) {
	rep := testReport()
	for i := 0; i < 9; i++ {
		rep.Sections[0].Articles = append(rep.Sections[0].Articles,
			model.Article{Title: "Story", URL: "https://example.com/x", SourceName: "Wire"})
	}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if got := strings.Count(out, "<a href="); got != maxLinksPerSection {
		t.Errorf("rendered %d links, want the cap of %d", got, maxLinksPerSection)
	}
	if !strings.Contains(out, "+5 more") {
		t.Errorf("the trimmed links are not accounted for:\n%s", out)
	}
}

// Telegram rejects an over-long message outright, so a heavy day must split
// rather than fail.
func TestRenderSplitsLongReportsWithoutTearingTags(t *testing.T) {
	rep := testReport()
	rep.Overview = strings.TrimSpace(strings.Repeat("The market moved on heavy volume today. ", 400))

	messages := Render(rep, time.UTC)
	if len(messages) < 2 {
		t.Fatalf("got %d messages, want the long report split", len(messages))
	}
	for i, m := range messages {
		if got := len([]rune(m)); got > maxMessageRunes {
			t.Errorf("message %d is %d runes, over the %d limit", i, got, maxMessageRunes)
		}
		if strings.Count(m, "<a ") != strings.Count(m, "</a>") {
			t.Errorf("message %d has an unbalanced anchor tag", i)
		}
	}
}

func TestRenderKeepsAnAnchorWhole(t *testing.T) {
	// A section whose links land exactly at a boundary must not be cut inside
	// the href, which would send Telegram invalid HTML.
	rep := testReport()
	rep.Overview = strings.TrimSpace(strings.Repeat("Filler prose. ", 290))

	for _, m := range Render(rep, time.UTC) {
		if strings.Contains(m, "<a href=") && !strings.Contains(m, "</a>") {
			t.Errorf("an anchor was split across messages:\n%s", m)
		}
	}
}

// newTestClient points a client at a stub server.
func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		Token:   "test-token",
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Sleep:   func(context.Context, time.Duration) error { return nil }, // no real waiting
	}
}

func TestSendMessagePostsHTMLWithPreviewsOff(t *testing.T) {
	var got sendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/bottest-token/sendMessage") {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv).SendMessage(context.Background(), 99, "<b>hi</b>"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got.ChatID != 99 || got.Text != "<b>hi</b>" {
		t.Errorf("request = %+v", got)
	}
	if got.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", got.ParseMode)
	}
	if !got.DisableWebPagePreview {
		t.Error("link previews are on; the first article would bury the brief")
	}
}

func TestSendMessageHonoursRetryAfterThenSucceeds(t *testing.T) {
	var calls int
	var waited time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.Sleep = func(_ context.Context, d time.Duration) error { waited = d; return nil }

	if err := c.SendMessage(context.Background(), 1, "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d calls, want a retry", calls)
	}
	if waited != 7*time.Second {
		t.Errorf("waited %s, want the server's 7s", waited)
	}
}

// A bad token or unknown chat will never succeed; retrying only delays the
// error the operator needs to see.
func TestSendMessageDoesNotRetryPermanentFailures(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).SendMessage(context.Background(), 1, "hi")
	if err == nil {
		t.Fatal("SendMessage succeeded on a 401")
	}
	if calls != 1 {
		t.Errorf("made %d calls, want exactly 1", calls)
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("error %q does not carry the API description", err)
	}
}

func TestSendReportStopsAtTheFirstFailure(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).SendReport(context.Background(), 1, []string{"one", "two", "three"})
	if err == nil {
		t.Fatal("SendReport succeeded despite a failing part")
	}
	if calls != 2 {
		t.Errorf("made %d calls, want it to stop at the failure", calls)
	}
	if !strings.Contains(err.Error(), "part 2 of 3") {
		t.Errorf("error %q does not say which part failed", err)
	}
}

func TestMeReturnsTheBotUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"username":"market_watch_bot"}}`))
	}))
	defer srv.Close()

	got, err := newTestClient(srv).Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got != "market_watch_bot" {
		t.Errorf("Me() = %q", got)
	}
}
