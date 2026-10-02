package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/industry"
	"github.com/joseph1009/market-watch/internal/pages"
)

// keptPages stands in for the page store, keeping what it is given.
type keptPages struct {
	pages []pages.Page
	err   error
}

func (k *keptPages) Publish(p pages.Page) (string, error) {
	if k.err != nil {
		return "", k.err
	}
	k.pages = append(k.pages, p)
	return "https://pages.example/r/" + string(rune('a'+len(k.pages)-1)), nil
}

func explainingRobotics(a *App) {
	a.Industry = &industry.Explainer{
		Completer: industryModel{reply: "### 🧭 The big picture\n- Robots sense, decide and move.\n\n### ⚙️ Motion parts\n- Gears and motors.\n\n" +
			"COMPANIES BY PART\nRobot makers|Fanuc|6954|JP|the largest maker of factory robots"},
		Verifier: knownTickers{"6954|JP": "FANUC CORP"},
	}
}

// With pages on, the chat gets one message -- the summary, with a button to
// the page -- and the page carries the whole of what was written. /share
// posts the channel's own summary and page, under the channel's note.
func TestLongMessagesGoOutAsASummaryWithAButtonToThePage(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	kept := &keptPages{}
	a.Pages = kept
	explainingRobotics(a)

	handle(a, message("/industry robotics"))

	var summary *sentMessage
	for i := range *sent {
		if m := &(*sent)[i]; m.ChatID == 4242 && len(m.ReplyMarkup.Rows) > 0 {
			summary = m
		}
	}
	if summary == nil {
		t.Fatalf("no message with a button; sent %+v", *sent)
	}
	if b := summary.ReplyMarkup.Rows[0][0]; b.URL != "https://pages.example/r/a" || !strings.Contains(b.Text, "Read") {
		t.Errorf("button = %+v", b)
	}
	for _, want := range []string{"Robotics — how the industry fits together", "Robots sense, decide and move.", "<b>1 COMPANY TO LOOK INTO</b>", "• Robot makers — 1"} {
		if !strings.Contains(summary.Text, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary.Text)
		}
	}
	if strings.Contains(summary.Text, "Gears and motors") {
		t.Errorf("the summary carries more than the first part:\n%s", summary.Text)
	}
	if len(kept.pages) != 1 || kept.pages[0].Doc == nil {
		t.Fatalf("pages = %+v", kept.pages)
	}
	page := pages.Fragment(kept.pages[0])
	for _, want := range []string{"Gears and motors", "Fanuc", "<code>6954.JP</code>", "Robot makers"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q:\n%s", want, page)
		}
	}

	handle(a, message("/share"))
	channel := messagesTo(*sent, testChannel)
	if len(channel) != 1 || !strings.Contains(channel[0], "AI-written, unchecked, not advice.") {
		t.Errorf("channel got %q", channel)
	}
	if len(kept.pages) != 2 || !strings.Contains(pages.Fragment(kept.pages[1]), "AI-written, unchecked, not advice.") {
		t.Errorf("the channel's page does not carry its note: %+v", kept.pages)
	}
}

// A page that cannot be written costs the layout, never the content: the
// messages go out in full, as they did before pages.
func TestAPageThatFailsSendsTheMessagesInFull(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Pages = &keptPages{err: errors.New("disk full")}
	explainingRobotics(a)

	handle(a, message("/industry robotics"))

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	if !strings.Contains(owner, "Gears and motors") || !strings.Contains(owner, "Fanuc 🇯🇵 <code>6954.JP</code>") {
		t.Errorf("the full explanation did not arrive:\n%s", owner)
	}
	for _, m := range *sent {
		if len(m.ReplyMarkup.Rows) > 0 {
			t.Errorf("a button to a page that does not exist: %+v", m)
		}
	}
}
