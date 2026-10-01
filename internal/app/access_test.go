package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/telegram"
)

const (
	commandChat = 111
	controlChat = 333
)

func from(chat int64, text string) telegram.Message {
	return telegram.Message{Text: text, Chat: telegram.Chat{ID: chat, Type: "private"}}
}

// repliesTo is the text of every message sent to one chat.
func repliesTo(sent []sentMessage, chat int64) []string {
	var out []string
	for _, m := range sent {
		if m.ChatID == chat {
			out = append(out, m.Text)
		}
	}
	return out
}

// A command chat gets the commands that only answer. The ones that spend the
// most, change what everyone reads, or act on the owner's deliveries are
// refused with a word saying where they work, rather than silence, since the
// chat is one the owner let in.
func TestACommandChatGetsTheCommandsThatOnlyAnswer(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramCommandChats = []int64{commandChat}

	a.HandleMessage(context.Background(), from(commandChat, "/help"))
	if got := repliesTo(*sent, commandChat); len(got) != 1 || !strings.Contains(got[0], "/analyse") {
		t.Fatalf("/help from a command chat = %q, want the help", got)
	}

	for _, text := range []string{
		"/now",
		"/watchlist add semis-ai ZZZZ",
		"/sources off cnbc-top",
		"/start",
		"/share",
		"/clear",
	} {
		*sent = nil
		a.HandleMessage(context.Background(), from(commandChat, text))
		got := repliesTo(*sent, commandChat)
		if len(got) != 1 || !strings.Contains(got[0], "only") {
			t.Errorf("%s from a command chat: replies %q, want one refusal", text, got)
		}
	}

	if a.Prefs().ChatID != 4242 {
		t.Error("a command chat took the brief over")
	}
	for _, g := range a.Prefs().Groups {
		if g.Has("ZZZZ") {
			t.Error("a command chat edited the watchlists")
		}
	}
}

// A control chat may also change the watchlist and the feeds, but still not
// move the brief, clear the owner's chat or post to the channel.
func TestAControlChatMayChangeTheWatchlistAndTheFeeds(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramControlChats = []int64{controlChat}

	a.HandleMessage(context.Background(), from(controlChat, "/watchlist add semis-ai ZZZZ"))
	a.HandleMessage(context.Background(), from(controlChat, "/sources off cnbc-top"))
	a.HandleMessage(context.Background(), from(controlChat, "/analyse NVDA"))

	added := false
	for _, g := range a.Prefs().Groups {
		added = added || g.Has("ZZZZ")
	}
	if !added {
		t.Error("a control chat could not add to the watchlist")
	}
	for _, s := range a.Prefs().Sources {
		if s.ID == "cnbc-top" && s.Enabled {
			t.Error("a control chat could not turn a feed off")
		}
	}
	if got := repliesTo(*sent, controlChat); len(got) != 3 {
		t.Errorf("replies to the control chat = %q, want one a command", got)
	}

	for _, text := range []string{"/start", "/share", "/clear"} {
		*sent = nil
		a.HandleMessage(context.Background(), from(controlChat, text))
		if got := repliesTo(*sent, controlChat); len(got) != 1 || !strings.Contains(got[0], "owner's chat only") {
			t.Errorf("%s from a control chat: replies %q, want one refusal", text, got)
		}
	}
	if a.Prefs().ChatID != 4242 {
		t.Error("a control chat took the brief over")
	}
}

// A brief a control chat asks for goes there alone. The owner's copy of the
// day, the one /share would post and the next run's clearing are all left as
// they were.
func TestABriefAControlChatAsksForGoesThereAlone(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.prefs.LastBrief = []int64{7, 8}
	a.Cfg.TelegramControlChats = []int64{controlChat}

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Fed holds rates steady</title><link>https://feed.example/fed</link>
<description>The Fed held.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()
	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nThe Fed held [1].\n"}}

	a.HandleMessage(context.Background(), from(controlChat, "/now"))

	if got := repliesTo(*sent, 4242); len(got) != 0 {
		t.Errorf("the owner was sent %q for another chat's brief", got)
	}
	got := strings.Join(repliesTo(*sent, controlChat), "\n")
	if !strings.Contains(got, "The Fed held") {
		t.Errorf("the control chat did not get its brief: %s", got)
	}
	if lb := a.Prefs().LastBrief; len(lb) != 2 || lb[0] != 7 {
		t.Errorf("LastBrief = %v, want the owner's left as it was", lb)
	}
	if a.latest() != nil {
		t.Error("another chat's brief became the one /share posts")
	}
}
