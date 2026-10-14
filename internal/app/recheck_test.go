package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
)

// recheckModel answers every re-check request with the same reply, after
// failing the first fail of them, and counts the requests.
type recheckModel struct {
	reply string
	fail  int
	asked *int
}

func (m recheckModel) Complete(_ context.Context, _, _ string) (string, model.Usage, error) {
	*m.asked++
	if *m.asked <= m.fail {
		return "", model.Usage{}, errors.New("plan limit reached")
	}
	return m.reply, model.Usage{}, nil
}

// quickRetries makes the same-day retries come at once, for the test.
func quickRetries(t *testing.T) {
	saved := recheckRetries
	recheckRetries = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { recheckRetries = saved })
}

// The re-check runs with the first Friday's scheduled brief, once a month:
// not on another day, and not again the next Friday. It tells the owner
// what it did, and the closed call ends on the scorecard.
func TestTheRecheckRunsOnTheFirstFridayOfTheMonth(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	card, err := ideas.LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	a.Scorecard = card
	call := ideas.Record{At: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Name: "Carrier Global", Symbol: "CARR", Chart: "CARR", Verdict: model.Buy}
	if err := card.Add(call); err != nil {
		t.Fatal(err)
	}
	asked := 0
	a.Recheck = &ideas.Recheck{Completer: recheckModel{asked: &asked, reply: "=== " + call.Key() + "\nACTION: close\nWHY: Its data center orders fell on 29 September."}}

	run := func(at time.Time) {
		a.Now = func() time.Time { return at }
		a.monthlyRecheck(context.Background())
	}
	run(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)) // a Thursday
	if asked != 0 {
		t.Fatalf("the re-check ran on a Thursday")
	}
	run(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)) // the first Friday
	if asked != 1 {
		t.Fatalf("the re-check asked %d times on the first Friday, want 1", asked)
	}
	reply := lastReply(t, *sent)
	if !strings.Contains(reply, "Looked again at 1 call: 0 kept, 0 changed, 1 closed.") ||
		!strings.Contains(reply, "✋ <b>CARR</b> Carrier Global") {
		t.Errorf("reply = %q", reply)
	}
	if c := card.All()[0].Closed; c == nil || c.Reason != ideas.ClosedRechecked {
		t.Errorf("the call was not closed: %+v", c)
	}
	if a.Prefs().LastRecheck.IsZero() {
		t.Errorf("the month's re-check was not written down")
	}

	run(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)) // the second Friday
	if asked != 1 {
		t.Errorf("the re-check ran twice in October")
	}
}

// A try that gets no answer is tried again later the same Friday, and the
// owner hears once, with every call answered.
func TestAFailedRecheckIsTriedAgainTheSameFriday(t *testing.T) {
	quickRetries(t)
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	card, err := ideas.LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	a.Scorecard = card
	call := ideas.Record{At: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Symbol: "CARR", Chart: "CARR", Verdict: model.Buy}
	if err := card.Add(call); err != nil {
		t.Fatal(err)
	}
	asked := 0
	a.Recheck = &ideas.Recheck{Completer: recheckModel{asked: &asked, fail: 2,
		reply: "=== " + call.Key() + "\nACTION: keep\nWHY: Orders rose again."}}
	a.Now = func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

	a.monthlyRecheck(context.Background())
	a.work.wait()

	if asked != 3 {
		t.Errorf("asked %d times, want 3: two failures, then the answer", asked)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Text, "Looked again at 1 call: 1 kept") {
		t.Errorf("sent %+v, want one message with the kept call", *sent)
	}
	if a.Prefs().LastRecheck.IsZero() {
		t.Errorf("the month's re-check was not written down")
	}
}

// A Friday on which every try failed sends nothing, is not written down, and
// is caught up the next Friday.
func TestAFridayWithNoAnswersIsCaughtUpTheNextFriday(t *testing.T) {
	quickRetries(t)
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	card, err := ideas.LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	a.Scorecard = card
	if err := card.Add(ideas.Record{At: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Symbol: "CARR", Chart: "CARR", Verdict: model.Buy}); err != nil {
		t.Fatal(err)
	}
	asked := 0
	a.Recheck = &ideas.Recheck{Completer: recheckModel{asked: &asked, fail: 100}}
	a.Now = func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }
	a.monthlyRecheck(context.Background())
	a.work.wait()

	if want := 1 + len(recheckRetries); asked != want {
		t.Errorf("asked %d times, want %d", asked, want)
	}
	if len(*sent) != 0 {
		t.Errorf("sent %+v on a Friday with no answers", *sent)
	}
	if !a.Prefs().LastRecheck.IsZero() {
		t.Errorf("a re-check with no answers was written down as done")
	}
	if !a.recheckDue(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("the next Friday does not catch up")
	}
}

// The first re-check ever waits for a first Friday: deployed late in
// October, it runs on 6 November, not on 30 October and again a week on.
func TestTheFirstRecheckWaitsForAFirstFriday(t *testing.T) {
	a, _ := newTestApp(t)
	if a.recheckDue(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("the first re-check would run on the last Friday of October")
	}
	if !a.recheckDue(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("the first re-check would not run on 6 November")
	}
}
