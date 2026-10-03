package app

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/telegram"
)

const writtenAnalysis = `THE BUSINESS
- Makes memory.

THE VERDICT
VERDICT: BUY
CONFIDENCE: medium

### Why
- 8.7 times next year's expected earnings.

### What would change it
- Memory prices falling.`

// The verdict is shown last, after the analysis, and the channel's copy
// carries the warning a reader there needs, which the owner's does not.
func TestAnAnalysisShowsItsVerdictLastAndWarnsTheChannel(t *testing.T) {
	snap := fundamentals.Snapshot{Ticker: "MU", Company: "MICRON TECHNOLOGY INC"}
	w := splitAnalysis(writtenAnalysis, nil)

	owner := strings.Join(analysisMessages(snap, w, telegram.IdeasOptions{}), "\n")
	channel := strings.Join(analysisMessages(snap, w, telegram.IdeasOptions{ForChannel: true}), "\n")

	for name, text := range map[string]string{"owner": owner, "channel": channel} {
		v, b := strings.Index(text, "THE VERDICT"), strings.Index(text, "THE BUSINESS")
		if v < 0 || b < 0 || v < b {
			t.Errorf("%s: the verdict is not shown after the analysis:\n%s", name, text)
		}
		if !strings.Contains(text, "🟢 <b>BUY</b> · medium confidence") || !strings.Contains(text, "8.7 times") {
			t.Errorf("%s: the verdict is missing:\n%s", name, text)
		}
	}
	if !strings.Contains(owner, "at least 5 percentage points over 12 months") || strings.Contains(owner, "not advice") {
		t.Errorf("owner's note:\n%s", owner)
	}
	for _, want := range []string{"AI-written, unchecked, not advice."} {
		if !strings.Contains(channel, want) {
			t.Errorf("the channel's copy is missing %q:\n%s", want, channel)
		}
	}
}

// A name filed in capitals is written as a reader would write it.
func TestTheCompanysNameIsReadable(t *testing.T) {
	for filed, want := range map[string]string{
		"MICRON TECHNOLOGY INC":        "Micron Technology",
		"ADVANCED MICRO DEVICES INC":   "Advanced Micro Devices",
		"IBM CORP":                     "IBM",
		"Rivian Automotive, Inc. / DE": "Rivian Automotive",
	} {
		if got := readableName(filed); got != want {
			t.Errorf("readableName(%q) = %q, want %q", filed, got, want)
		}
	}
}

// /share posts the channel's copy of an analysis, not the owner's.
func TestShareSendsTheChannelsCopy(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	a.rememberFor("the MU analysis", []string{"owner's copy"}, []string{"channel's copy"})

	handle(a, message("/share"))

	if got := messagesTo(*sent, testChannel); strings.Join(got, "|") != "channel's copy" {
		t.Errorf("channel got %q", got)
	}
}

// An analysis's verdict is kept score of like the closer look's, marked as
// an analysis's, and asking again the same day does not count it twice.
func TestAnAnalysisVerdictGoesOnTheScorecard(t *testing.T) {
	a, _ := newTestApp(t)
	card, err := ideas.LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	a.Scorecard = card
	snap := fundamentals.Snapshot{Ticker: "BRK.B", Company: "BERKSHIRE HATHAWAY INC",
		Trading: &model.Trading{Last: 480, Currency: "USD"}}
	_, verdict := fundamentals.SplitVerdict(writtenAnalysis)

	a.recordAnalysis(snap, verdict)
	a.recordAnalysis(snap, verdict)

	all := card.All()
	if len(all) != 1 {
		t.Fatalf("recorded %d, want the one call once", len(all))
	}
	r := all[0]
	if r.Source != ideas.SourceAnalysis || r.Verdict != model.Buy || r.Confidence != "medium" || r.Chart != "BRK-B" || r.Price != 480 {
		t.Errorf("record = %+v", r)
	}

	// A day on, it is a call made again.
	later := a.now().Add(25 * time.Hour)
	a.Now = func() time.Time { return later }
	a.recordAnalysis(snap, verdict)
	if len(card.All()) != 2 {
		t.Errorf("a verdict given again the next day was not recorded")
	}
}
