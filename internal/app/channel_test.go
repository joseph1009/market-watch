package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/joseph1009/market-watch/internal/telegram"
)

const testChannel = -1001234567890

// messagesTo is what one chat received.
func messagesTo(sent []sentMessage, chat int64) []string {
	var got []string
	for _, m := range sent {
		if m.ChatID == chat {
			got = append(got, m.Text)
		}
	}
	return got
}

func lastReply(t *testing.T, sent []sentMessage) string {
	t.Helper()
	got := messagesTo(sent, 4242)
	if len(got) == 0 {
		t.Fatal("the owner got no reply")
	}
	return got[len(got)-1]
}

func TestShareWithoutAChannelSaysHowToSetOneUp(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.remember("the brief of Thu 10 Sep", []string{"brief"})

	handle(a, message("/share"))

	if reply := lastReply(t, *sent); !strings.Contains(reply, "TELEGRAM_CHANNEL_ID") {
		t.Errorf("reply = %q, want it to say how to set a channel up", reply)
	}
}

func TestShareWithNothingSentSaysSo(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel

	handle(a, message("/share"))

	if got := messagesTo(*sent, testChannel); len(got) != 0 {
		t.Errorf("the channel got %q with nothing to share", got)
	}
	if reply := lastReply(t, *sent); !strings.Contains(reply, "Nothing to share") {
		t.Errorf("reply = %q, want it to say there is nothing to share", reply)
	}
}

// /share posts whatever reached the owner last, whole and in order, and only
// once: a second /share must not post the same brief twice.
func TestSharePostsTheLatestOnce(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel

	a.remember("the brief of Thu 10 Sep", []string{"old brief"})
	a.remember("the NVDA analysis", []string{"part one", "part two"})

	handle(a, message("/share"))

	if got := messagesTo(*sent, testChannel); strings.Join(got, "|") != "part one|part two" {
		t.Fatalf("channel got %q, want the analysis in order", got)
	}
	if reply := lastReply(t, *sent); reply != "Posted the NVDA analysis to the channel." {
		t.Errorf("reply = %q", reply)
	}

	handle(a, message("/share"))

	if got := messagesTo(*sent, testChannel); len(got) != 2 {
		t.Errorf("channel got %d messages after a second /share, want still 2", len(got))
	}
	if reply := lastReply(t, *sent); reply != "The NVDA analysis is already in the channel." {
		t.Errorf("reply = %q", reply)
	}
}

// The scheduled brief goes to the channel by itself, and /share afterwards
// knows it is already there.
func TestTheScheduledBriefIsPostedToTheChannel(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel

	a.shareBrief(context.Background(), a.remember("the brief of Thu 10 Sep", []string{"one", "two"}), nil)

	if got := messagesTo(*sent, testChannel); len(got) != 2 {
		t.Fatalf("channel got %d messages, want the brief's 2", len(got))
	}
	if got := messagesTo(*sent, 4242); len(got) != 0 {
		t.Errorf("the owner was told %q about a post that worked", got)
	}

	handle(a, message("/share"))
	if got := messagesTo(*sent, testChannel); len(got) != 2 {
		t.Errorf("channel got %d messages, want no second copy", len(got))
	}
}

// Without a channel the scheduler posts nothing and says nothing: the owner's
// brief is the whole of it, as before there were channels.
func TestNoChannelMeansNoPost(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242

	a.shareBrief(context.Background(), a.remember("the brief of Thu 10 Sep", []string{"one"}), nil)

	if len(*sent) != 0 {
		t.Errorf("sent %+v with no channel set", *sent)
	}
}

// A channel that refuses the post costs the channel one brief. The owner is
// told, with the way to retry, and a retry that works is not refused as a
// repeat of the one that failed.
func TestAFailedChannelPostIsReportedAndCanBeRetried(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel

	var (
		mu     sync.Mutex
		sent   []sentMessage
		refuse = true
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m sentMessage
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		defer mu.Unlock()
		if m.ChatID == testChannel && refuse {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the channel chat"}`))
			return
		}
		sent = append(sent, m)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	a.Bot = &telegram.Client{Token: "test", BaseURL: srv.URL, HTTP: srv.Client()}

	a.shareBrief(context.Background(), a.remember("the brief of Thu 10 Sep", []string{"one", "two"}), nil)

	mu.Lock()
	told := messagesTo(sent, 4242)
	mu.Unlock()
	if len(told) != 1 || !strings.Contains(told[0], "not the channel") || !strings.Contains(told[0], "/share") {
		t.Fatalf("owner was told %q, want the failure and how to retry", told)
	}
	if !strings.Contains(told[0], "not a member") {
		t.Errorf("owner was told %q, want Telegram's reason", told[0])
	}

	mu.Lock()
	refuse = false
	mu.Unlock()
	handle(a, message("/share"))

	mu.Lock()
	defer mu.Unlock()
	if got := messagesTo(sent, testChannel); len(got) != 2 {
		t.Errorf("channel got %d messages on retry, want the brief's 2", len(got))
	}
}
