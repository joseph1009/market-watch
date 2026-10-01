package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/search"
)

// /usage answers any chat that may send commands with what is left of the
// Claude plan, window by window, and of the month's search credits.
func TestUsageSaysWhatIsLeftToACommandChat(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramCommandChats = []int64{commandChat}

	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"account":{"current_plan":"Researcher","plan_usage":44,"plan_limit":1000}}`))
	}))
	defer tavily.Close()
	a.Search = &search.Client{APIKey: "test", HTTP: tavily.Client(), UsageURL: tavily.URL}

	// A reading from a call a minute ago is shown as it is, with no new call.
	resets := a.now().Add(3 * time.Hour)
	a.plan = &planWatch{}
	a.plan.note(relay.Limits{Status: "allowed", Overage: "rejected", Windows: map[string]relay.Window{
		"five_hour": {Utilization: 0.43, ResetsAt: resets.Unix()},
		"seven_day": {Utilization: 0.21, ResetsAt: resets.Add(5 * 24 * time.Hour).Unix()},
	}})

	a.HandleMessage(context.Background(), from(commandChat, "/usage"))

	got := repliesTo(*sent, commandChat)
	if len(got) != 1 {
		t.Fatalf("replies = %q, want one", got)
	}
	for _, want := range []string{
		"CLAUDE PLAN", "Next five hours: <b>57% left</b> (43% used) · resets @ " + resets.In(a.Cfg.DisplayLocation).Format("15:04"),
		"This week: <b>79% left</b> (21% used)", "Extra usage past the plan: off",
		"NEWS SEARCH", "<b>956 of 1,000 credits left</b> this month (44 used), Researcher plan",
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("reply is missing %q:\n%s", want, got[0])
		}
	}
}

// With no reading and nothing to take one with, /usage says so rather than
// failing, and still gives the search credits.
func TestUsageWithoutClaudeCodeSaysWhy(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242

	a.HandleMessage(context.Background(), message("/usage"))

	got := messagesTo(*sent, 4242)
	if len(got) != 1 || !strings.Contains(got[0], "Could not be read") || !strings.Contains(got[0], "no TAVILY_API_KEY") {
		t.Errorf("reply = %q", got)
	}
}
