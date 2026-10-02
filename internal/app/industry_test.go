package app

import (
	"context"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/industry"
	"github.com/joseph1009/market-watch/internal/model"
)

type industryModel struct{ reply string }

func (m industryModel) Complete(context.Context, string, string) (string, model.Usage, error) {
	return m.reply, model.Usage{}, nil
}

type knownTickers map[string]string

func (k knownTickers) Verify(_ context.Context, queries []discover.Query) ([]string, error) {
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = k[q.Ticker+"|"+q.Exchange]
	}
	return out, nil
}

// /industry robotics explains the industry part by part and lists the
// companies to look into under each, with checked tickers; /share then posts
// the channel's copy, which says a model wrote it.
func TestIndustryExplainsAndListsCompaniesByPart(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	a.Industry = &industry.Explainer{
		Completer: industryModel{reply: "### 🧭 The big picture\n- Robots sense, decide and move.\n\n" +
			"COMPANIES BY PART\nRobot makers|Fanuc|6954|JP|the largest maker of factory robots\nRobot makers|ATS Corporation|ATS|CN|builds automated factory lines\nRobot makers|Nobody Inc|NOPE|US|made up"},
		Verifier: knownTickers{"6954|JP": "FANUC CORP", "ATS|CN": "ATS CORP"},
	}

	handle(a, message("/industry robotics"))

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	for _, want := range []string{"🧭 Robotics — how the industry fits together", "<b>🧭 The big picture</b>",
		"🏢 COMPANIES TO LOOK INTO", "<b>Robot makers</b>", "Fanuc 🇯🇵 <code>6954.JP</code> — the largest maker of factory robots", "ATS Corporation 🇨🇦 <code>ATS.CN</code>", "not recommendations"} {
		if !strings.Contains(owner, want) {
			t.Errorf("owner's copy is missing %q:\n%s", want, owner)
		}
	}
	if strings.Contains(owner, "NOPE") || strings.Contains(owner, "AI-written") {
		t.Errorf("owner's copy shows an unchecked ticker or the channel's note:\n%s", owner)
	}

	handle(a, message("/share"))
	if channel := strings.Join(messagesTo(*sent, testChannel), "\n"); !strings.Contains(channel, "AI-written, unchecked, not advice.") || !strings.Contains(channel, "Fanuc") {
		t.Errorf("channel's copy:\n%s", channel)
	}
}

// /industry on its own asks which industry, and the next message, however
// many words, is the topic (2026-10-01).
func TestIndustryAsksWhichThenTakesTheAnswer(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	explainingRobotics(a)

	handle(a, message("/industry"))
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Text, "Which industry?") || !(*sent)[0].ReplyMarkup.ForceReply {
		t.Fatalf("asked %+v", *sent)
	}

	handle(a, message("industrial robots"))
	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	if !strings.Contains(owner, "Mapping industrial robots") || !strings.Contains(owner, "Fanuc") {
		t.Errorf("after the answer:\n%s", owner)
	}
}
