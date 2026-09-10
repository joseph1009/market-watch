package telegram

import (
	"context"
	"encoding/json"
	"fmt"
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

// The unlabelled link list read as stray text, and "+3 more" looked like part
// of the brief rather than a note about the sources.
func TestRenderLabelsTheSourceList(t *testing.T) {
	out := strings.Join(Render(testReport(), time.UTC), "\n")
	if !strings.Contains(out, "<i>Sources</i>") {
		t.Errorf("the link list is unlabelled:\n%s", out)
	}
}

func TestRenderTurnsModelListsIntoBullets(t *testing.T) {
	rep := testReport()
	rep.Overview = "Three moves stood out:\n- Meta rose on Muse\n- Qualcomm gained 9%\n- Apple slipped"

	out := strings.Join(Render(rep, time.UTC), "\n")
	if strings.Contains(out, "\n- Meta") {
		t.Errorf("raw list markers survived:\n%s", out)
	}
	if got := strings.Count(out, "• "); got < 3 {
		t.Errorf("got %d bullets, want the 3 list items rendered:\n%s", got, out)
	}
}

func TestRenderBoldsTopicLabels(t *testing.T) {
	rep := testReport()
	rep.Overview = "Oil - Brent topped $100 for the first time since July.\n\n" +
		"Fed — the committee is split three ways going into September."

	out := strings.Join(Render(rep, time.UTC), "\n")
	for _, want := range []string{"<b>Oil</b> - Brent topped", "<b>Fed</b> — the committee"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Bolding a fragment of a sentence reads worse than no label at all, so an
// ambiguous block is left exactly as written.
func TestRenderLeavesSentencesWithDashesAlone(t *testing.T) {
	rep := testReport()
	rep.Overview = "The Dow fell 350 points - its worst session in weeks - as crude climbed.\n\n" +
		"Investors who had positioned for a cut, and were caught out - are now rethinking."

	out := strings.Join(Render(rep, time.UTC), "\n")
	if strings.Contains(out, "<b>The Dow fell 350 points</b>") {
		t.Errorf("bolded a sentence clause:\n%s", out)
	}
	if strings.Contains(out, "<b>") && strings.Contains(out, "caught out</b>") {
		t.Errorf("bolded a long fragment:\n%s", out)
	}
}

func TestRenderDoesNotLabelListItems(t *testing.T) {
	rep := testReport()
	rep.Overview = "- Waller - comfortable holding steady\n- Hammack - wants to act now"

	out := strings.Join(Render(rep, time.UTC), "\n")
	if strings.Contains(out, "<b>") && strings.Contains(out, "• <b>") {
		t.Errorf("a bullet was given a bold label:\n%s", out)
	}
	if !strings.Contains(out, "• Waller - comfortable") {
		t.Errorf("bullet lost its shape:\n%s", out)
	}
}

func TestIsLabelRejectsProse(t *testing.T) {
	labels := []string{"Oil", "Fed", "Big Tech", "Treasury and the yen"}
	for _, s := range labels {
		if !isLabel(s) {
			t.Errorf("isLabel(%q) = false, want true", s)
		}
	}
	prose := []string{
		"",
		"The Dow fell 350 points",                // determiner, digits, too many words
		"Dow fell 350 points",                    // a figure means a sentence is under way
		"The oil trade",                          // opens with a determiner
		"Investors, having positioned for a cut", // comma
		"One thing mattered today:",              // colon
		strings.Repeat("x", maxLabelRunes+1),     // too long
		"Brent topped $100 for the first time since July this year", // too long and too many words
	}
	for _, s := range prose {
		if isLabel(s) {
			t.Errorf("isLabel(%q) = true, want false", s)
		}
	}
}

func TestRenderSeparatesSections(t *testing.T) {
	out := strings.Join(Render(testReport(), time.UTC), "\n")
	if !strings.Contains(out, divider) {
		t.Errorf("no visual break between sections:\n%s", out)
	}
	if !strings.Contains(out, "<b>Overview</b>") {
		t.Errorf("the overview is unlabelled:\n%s", out)
	}
}

func TestRenderShowsTokenUsageAndCost(t *testing.T) {
	rep := testReport()
	rep.Usage = model.Usage{InputTokens: 21_450, OutputTokens: 3_204, EstimatedUSD: 0.187}

	out := strings.Join(Render(rep, time.UTC), "\n")
	for _, want := range []string{"21,450 in", "3,204 out", "~$0.187"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer is missing %q in:\n%s", want, out)
		}
	}
}

// An unpriced model must not render a "$0.000" that reads as free.
func TestRenderOmitsCostWhenUnpriced(t *testing.T) {
	rep := testReport()
	rep.Usage = model.Usage{InputTokens: 100, OutputTokens: 50}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if strings.Contains(out, "$") {
		t.Errorf("rendered a price with no estimate available:\n%s", out)
	}
	if !strings.Contains(out, "100 in") {
		t.Errorf("token counts were dropped along with the price:\n%s", out)
	}
}

func TestRenderOmitsUsageEntirelyWhenUnrecorded(t *testing.T) {
	out := strings.Join(Render(testReport(), time.UTC), "\n")
	if strings.Contains(out, " in · ") {
		t.Errorf("rendered a usage line with no usage recorded:\n%s", out)
	}
}

func TestThousandsGroupsDigits(t *testing.T) {
	tests := map[int64]string{0: "0", 42: "42", 999: "999", 1000: "1,000", 21450: "21,450", 1234567: "1,234,567"}
	for in, want := range tests {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderCapsLinksPerSection(t *testing.T) {
	rep := testReport()
	for i := 0; i < 12; i++ {
		rep.Sections[0].Articles = append(rep.Sections[0].Articles,
			model.Article{Title: "Story", URL: "https://example.com/x", SourceName: "Wire"})
	}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if got := strings.Count(out, "<a href="); got != maxLinksPerSection {
		t.Errorf("rendered %d links, want the cap of %d", got, maxLinksPerSection)
	}
	if !strings.Contains(out, "+5 more") { // 13 articles, 8 shown
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

	_, err := newTestClient(srv).SendReport(context.Background(), 1, []string{"one", "two", "three"})
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

func TestSendReportReturnsTheMessageIDs(t *testing.T) {
	var next int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next++
		_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":true,"result":{"message_id":%d}}`, 100+next)))
	}))
	defer srv.Close()

	ids, err := newTestClient(srv).SendReport(context.Background(), 1, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("SendReport: %v", err)
	}
	if len(ids) != 3 || ids[0] != 101 || ids[2] != 103 {
		t.Errorf("ids = %v, want [101 102 103]", ids)
	}
}

// A partial send still has to report what landed, or the delivered parts can
// never be cleaned up.
func TestSendReportReturnsIDsEvenWhenItFailsPartway(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 3 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request"}`))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":true,"result":{"message_id":%d}}`, 200+calls)))
	}))
	defer srv.Close()

	ids, err := newTestClient(srv).SendReport(context.Background(), 1, []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("SendReport succeeded despite a failing part")
	}
	if len(ids) != 2 {
		t.Errorf("ids = %v, want the 2 parts that landed", ids)
	}
}

// Telegram refuses to delete anything older than 48 hours, and a message the
// reader already removed is gone. Neither may stop the new brief.
func TestDeleteMessagesCountsFailuresWithoutStopping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req deleteMessageRequest
		_ = json.Unmarshal(body, &req)
		if req.MessageID == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"message can't be deleted"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	deleted, failed := newTestClient(srv).DeleteMessages(context.Background(), 1, []int64{1, 2, 3})
	if deleted != 2 || failed != 1 {
		t.Errorf("deleted=%d failed=%d, want 2 and 1", deleted, failed)
	}
}

// The backlog problem: briefs sent before ids were recorded can only be reached
// by walking id space backwards.
func TestSweepMessagesWalksBackwardsAndTolerates(t *testing.T) {
	var attempted []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req deleteMessageRequest
		_ = json.Unmarshal(body, &req)
		attempted = append(attempted, req.MessageID)
		// Only even ids belong to the bot; the rest are the reader's messages
		// or ids that never existed, which Telegram refuses.
		if req.MessageID%2 != 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"message to delete not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	deleted, failed := newTestClient(srv).SweepMessages(context.Background(), 1, 10, 4)
	if deleted != 2 || failed != 2 {
		t.Errorf("deleted=%d failed=%d, want 2 and 2", deleted, failed)
	}
	if len(attempted) != 4 || attempted[0] != 10 || attempted[3] != 7 {
		t.Errorf("attempted %v, want 10 down to 7", attempted)
	}
}

// Ids below 1 do not exist; the walk must stop rather than count failures.
func TestSweepMessagesStopsAtTheStartOfTheChat(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	newTestClient(srv).SweepMessages(context.Background(), 1, 3, 100)
	if calls != 3 {
		t.Errorf("made %d calls, want 3 (ids 3, 2, 1)", calls)
	}
}

// The quiet block closes the last section's prose, above its links, so the
// brief ends on somewhere to read next rather than on a note about absence.
func TestRenderPutsQuietAboveTheLastSectionsSources(t *testing.T) {
	rep := testReport()
	rep.Usage = model.Usage{InputTokens: 1000, OutputTokens: 500, EstimatedUSD: 0.02}
	rep.QuietGroups = []string{"Energy", "Crypto & Digital Assets"}

	out := strings.Join(Render(rep, time.UTC), "\n")

	quiet := strings.Index(out, "<b>Quiet today</b>")
	sources := strings.Index(out, "<i>Sources</i>")
	body := strings.Index(out, "NVDA carried the group.")
	stats := strings.Index(out, "articles from")

	if quiet < 0 || sources < 0 {
		t.Fatalf("quiet or sources block missing:\n%s", out)
	}
	if quiet < body {
		t.Errorf("the quiet block came before the section prose:\n%s", out)
	}
	if quiet > sources {
		t.Errorf("the quiet block came after the source links; it belongs above them:\n%s", out)
	}
	if quiet > stats {
		t.Errorf("the quiet block fell below the statistics:\n%s", out)
	}
	if !strings.Contains(out, "Energy, Crypto &amp; Digital Assets — nothing that warranted a section.") {
		t.Errorf("quiet groups not listed:\n%s", out)
	}
}

// Only the final section carries it, or it repeats down the brief.
func TestRenderShowsTheQuietBlockOnce(t *testing.T) {
	rep := testReport()
	rep.Sections = append(rep.Sections, model.Section{
		GroupID: "macro-rates", GroupName: "Macro & Rates", Body: "Rates did the work.",
		Articles: []model.Article{{Title: "Fed holds", URL: "https://example.com/f", SourceName: "CNBC"}},
	})
	rep.QuietGroups = []string{"Energy"}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if got := strings.Count(out, "<b>Quiet today</b>"); got != 1 {
		t.Errorf("the quiet block appears %d times, want once", got)
	}
	// And on the last section, not the first.
	if strings.Index(out, "<b>Quiet today</b>") < strings.Index(out, "Rates did the work.") {
		t.Errorf("the quiet block landed on the wrong section:\n%s", out)
	}
}

// A day where nothing cleared the threshold is when it matters most.
func TestRenderShowsQuietWithNoSectionsAtAll(t *testing.T) {
	rep := testReport()
	rep.Sections = nil
	rep.QuietGroups = []string{"Energy", "Big Tech"}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "<b>Quiet today</b>") {
		t.Errorf("the quiet block vanished when there were no sections:\n%s", out)
	}
}

func TestRenderOmitsQuietWhenEveryWatchlistHadNews(t *testing.T) {
	out := strings.Join(Render(testReport(), time.UTC), "\n")
	if strings.Contains(out, "Quiet today") {
		t.Errorf("rendered a quiet block with nothing quiet:\n%s", out)
	}
}

// Without a published menu the commands are invisible: typing "/" offers
// nothing and the only way to find them is to be told.
func TestSetMyCommandsPublishesTheMenu(t *testing.T) {
	var got setMyCommandsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/setMyCommands") {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).SetMyCommands(context.Background(), []Command{
		{Command: "now", Description: "Build a brief"},
	})
	if err != nil {
		t.Fatalf("SetMyCommands: %v", err)
	}
	if len(got.Commands) != 1 || got.Commands[0].Command != "now" {
		t.Errorf("published %+v", got.Commands)
	}
}
