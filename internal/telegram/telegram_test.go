package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		"<b>SEMICONDUCTORS &amp; AI</b>",
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
	if !strings.Contains(out, "<b>Sources</b>") {
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

// Bullets were once deliberately left unlabelled, on the grounds that a list
// reads cleanly without bold. A screenful of them says otherwise: every bullet
// opening with prose is a wall, and the label is what makes it skimmable. So
// the rule is now the same as for a paragraph -- bold a real label, leave
// anything else alone.
func TestRenderLabelsListItems(t *testing.T) {
	rep := testReport()
	rep.Overview = "- Waller - comfortable holding steady\n- Hammack - wants to act now"

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "• <b>Waller</b> - comfortable holding steady") {
		t.Errorf("a bullet label was not bolded:\n%s", out)
	}
	if !strings.Contains(out, "• <b>Hammack</b> - wants to act now") {
		t.Errorf("the second bullet lost its label:\n%s", out)
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
	if !strings.Contains(out, "<b>OVERVIEW</b>") {
		t.Errorf("the overview is unlabelled:\n%s", out)
	}
}

func TestRenderShowsTokenUsage(t *testing.T) {
	rep := testReport()
	rep.Usage = model.Usage{InputTokens: 21_450, OutputTokens: 3_204}

	out := strings.Join(Render(rep, time.UTC), "\n")
	for _, want := range []string{"21,450 in", "3,204 out"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer is missing %q in:\n%s", want, out)
		}
	}
	// Answered through a subscription, which is not billed by the token: a
	// price would be a figure nobody pays.
	if strings.Contains(out, "$") {
		t.Errorf("the footer quotes a price:\n%s", out)
	}
}

// Triage is a different, smaller model, so it gets its own line rather than
// being folded into the brief's tokens.
func TestRenderShowsTriageOnItsOwnLine(t *testing.T) {
	rep := testReport()
	rep.Usage = model.Usage{InputTokens: 21_450, OutputTokens: 3_204}
	rep.Triage = model.Usage{InputTokens: 32_328, OutputTokens: 4_397}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "<i>triage 32,328 in · 4,397 out</i>") {
		t.Errorf("footer is missing the triage line in:\n%s", out)
	}
	if !strings.Contains(out, "<i>21,450 in · 3,204 out</i>") {
		t.Errorf("the brief's own usage line changed in:\n%s", out)
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

// Posting to a channel takes admin rights with posting allowed; membership
// alone, or admin rights without posting, is not enough.
func TestCanPostNeedsAnAdminWhoMayPost(t *testing.T) {
	for _, tc := range []struct {
		member string
		want   bool
	}{
		{`{"status":"creator"}`, true},
		{`{"status":"administrator","can_post_messages":true}`, true},
		{`{"status":"administrator","can_post_messages":false}`, false},
		{`{"status":"member"}`, false},
		{`{"status":"left"}`, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/getMe"):
				_, _ = w.Write([]byte(`{"ok":true,"result":{"id":77,"username":"bot"}}`))
			case strings.HasSuffix(r.URL.Path, "/getChat"):
				_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-100,"title":"Market Watch"}}`))
			case strings.HasSuffix(r.URL.Path, "/getChatMember"):
				body, _ := io.ReadAll(r.Body)
				var req chatMemberRequest
				_ = json.Unmarshal(body, &req)
				if req.UserID != 77 {
					t.Errorf("asked about user %d, want the bot's own id", req.UserID)
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":` + tc.member + `}`))
			}
		}))

		title, ok, err := newTestClient(srv).CanPost(context.Background(), -100)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.member, err)
		}
		if ok != tc.want || title != "Market Watch" {
			t.Errorf("%s: ok=%v title=%q, want ok=%v", tc.member, ok, title, tc.want)
		}
	}
}

// A channel hears the first part of a brief and no more. The owner's own copy
// is unchanged: every part arrives as it always has.
func TestBroadcastRingsOnlyForTheFirstPart(t *testing.T) {
	var (
		mu     sync.Mutex
		silent []bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req sendMessageRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		silent = append(silent, req.DisableNotification)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).Broadcast(context.Background(), -100, []string{"a", "b", "c"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if got := fmt.Sprint(silent); got != "[false true true]" {
		t.Errorf("silent per part = %s, want only the first to ring", got)
	}

	silent = nil
	if _, err := newTestClient(srv).SendReport(context.Background(), 1, []string{"a", "b"}); err != nil {
		t.Fatalf("SendReport: %v", err)
	}
	if got := fmt.Sprint(silent); got != "[false false]" {
		t.Errorf("silent per part = %s, want the owner's copy to ring as before", got)
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
	rep.Usage = model.Usage{InputTokens: 1000, OutputTokens: 500}
	rep.QuietGroups = []string{"Energy", "Crypto & Digital Assets"}

	out := strings.Join(Render(rep, time.UTC), "\n")

	quiet := strings.Index(out, "<b>QUIET TODAY</b>")
	sources := strings.Index(out, "<b>Sources</b>")
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
	if got := strings.Count(out, "<b>QUIET TODAY</b>"); got != 1 {
		t.Errorf("the quiet block appears %d times, want once", got)
	}
	// And on the last section, not the first.
	if strings.Index(out, "<b>QUIET TODAY</b>") < strings.Index(out, "Rates did the work.") {
		t.Errorf("the quiet block landed on the wrong section:\n%s", out)
	}
}

// A day where nothing cleared the threshold is when it matters most.
func TestRenderShowsQuietWithNoSectionsAtAll(t *testing.T) {
	rep := testReport()
	rep.Sections = nil
	rep.QuietGroups = []string{"Energy", "Big Tech"}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "<b>QUIET TODAY</b>") {
		t.Errorf("the quiet block vanished when there were no sections:\n%s", out)
	}
}

func TestRenderOmitsQuietWhenEveryWatchlistHadNews(t *testing.T) {
	out := strings.Join(Render(testReport(), time.UTC), "\n")
	if strings.Contains(out, "QUIET TODAY") {
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

// The reading order the layout exists for: every category's analysis first,
// uninterrupted, then all the links together. Interleaved source lists made
// the reader skip past headlines to reach the next piece of prose.
func TestRenderPutsAllProseBeforeAllSources(t *testing.T) {
	rep := testReport()
	rep.Sections = append(rep.Sections, model.Section{
		GroupID: "macro-rates", GroupName: "Macro & Rates", Body: "Rates did the work.",
		Articles: []model.Article{{Title: "Fed holds", URL: "https://example.com/f", SourceName: "Reuters"}},
	})

	out := strings.Join(Render(rep, time.UTC), "\n")

	lastProse := strings.Index(out, "Rates did the work.")
	sourcesHeading := strings.Index(out, "<b>Sources</b>")
	firstLink := strings.Index(out, "<a href=")

	if sourcesHeading < 0 {
		t.Fatalf("no consolidated sources heading:\n%s", out)
	}
	if firstLink < lastProse {
		t.Errorf("a source link appears before the last section's prose:\n%s", out)
	}
	if sourcesHeading < lastProse {
		t.Errorf("the sources block starts before the prose ends:\n%s", out)
	}
	if strings.Count(out, "<b>Sources</b>") != 1 {
		t.Errorf("sources should be one block, found %d headings", strings.Count(out, "<b>Sources</b>"))
	}
}

// Links no longer sit under their prose, so each list is labelled with its
// category, and the categories keep the order the sections were written in.
func TestRenderGroupsSourcesByCategoryInSectionOrder(t *testing.T) {
	rep := testReport()
	rep.Sections = append(rep.Sections, model.Section{
		GroupID: "macro-rates", GroupName: "Macro & Rates", Body: "Rates did the work.",
		Articles: []model.Article{{Title: "Fed holds", URL: "https://example.com/f", SourceName: "Reuters"}},
	})

	out := strings.Join(Render(rep, time.UTC), "\n")
	sources := out[strings.Index(out, "<b>Sources</b>"):]

	semis := strings.Index(sources, "<i>Semiconductors &amp; AI</i>")
	macro := strings.Index(sources, "<i>Macro &amp; Rates</i>")
	if semis < 0 || macro < 0 {
		t.Fatalf("category labels missing from the sources block:\n%s", sources)
	}
	if semis > macro {
		t.Errorf("categories are out of section order in the sources block:\n%s", sources)
	}
	// Each link sits under its own category's label.
	if nvda := strings.Index(sources, "NVDA beats"); nvda < semis || nvda > macro {
		t.Errorf("the NVDA link is not under Semiconductors & AI:\n%s", sources)
	}
	if fed := strings.Index(sources, "Fed holds"); fed < macro {
		t.Errorf("the Fed link is not under Macro & Rates:\n%s", sources)
	}
}

// A category with no articles has nothing to cite and must not leave an empty
// label in the sources block.
func TestRenderSkipsCategoriesWithoutSources(t *testing.T) {
	rep := testReport()
	rep.Sections = append(rep.Sections, model.Section{
		GroupID: "energy", GroupName: "Energy", Body: "Quiet on oil.",
	})

	out := strings.Join(Render(rep, time.UTC), "\n")
	sources := out[strings.Index(out, "<b>Sources</b>"):]
	if strings.Contains(sources, "<i>Energy</i>") {
		t.Errorf("an empty category left a label in the sources block:\n%s", sources)
	}
}

// longReport builds a report with n sections, each around 1,200 runes over four
// paragraphs -- enough that a real brief spans several messages -- with markers
// identifying where each section opens and closes.
func longReport(n int) model.Report {
	rep := testReport()
	rep.Sections = nil
	filler := strings.Repeat("Supporting detail for the reader. ", 9)
	for i := 0; i < n; i++ {
		body := strings.Join([]string{
			fmt.Sprintf("Section %d opens. %s", i, filler),
			filler,
			filler,
			fmt.Sprintf("Section %d closes. %s", i, filler),
		}, "\n\n")
		rep.Sections = append(rep.Sections, model.Section{
			GroupID:   fmt.Sprintf("s%d", i),
			GroupName: fmt.Sprintf("Sector %d", i),
			Body:      body,
			Articles: []model.Article{
				{Title: fmt.Sprintf("Story %d", i), URL: fmt.Sprintf("https://example.com/%d", i), SourceName: "Wire"},
			},
		})
	}
	return rep
}

func messageContaining(msgs []string, needle string) int {
	for i, m := range msgs {
		if strings.Contains(m, needle) {
			return i
		}
	}
	return -1
}

// Sources are reference material, not reading, so they open a message of their
// own rather than trailing off the bottom of the last section.
func TestRenderStartsSourcesOnANewMessage(t *testing.T) {
	for _, rep := range []model.Report{testReport(), longReport(12)} {
		msgs := Render(rep, time.UTC)
		i := messageContaining(msgs, "<b>Sources</b>")
		if i < 0 {
			t.Fatal("no sources message rendered")
		}
		if !strings.HasPrefix(msgs[i], "<b>Sources</b>") {
			t.Errorf("message %d carries the sources heading but does not start with it:\n%s", i, msgs[i])
		}
		if i == 0 {
			t.Error("sources share the first message with the analysis")
		}
	}
}

// The break the reader saw: a section heading at the foot of one message and
// its prose opening the next. A section that fits in a message stays in one.
func TestRenderKeepsEachSectionInOneMessage(t *testing.T) {
	rep := longReport(12)
	msgs := Render(rep, time.UTC)
	if len(msgs) < 3 {
		t.Fatalf("got %d messages; the fixture should span several", len(msgs))
	}
	for i := range rep.Sections {
		heading := messageContaining(msgs, fmt.Sprintf("<b>SECTOR %d</b>", i))
		closes := messageContaining(msgs, fmt.Sprintf("Section %d closes.", i))
		if heading != closes {
			t.Errorf("Sector %d opens in message %d but closes in message %d", i, heading, closes)
		}
	}
}

// A message boundary already separates what is above from what is below; a
// divider as the first line of a message is a rule drawn under nothing.
func TestRenderNeverOpensAMessageWithADivider(t *testing.T) {
	for i, m := range Render(longReport(12), time.UTC) {
		if strings.HasPrefix(m, divider) {
			t.Errorf("message %d opens with a divider:\n%s", i, m[:min(len(m), 200)])
		}
	}
}

// Only a section too large for any single message is split, and even then its
// heading stays with its opening prose instead of ending a message alone.
func TestRenderKeepsAHeadingWithItsProseWhenASectionMustSplit(t *testing.T) {
	rep := testReport()
	var paras []string
	for i := 0; i < 30; i++ {
		paras = append(paras, fmt.Sprintf("Paragraph %d. %s", i, strings.Repeat("Detail here. ", 20)))
	}
	rep.Sections = []model.Section{{GroupID: "big", GroupName: "Big Section", Body: strings.Join(paras, "\n\n")}}

	msgs := Render(rep, time.UTC)
	heading := messageContaining(msgs, "<b>BIG SECTION</b>")
	if heading < 0 {
		t.Fatal("heading missing")
	}
	if first := messageContaining(msgs, "Paragraph 0."); first != heading {
		t.Errorf("heading in message %d but its first paragraph in message %d", heading, first)
	}
	for i, m := range msgs {
		if strings.HasSuffix(strings.TrimSpace(m), "<b>Big Section</b>") {
			t.Errorf("message %d ends on an orphaned heading", i)
		}
		if got := runeLen(m); got > maxMessageRunes {
			t.Errorf("message %d is %d runes, over the limit", i, got)
		}
	}
}

// fullSourcesReport has more articles behind one section than the short list
// shows, plus general news no section claimed.
func fullSourcesReport() model.Report {
	rep := testReport()
	rep.Sections[0].Articles = nil
	for i := 0; i < 20; i++ {
		rep.Sections[0].Articles = append(rep.Sections[0].Articles, model.Article{
			Title: fmt.Sprintf("Semis story %d", i), URL: fmt.Sprintf("https://example.com/s/%d", i), SourceName: "Wire"})
	}
	rep.General = []model.Article{
		{Title: "Retail sales tick higher", URL: "https://example.com/g/1", SourceName: "CNBC"},
	}
	return rep
}

// For cross-checking, the short list hides most of the evidence: in full mode
// every article behind a section is listed, and nothing collapses into
// "+N more".
func TestFullSourcesListsEveryArticle(t *testing.T) {
	out := strings.Join(RenderWith(fullSourcesReport(), Options{Sources: SourcesFull}), "\n")
	for i := 0; i < 20; i++ {
		if !strings.Contains(out, fmt.Sprintf("Semis story %d<", i)) {
			t.Errorf("Semis story %d is missing from the full list", i)
		}
	}
	if strings.Contains(out, "more article(s) not listed") {
		t.Errorf("full mode still collapsed articles:\n%s", out)
	}
}

// The overview is written partly from articles no section claimed; without
// them a claim in the overview has nothing to be checked against.
func TestFullSourcesIncludesTheGeneralNews(t *testing.T) {
	out := strings.Join(RenderWith(fullSourcesReport(), Options{Sources: SourcesFull}), "\n")
	if !strings.Contains(out, "General market news") {
		t.Errorf("no general news group:\n%s", out)
	}
	if !strings.Contains(out, "Retail sales tick higher") {
		t.Errorf("the general article is missing:\n%s", out)
	}
}

// The daily brief keeps the short list; full is something you opt into.
func TestShortSourcesAreTheDefault(t *testing.T) {
	out := strings.Join(Render(fullSourcesReport(), time.UTC), "\n")
	if !strings.Contains(out, "+12 more article(s) not listed") {
		t.Errorf("the short list did not cap at %d:\n%s", maxLinksPerSection, out)
	}
	if strings.Contains(out, "General market news") {
		t.Errorf("general news appeared without full mode:\n%s", out)
	}
}

// A full list outgrows a message. It has to break between links, never inside
// one, and say where a category carries on.
func TestFullSourcesSplitLongListsBetweenLinks(t *testing.T) {
	rep := testReport()
	rep.Sections[0].Articles = nil
	for i := 0; i < 120; i++ {
		rep.Sections[0].Articles = append(rep.Sections[0].Articles, model.Article{
			Title:      fmt.Sprintf("A fairly long headline about market developments number %d", i),
			URL:        fmt.Sprintf("https://example.com/markets/2026/09/14/story-%d", i),
			SourceName: "Wire",
		})
	}

	msgs := RenderWith(rep, Options{Sources: SourcesFull})
	for i, m := range msgs {
		if got := runeLen(m); got > maxMessageRunes {
			t.Errorf("message %d is %d runes, over the limit", i, got)
		}
		if strings.Count(m, "<a ") != strings.Count(m, "</a>") {
			t.Errorf("message %d cuts through a link", i)
		}
	}

	out := strings.Join(msgs, "\n")
	if !strings.Contains(out, "(continued)") {
		t.Error("a list split across blocks does not say it continues")
	}
	for i := 0; i < 120; i++ {
		if !strings.Contains(out, fmt.Sprintf("number %d</a>", i)) {
			t.Errorf("link %d was lost in the split", i)
		}
	}
}

// The layout guarantee holds in full mode too: no message carries both the
// analysis and links.
func TestFullSourcesNeverShareAMessageWithTheAnalysis(t *testing.T) {
	for i, m := range RenderWith(fullSourcesReport(), Options{Sources: SourcesFull}) {
		if strings.Contains(m, "NVDA carried the group.") && strings.Contains(m, "<a href=") {
			t.Errorf("message %d holds both analysis and links:\n%s", i, m)
		}
	}
}

// SOURCE_LINKS=off is for a reader who wants the analysis alone: the links go,
// and nothing else does.
func TestSourcesOffOmitsTheSourceList(t *testing.T) {
	msgs := RenderWith(fullSourcesReport(), Options{Sources: SourcesOff})
	out := strings.Join(msgs, "\n")

	if strings.Contains(out, "<b>Sources</b>") {
		t.Errorf("the source list survived with sources off:\n%s", out)
	}
	if strings.Contains(out, "<a href=") {
		t.Errorf("article links survived with sources off:\n%s", out)
	}
	if strings.Contains(out, generalCategory) {
		t.Errorf("the general news list survived with sources off:\n%s", out)
	}
	// The brief itself has to be untouched.
	for _, want := range []string{"Market Watch", "<b>OVERVIEW</b>", "articles from"} {
		if !strings.Contains(out, want) {
			t.Errorf("sources off also removed %q:\n%s", want, out)
		}
	}
}

// Short remains the default, so a caller that sets only Display is unaffected.
func TestZeroValueOptionsStillListSources(t *testing.T) {
	out := strings.Join(RenderWith(testReport(), Options{}), "\n")
	if !strings.Contains(out, "<b>Sources</b>") {
		t.Errorf("the zero value dropped the source list:\n%s", out)
	}
}

func TestRenderPlainSplitsLongProseAndEscapesIt(t *testing.T) {
	body := strings.TrimSpace(strings.Repeat("Gross margin fell to 71.1% from 75.0% & that matters.\n\n", 120))

	msgs := RenderPlain("NVDA — what the filings say", body)
	if len(msgs) < 2 {
		t.Fatalf("got %d message(s), want the prose split across several", len(msgs))
	}
	for i, m := range msgs {
		if runeLen(m) > maxMessageRunes {
			t.Errorf("message %d is %d runes, over the limit", i, runeLen(m))
		}
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "&amp;") {
		t.Errorf("ampersands were not escaped:\n%s", msgs[0])
	}
	if !strings.Contains(msgs[0], "<b>NVDA — what the filings say</b>") {
		t.Errorf("heading missing from the first message:\n%s", msgs[0])
	}
}

// A citation is only useful if it reaches the article. The number stays visible
// so the reader can see which claim rests on which source.
func TestRenderLinksCitationsToTheirArticles(t *testing.T) {
	rep := testReport()
	rep.Overview = "Oracle said cloud revenue doubled [1]. Two outlets carried the chip story [1][2]."
	rep.Cited = []model.Article{
		{Title: "Oracle results", URL: "https://example.com/oracle"},
		{Title: "Chip story", URL: "https://example.com/chips?a=1&b=2"},
	}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, `<a href="https://example.com/oracle">[1]</a>`) {
		t.Errorf("citation 1 was not linked:\n%s", out)
	}
	if !strings.Contains(out, `<a href="https://example.com/chips?a=1&amp;b=2">[2]</a>`) {
		t.Errorf("citation 2 was not linked or not escaped:\n%s", out)
	}
}

// A number with no article behind it must not be attached to some other
// outlet's story, so it is left exactly as the model wrote it.
func TestRenderLeavesUnbackedCitationsAlone(t *testing.T) {
	rep := testReport()
	rep.Overview = "A claim citing nothing that exists [99]. A bracketed aside [not a citation]."
	rep.Cited = []model.Article{{Title: "One", URL: "https://example.com/one"}}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "[99]") || strings.Contains(out, ">[99]</a>") {
		t.Errorf("an unbacked citation was linked:\n%s", out)
	}
	if !strings.Contains(out, "[not a citation]") {
		t.Errorf("a bracketed aside was mangled:\n%s", out)
	}
}

func TestRenderNewNamesSection(t *testing.T) {
	rep := testReport()
	rep.Cited = []model.Article{{Title: "Grab deal", URL: "https://example.com/grab"}}
	rep.Candidates = []model.Candidate{
		{
			Name: "Grab", Ticker: "GRAB", Exchange: "US", Listed: "GRAB HOLDINGS LTD",
			Why: "Agreed to buy Atome Financial for $1.49bn", Sources: 2, Days: 3,
			Articles: rep.Cited,
		},
		{Name: "Atome Financial", Private: true, Why: "Being acquired by Grab", Sources: 2},
		{Name: "Nonesuch", Why: "Announced a takeover", Sources: 2},
	}

	out := strings.Join(Render(rep, time.UTC), "\n")

	if !strings.Contains(out, "<b>NEW NAMES IN THE NEWS</b>") {
		t.Errorf("no new-names section:\n%s", out)
	}
	if !strings.Contains(out, "Not recommendations") {
		t.Errorf("the section does not say what it is not:\n%s", out)
	}
	if !strings.Contains(out, "<code>GRAB</code>") {
		t.Errorf("a verified US ticker is not shown plainly:\n%s", out)
	}
	if !strings.Contains(out, "3 days running") || !strings.Contains(out, "2 outlets") {
		t.Errorf("the evidence is missing:\n%s", out)
	}
	if !strings.Contains(out, "<i>private</i>") {
		t.Errorf("a private company is not flagged:\n%s", out)
	}
	if !strings.Contains(out, "<i>ticker unverified</i>") {
		t.Errorf("an unverified name is not flagged:\n%s", out)
	}
	if !strings.Contains(out, `<a href="https://example.com/grab">[1]</a>`) {
		t.Errorf("the candidate does not cite its article:\n%s", out)
	}
}

// The move is the point of the price: a name in the news that did nothing is a
// different story from one that moved 9%. The level is left out on purpose --
// it would need a currency beside it, and the percentage does not.
func TestANewNameCarriesItsMoveOnTheDay(t *testing.T) {
	rep := testReport()
	rep.Candidates = []model.Candidate{
		{Name: "Grab", Ticker: "GRAB", Exchange: "US", Why: "Bought Atome",
			Quote: &model.Quote{Symbol: "GRAB", Price: 5.42, Percent: 9.1}},
		{Name: "Tencent", Ticker: "700", Exchange: "HK", Why: "Raised its buyback",
			Quote: &model.Quote{Symbol: "0700.HK", Price: 512.40, Percent: -0.8, Currency: "HKD"}},
		{Name: "Nonesuch", Why: "Announced a takeover"},
	}

	out := strings.Join(Render(rep, time.UTC), "\n")

	if !strings.Contains(out, "<code>GRAB</code> · +9.1% last session") {
		t.Errorf("a US name does not carry its move:\n%s", out)
	}
	if !strings.Contains(out, "<code>700.HK</code> · -0.8% last session") {
		t.Errorf("a Hong Kong name does not carry its move:\n%s", out)
	}
	if strings.Contains(out, "512.40") || strings.Contains(out, "5.42") {
		t.Errorf("the block prints a price level, which has no currency beside it:\n%s", out)
	}
	if !strings.Contains(out, "Nonesuch") {
		t.Errorf("a name with no price was dropped:\n%s", out)
	}
}

// A foreign listing has to say which market, or the symbol is ambiguous.
func TestRenderShowsTheExchangeForForeignListings(t *testing.T) {
	rep := testReport()
	rep.Candidates = []model.Candidate{
		{Name: "GSK", Ticker: "GSK", Exchange: "LN", Why: "Licensed a cancer drug", Sources: 2},
	}

	if out := strings.Join(Render(rep, time.UTC), "\n"); !strings.Contains(out, "<code>GSK.LN</code>") {
		t.Errorf("the exchange is missing from a foreign listing:\n%s", out)
	}
}

// A screen of bullets that all open with prose reads as one block. The label
// makes it skimmable -- but only where there is really a label.
func TestBulletLabelsAreEmphasized(t *testing.T) {
	rep := testReport()
	rep.Sections[0].Body = strings.Join([]string{
		"- Gross margin - fell to 71.1% from 75.0% as direct costs grew faster than sales.",
		"- Inventory — US$31.6bn, about 184 days of the latest year's cost of sales.",
		"- Revenue grew 65.5% in the year, which is a sentence rather than a label.",
	}, "\n")

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "• <b>Gross margin</b> - fell to 71.1%") {
		t.Errorf("a dash-separated label was not bolded:\n%s", out)
	}
	if !strings.Contains(out, "• <b>Inventory</b> — US$31.6bn") {
		t.Errorf("an em-dash label was not bolded:\n%s", out)
	}
	if strings.Contains(out, "<b>Revenue grew 65.5% in the year</b>") {
		t.Errorf("a sentence was bolded as a label:\n%s", out)
	}
}

// Bullets stacked flush read as a paragraph with odd punctuation. The gap is
// what makes a list scannable on a phone.
func TestBulletsAreSpacedApart(t *testing.T) {
	rep := testReport()
	rep.Sections[0].Body = strings.Join([]string{
		"- Gross margin - 37.7% → 76.6% over the nine months to 28 May 2026.",
		"- Receivables - US$5.5bn → US$26.9bn, which is most of the profit uncollected.",
		"- Debt - US$14.0bn → US$5.1bn.",
	}, "\n")

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "28 May 2026.\n\n• <b>Receivables</b>") {
		t.Errorf("no blank line between bullets:\n%s", out)
	}
	if !strings.Contains(out, "→") {
		t.Errorf("the arrow did not survive rendering:\n%s", out)
	}
}

// A lead line followed by bullets keeps its own spacing: the gap belongs
// between bullets, not everywhere.
func TestALeadLineIsNotSpacedFromItsFirstBullet(t *testing.T) {
	rep := testReport()
	rep.Sections[0].Body = "Three moves stood out:\n- Meta - rose on Muse\n- Apple - slipped"

	out := strings.Join(Render(rep, time.UTC), "\n")
	if !strings.Contains(out, "Three moves stood out:\n• <b>Meta</b>") {
		t.Errorf("a gap was inserted after the lead line:\n%s", out)
	}
	if !strings.Contains(out, "rose on Muse\n\n• <b>Apple</b>") {
		t.Errorf("the bullets were not spaced:\n%s", out)
	}
}

// The case for and against split into the business and the figures. The two
// labels are what a reader scans for on a phone, so they stand out and keep a
// gap from the bullets above them -- but a brief's lead line does not.
func TestGroupLabelsAreEmphasized(t *testing.T) {
	body := "THE CASE FOR IT\n\n" +
		"In the business:\n- Demand - AI memory is scarce.\n- Technology - first 1γ node.\n" +
		"In the numbers:\n- Cash - US$26.17bn of free cash flow."

	out := strings.Join(RenderPlain("MU", body), "\n")
	if !strings.Contains(out, "<b>In the business:</b>\n• <b>Demand</b>") {
		t.Errorf("the business label was not bolded or was spaced from its bullets:\n%s", out)
	}
	if !strings.Contains(out, "first 1γ node.\n\n<b>In the numbers:</b>\n• <b>Cash</b>") {
		t.Errorf("the numbers label was not bolded and set apart:\n%s", out)
	}

	rep := testReport()
	rep.Sections[0].Body = "Three moves stood out:\n- Meta - rose on Muse"
	if lead := strings.Join(Render(rep, time.UTC), "\n"); strings.Contains(lead, "<b>Three moves stood out:</b>") {
		t.Errorf("a brief's lead line was bolded as a group label:\n%s", lead)
	}
}

// A message that ends on "THE CASE AGAINST IT" with the case itself opening the
// next one reads as though the analysis had been cut off.
func TestPlainKeepsHeadingsWithTheirSections(t *testing.T) {
	var body strings.Builder
	for _, section := range []string{"THE BUSINESS", "WHAT IT OWNS AND OWES", "THE CASE AGAINST IT"} {
		body.WriteString(section + "\n\n")
		for i := 0; i < 9; i++ {
			body.WriteString("- Point " + section + " - " +
				strings.Repeat("a figure and the sentence that carries it. ", 6) + "\n")
		}
		body.WriteString("\n")
	}

	msgs := RenderPlain("MU — what the filings say", body.String())
	if len(msgs) < 2 {
		t.Fatalf("got %d message(s), want the analysis split", len(msgs))
	}
	for i, m := range msgs {
		trimmed := strings.TrimSpace(m)
		for _, heading := range []string{"THE BUSINESS", "WHAT IT OWNS AND OWES", "THE CASE AGAINST IT"} {
			if strings.HasSuffix(trimmed, "<b>"+heading+"</b>") {
				t.Errorf("message %d ends on the heading %q with nothing under it:\n%s", i, heading, m)
			}
		}
		if runeLen(m) > maxMessageRunes {
			t.Errorf("message %d is %d runes, over the limit", i, runeLen(m))
		}
	}
}

func TestSectionHeadingsAreRecognisedNotGuessed(t *testing.T) {
	headings := []string{"THE BUSINESS", "WHAT IT HAS ANNOUNCED", "CASH"}
	for _, h := range headings {
		if !isSectionHeading(h) {
			t.Errorf("isSectionHeading(%q) = false, want true", h)
		}
	}

	prose := []string{
		"• <b>Gross margin</b> - 37.7% → 76.6% over the period.",
		"The company earns most of its money from memory.",
		"US$215.9bn",             // no letters to speak of
		strings.Repeat("A", 200), // too long to be a heading
	}
	for _, p := range prose {
		if isSectionHeading(p) {
			t.Errorf("isSectionHeading(%q) = true, want false", p)
		}
	}
}

// The biggest moves sit directly under the section's heading, where a break
// between messages cannot separate them, and a quiet section has no line.
func TestRenderPutsTheBiggestMovesUnderTheHeading(t *testing.T) {
	rep := testReport()
	rep.Sections[0].Movers = []model.Quote{{Symbol: "NVDA", Percent: -6.2}, {Symbol: "AMD", Percent: 3.1}}
	rep.Sections = append(rep.Sections, model.Section{GroupID: "macro-rates", GroupName: "Macro & Rates", Body: "Rates did the work."})

	out := strings.Join(Render(rep, time.UTC), "\n")

	want := "<b>SEMICONDUCTORS &amp; AI</b>\n<i>Biggest moves: NVDA -6.2% · AMD +3.1%</i>"
	if !strings.Contains(out, want) {
		t.Errorf("rendered brief lacks %q under the heading:\n%s", want, out)
	}
	if strings.Count(out, "Biggest moves") != 1 {
		t.Errorf("a section with no movers got a line:\n%s", out)
	}
}

// The market's outsized moves sit under the overview's heading, in the
// message and on the page.
func TestTheMovesAcrossTheMarketSitUnderTheOverview(t *testing.T) {
	rep := testReport()
	rep.MarketMoves = []model.MarketMove{
		{Quote: model.Quote{Symbol: "FICO", Percent: -26.5}, Name: "Fair Isaac"},
		{Quote: model.Quote{Symbol: "IOVA", Percent: 31.5}, Name: "Iovance"},
	}

	out := strings.Join(Render(rep, time.UTC), "\n")
	if want := "<b>OVERVIEW</b>\n<i>Across the market: FICO -26.5% · IOVA +31.5%</i>"; !strings.Contains(out, want) {
		t.Errorf("rendered brief lacks %q:\n%s", want, out)
	}
	page := render(BriefDoc(rep, Market{}, Options{Display: time.UTC}))
	if !strings.Contains(page, "Across the market") || !strings.Contains(page, "FICO -26.5%") {
		t.Errorf("page lacks the line:\n%s", page)
	}
}

// The week's picks come under their themes, each theme with its numbers and
// what the research found, labelled popular or early; the reactions follow,
// then the earlier picks. A pick says where it fits and what its price is
// against its theme, with its warning signs.
func TestThePicksComeUnderTheirThemes(t *testing.T) {
	cited := []model.Article{{ID: "a1", Title: "Micron raises HBM outlook", URL: "https://example.com/1"}}
	picks := Picks{
		Themes: []ThemeView{
			{Kind: "popular", Name: "AI data centres", Figures: "median member +40 pts over 12 months",
				Driving: "Hyperscalers spend $400bn a year.", PricedIn: "Chips at 40x earnings.", Value: "Power equipment & cooling.",
				Ideas: []model.Idea{{Name: "Vertiv", Ticker: "VRT", Exchange: "US", Kind: model.IdeaTheme, Theme: "AI data centres",
					Link: "Cools the racks; backlog up 30%.", Accounts: true, Verdict: model.Buy, Confidence: "medium",
					Value: "22x earnings against the theme's 35x.", Flags: []string{"above the average analyst target"},
					Case: "Orders outrun the price."}}},
			{Kind: "early", Name: "Grid batteries", Ideas: []model.Idea{{Name: "Seatrium", Ticker: "5E2", Exchange: "SP", Kind: model.IdeaTheme,
				Link: "Builds platforms.", Verdict: model.Sell, Confidence: "low", Before: "BUY on 6 Oct"}}},
			{Kind: "popular", Name: "Nothing held up"},
		},
		Reactions: []model.Idea{{Name: "Micron", Ticker: "MU", Exchange: "US", Kind: model.IdeaReaction, Accounts: true,
			Verdict: model.Buy, Confidence: "high", Articles: cited,
			Changed: "Next year's expected earnings rose 4% [1].", Moved: "-9% in a week",
			Reaction: "Underreacted: the outlook rose and the price fell [1]."}},
		Earlier: []EarlierPick{
			{Name: "Rambus", Symbol: "RMBS", Verdict: model.Buy, At: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), Ahead: 4.2, Priced: true},
			{Name: "DBS", Symbol: "D05.SP", Verdict: model.Hold, At: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)},
		},
	}
	out := strings.Join(RenderPicks(picks, cited, IdeasOptions{}, time.UTC), "\n")
	for _, want := range []string{
		"<b>🔎 This week's picks</b>",
		"at least 5 percentage points over 12 months",
		"<b>AI data centres</b> · <i>Popular</i>",
		"<i>The numbers:</i> median member +40 pts over 12 months",
		"<i>Where the value is:</i> Power equipment &amp; cooling.",
		"🟢 <b>Vertiv</b> 🇺🇸 <code>VRT</code>",
		"<i>Where it fits:</i> Cools the racks; backlog up 30%.",
		"<i>The price:</i> 22x earnings against the theme's 35x.",
		"<i>Warning signs:</i> above the average analyst target",
		"<b>Grid batteries</b> · <i>Early</i>",
		"🇸🇬 <code>5E2.SP</code>",
		"<b>SELL</b> · low confidence · <i>no SEC accounts behind it</i> · <i>was BUY on 6 Oct</i>",
		"<b>Reacting to the news</b>",
		"<b>BUY</b> · high confidence · underreacted",
		`<i>What changed:</i> Next year's expected earnings rose 4% <a href="https://example.com/1">[1]</a>.`,
		"<b>Earlier picks</b>",
		"• Rambus 🇺🇸 <code>RMBS</code> · BUY on 28 Sep · +4.2 points the way called",
		"• DBS 🇸🇬 <code>D05.SP</code> · HOLD on 5 Oct · not yet traded since",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("picks are missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Nothing held up") {
		t.Error("a theme with no picks was shown")
	}
	if i, j := strings.Index(out, "AI data centres"), strings.Index(out, "Reacting to the news</b>"); i > j {
		t.Error("the reactions came before the themes")
	}

	// A day with no themes is headed for what it is.
	day := strings.Join(RenderPicks(Picks{Reactions: picks.Reactions}, cited, IdeasOptions{}, time.UTC), "\n")
	if !strings.Contains(day, "<b>🔎 Reacting to the news</b>") || strings.Contains(day, "This week's picks") || strings.Count(day, "Reacting to the news") != 1 {
		t.Errorf("a day's reactions:\n%s", day)
	}
	if RenderPicks(Picks{Themes: []ThemeView{{Name: "Empty"}}}, nil, IdeasOptions{}, time.UTC) != nil {
		t.Error("nothing to show rendered a heading over nothing")
	}

	// The channel's copy is the same under a different note: a reader who
	// has never seen this before is told what wrote it and that it is not
	// advice.
	forChannel := strings.Join(RenderPicks(picks, cited, IdeasOptions{ForChannel: true}, time.UTC), "\n")
	for _, want := range []string{"<b>Vertiv</b>", "AI-written, unchecked, not advice."} {
		if !strings.Contains(forChannel, want) {
			t.Errorf("the channel's picks are missing %q:\n%s", want, forChannel)
		}
	}
	if strings.Contains(forChannel, "/scorecard") {
		t.Error("the channel was told to use /scorecard, a command only the owner can send")
	}
}

// Each part of a verdict is a paragraph of its own, the figures are listed one
// to a line, and a short rule separates one company from the next -- but
// never opens a message, where it would be drawn under nothing.
func TestTheCloserLookIsSpacedOut(t *testing.T) {
	idea := func(name string) model.Idea {
		return model.Idea{Name: name, Ticker: name, Exchange: "US", Accounts: true, Kind: model.IdeaReaction,
			Verdict: model.Sell, Confidence: "medium",
			Changed:  "An order with no value attached.",
			Moved:    "+4.4% today, +15.5% in a week",
			Reaction: "Overreacted: a headline, no revenue.",
			Case:     "Priced for a contract it has not won.",
			Numbers:  "56 times revenue; free cash flow -$273m;  ; cash $2.1bn",
			Risk:     "A real contract."}
	}
	withAhead := func(name string) model.Idea {
		i := idea(name)
		i.Catalyst = "Results on 5 Nov, the first with the order in them."
		i.Sensitivity = "1% of revenue is 2% of expected earnings."
		return i
	}
	// Enough of them to run over one message.
	ideas := []model.Idea{withAhead("IONQ"), idea("QBTS")}
	for _, name := range []string{"RGTI", "QUBT", "ARQQ", "LAES", "QMCO", "HON", "IBM", "GOOGL", "MSFT", "NVDA"} {
		ideas = append(ideas, idea(name))
	}
	out := RenderPicks(Picks{Reactions: ideas}, nil, IdeasOptions{}, time.UTC)
	if len(out) < 2 {
		t.Fatalf("twelve companies fit one message; the test needs a break between messages")
	}
	all := strings.Join(out, "\n")

	for _, want := range []string{
		"<b>SELL</b> · medium confidence · overreacted\n\n<i>What changed:</i> An order",
		"value attached.\n\n<i>The move:</i> +4.4% today, +15.5% in a week\n<i>Justified?</i> Overreacted",
		"no revenue.\n\n<i>The case:</i> Priced for",
		"has not won.\n\n<i>Catalyst:</i> Results on 5 Nov, the first with the order in them.\n<i>Sensitivity:</i> 1% of revenue is 2% of expected earnings.\n\n<i>Numbers</i>",
		"<i>Numbers</i>\n• 56 times revenue\n• free cash flow -$273m\n• cash $2.1bn\n\n<i>Risk:</i> A real contract.",
		"A real contract.\n\n" + companyRule + "\n🔴 <b>QBTS</b>",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("closer look is missing %q:\n%s", want, all)
		}
	}
	for i, msg := range out {
		if strings.HasPrefix(msg, companyRule) || strings.HasPrefix(msg, divider) {
			t.Errorf("message %d opens with a rule:\n%s", i, msg)
		}
		// Within a message, a rule before every company but the first.
		companies, rules := strings.Count(msg, "🔴 <b>"), strings.Count(msg, "\n"+companyRule+"\n")
		if companies > 0 && rules != companies-1 {
			t.Errorf("message %d has %d rules between %d companies:\n%s", i, rules, companies, msg)
		}
	}
}

// The brief's points sit under bold sub-headings, below a section heading in
// capitals, with a blank line between bullets. A sub-heading written with a
// blank line under it still heads its bullets, and one with a dash in it is
// not bolded twice.
func TestTheBriefIsSubheadingsAndBullets(t *testing.T) {
	rep := testReport()
	rep.Sections[0].Body = "### Oil & gas\n- Brent topped $100 as talks stalled.\n- Refiners fell.\n\n" +
		"### Fed\n\n- Williams said another rise is reasonable.\n\n### Trump - Xi\n- The truce runs to 10 January."

	out := strings.Join(Render(rep, time.UTC), "\n")

	for _, want := range []string{
		"<b>SEMICONDUCTORS &amp; AI</b>",
		"<b>Oil &amp; gas</b>\n• Brent topped $100 as talks stalled.\n\n• Refiners fell.\n\n<b>Fed</b>\n• Williams said",
		"<b>Trump - Xi</b>\n• The truce",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "###") {
		t.Errorf("a sub-heading marker reached the reader:\n%s", out)
	}
}

// The analysis's sections are ruled off in capitals, with bold sub-headings
// under them -- the case for and against among them -- even where a heading
// was written straight onto its first sub-heading.
func TestTheAnalysisIsSubheadingsAndBullets(t *testing.T) {
	body := "WHAT THE COMPANY EARNS\n\n### Revenue\n- US$26.1bn → US$79.0bn, +203%.\n\n" +
		"THE CASE FOR IT\n### In the business\n- Demand for HBM.\n\n### In the numbers\n- Margin 76.6%."

	out := strings.Join(RenderPlain("MU", body), "\n")

	for _, want := range []string{
		divider + "\n<b>WHAT THE COMPANY EARNS</b>\n\n<b>Revenue</b>\n• US$26.1bn",
		divider + "\n<b>THE CASE FOR IT</b>\n\n<b>In the business</b>\n• Demand for HBM.\n\n<b>In the numbers</b>\n• Margin 76.6%.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("analysis is missing %q:\n%s", want, out)
		}
	}
}

// News that came after the last price has not been traded on, and the verdict
// line says so rather than calling the share's stillness a judgment.
func TestTheReactionWordReadsNotYetTraded(t *testing.T) {
	for reaction, want := range map[string]string{
		"Overreacted: a headline, no revenue.":          "overreacted",
		"Not yet traded: results came after the close.": "not yet traded",
		"not yet traded -- out after the close":         "not yet traded",
		"Not enough news to judge.":                     "",
		"":                                              "",
	} {
		if got := reactionWord(reaction); got != want {
			t.Errorf("reactionWord(%q) = %q, want %q", reaction, got, want)
		}
	}
}

// A source a model names as a markdown link, as it does for what it found
// on the web, arrives as a link rather than as brackets.
func TestMarkdownLinksBecomeLinks(t *testing.T) {
	got := linkCitations("Checked: [MarketBeat](https://www.marketbeat.com/a?b=1&amp;c=2) and [3].", nil)
	want := `Checked: <a href="https://www.marketbeat.com/a?b=1&amp;c=2">MarketBeat</a> and [3].`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
