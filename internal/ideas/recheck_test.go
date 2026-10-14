package ideas

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The monthly re-check, end to end: the live calls a month old are looked
// at, the newer one is not; a kept call notes it, a changed one is replaced
// by the new call, and a closed one ends with its result. Each shows in the
// month's message, and the closed one in /scorecard with the replaced calls.
func TestTheMonthlyRecheck(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if err := s.Add(
		Record{At: then, Name: "Keeper", Symbol: "KEEP", Chart: "KEEP", Verdict: model.Buy, Confidence: "medium", Source: SourceTheme, Theme: "Power"},
		Record{At: then, Name: "Changer", Symbol: "CHG", Chart: "CHG", Verdict: model.Buy, Source: SourceTheme,
			Case: "Data center orders keep growing.", Catalyst: "Results on 29 Oct."},
		Record{At: then, Name: "Closer", Symbol: "CLS", Chart: "CLS", Verdict: model.Sell, Source: SourceReaction},
		Record{At: then, Name: "Waiter", Symbol: "BB", Chart: "BB", Verdict: model.Hold, Source: SourceAnalysis},
		Record{At: now.Add(-10 * 24 * time.Hour), Name: "Young", Symbol: "YNG", Chart: "YNG", Verdict: model.Buy, Source: SourceTheme},
	); err != nil {
		t.Fatal(err)
	}
	paths := map[string]Path{
		"KEEP": {day(1, 100, 100), day(30, 130, 130)},
		"CHG":  {day(1, 100, 100), day(30, 90, 90)},
		"CLS":  {day(1, 100, 100), day(30, 125, 125)},
		"BB":   {day(1, 100, 100), day(30, 110, 110)},
		"YNG":  {{Opened: now.Add(-9 * 24 * time.Hour), Open: 100, Close: 100}, day(30, 100, 100)},
	}
	bench := Path{day(1, 500, 500), {Opened: now.Add(-9 * 24 * time.Hour), Open: 500, Close: 500}, day(30, 550, 550)}
	if err := s.Refresh(now, paths, bench, nil); err != nil {
		t.Fatal(err)
	}

	due := s.Live(now, 0)
	var symbols []string
	for _, r := range due {
		symbols = append(symbols, r.Symbol)
	}
	if got := strings.Join(symbols, ","); got != "KEEP,CHG,CLS,BB" {
		t.Fatalf("due = %s, want KEEP,CHG,CLS,BB", got)
	}
	var calls []Live
	for _, r := range due {
		calls = append(calls, NewLive(r, now, paths[r.Chart], bench, nil))
	}

	c := &fakeCompleter{reply: "=== " + due[0].Key() + `
ACTION: keep
WHY: Orders rose again in September.
=== ` + due[1].Key() + `
ACTION: Change.
WHY: It cut its data center guidance on 20 September.
VERDICT: SELL
CONFIDENCE: Medium
CASE: The orders the case rested on are shrinking.
CATALYST: Results on 29 Oct.
RISK: A new order.
=== ` + due[2].Key() + `
ACTION: close
WHY: The ruling it hinged on came on 15 September and the price has taken it in.
=== ` + due[3].Key() + `
ACTION: change
WHY: A rival's failure leaves it the only supplier,
from 25 September.
VERDICT: BUY BELOW 105
CONFIDENCE: low
CASE: The only supplier left.`}
	found, _, err := (&Recheck{Completer: c, Batch: 5}).Check(context.Background(), calls, s.RecordNote(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Today is Friday 2 October 2026.",
		"Your record so far, from the scorecard",
		"=== " + due[0].Key(), "Keeper, KEEP, called BUY on 1 September 2026 at medium confidence, a weekly theme pick under \"Power\".",
		"That is +20.0 points the way it called, on course.",
		"Its case: Data center orders keep growing.", "The event it named: Results on 29 Oct.",
		"That is -20.0 points the way it called, off course.",
		"That is 0.0 points against the index, on course.",
	} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the re-check request is missing %q:\n%s", want, c.prompt)
		}
	}
	if strings.Contains(c.prompt, "YNG") {
		t.Errorf("the call ten days old was looked at:\n%s", c.prompt)
	}
	if rt := found[due[1].Key()]; rt.Action != RecheckChange || rt.Verdict != model.Sell || rt.Confidence != "medium" || rt.Risk != "A new order." {
		t.Errorf("the change was read as %+v", rt)
	}
	if rt := found[due[3].Key()]; rt.Verdict != model.BuyBelow || rt.Entry != 105 || rt.Why != "A rival's failure leaves it the only supplier, from 25 September." {
		t.Errorf("the BUY BELOW was read as %+v", rt)
	}

	var done []RecheckDone
	for _, l := range calls {
		rt := found[l.Record.Key()]
		var next *Record
		if rt.Action == RecheckChange {
			r, ok := l.Changed(rt, now, nil)
			if !ok {
				t.Fatalf("%s gave no new call", l.Record.Symbol)
			}
			next = &r
		}
		if ok, err := s.ApplyRecheck(l.Record.Key(), now, rt, next); !ok || err != nil {
			t.Fatalf("ApplyRecheck(%s) = %v, %v", l.Record.Symbol, ok, err)
		}
		d := RecheckDone{Live: l, Action: rt.Action, Why: rt.Why}
		if next != nil {
			d.Now, d.Below = next.Verdict, next.Trigger
		}
		done = append(done, d)
	}

	state := map[string]string{}
	for _, r := range s.All() {
		k := r.Symbol + " " + r.Verdict
		switch {
		case r.Closed == nil:
			state[k] = "live"
		default:
			state[k] = r.Closed.Reason
		}
		if r.Source == SourceRecheck {
			state[k] += " from the re-check"
		}
	}
	for k, want := range map[string]string{
		"KEEP BUY":     "live",
		"CHG BUY":      ClosedReplaced,
		"CHG SELL":     "live from the re-check",
		"CLS SELL":     ClosedRechecked,
		"BB HOLD":      ClosedReplaced,
		"BB BUY BELOW": "live from the re-check",
		"YNG BUY":      "live",
	} {
		if state[k] != want {
			t.Errorf("%s is %q, want %q", k, state[k], want)
		}
	}
	for _, r := range s.All() {
		if r.Symbol == "BB" && r.Verdict == model.BuyBelow && (r.Trigger != 105 || r.Price != 110 || r.Confidence != "low") {
			t.Errorf("the new BUY BELOW is %+v", r)
		}
		if r.Symbol == "KEEP" && (r.Rechecked == nil || r.Rechecked.Action != RecheckKeep) {
			t.Errorf("the kept call does not say it was looked at: %+v", r.Rechecked)
		}
	}

	msg := strings.Join(RecheckMessages(done, 1, time.UTC), "\n")
	for _, want := range []string{
		"<b>🔁 Monthly re-check</b>",
		"Looked again at 4 calls: 1 kept, 2 changed, 1 closed. 1 could not be looked at this time, and stay as they were.",
		"🔄 <b>CHG</b> Changer · 1 Sep · BUY → <b>SELL</b> · <b>-20.0</b>\nIt cut its data center guidance on 20 September.",
		"🔄 <b>BB</b> Waiter · 1 Sep · HOLD → <b>BUY BELOW $105.00</b> · <b>0.0</b>",
		"✋ <b>CLS</b> Closer · 1 Sep · SELL · <b>-15.0</b>\nThe ruling it hinged on came on 15 September",
		"<b>Kept</b>: <b>KEEP</b> BUY",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the month's message is missing %q:\n%s", want, msg)
		}
	}

	// The scorecard counts the closed call, and lists it with the replaced.
	later := now.Add(24 * time.Hour)
	if err := s.Refresh(later, paths, bench, nil); err != nil {
		t.Fatal(err)
	}
	text := summaryText(s, later, paths, bench, nil, time.UTC)
	for _, want := range []string{
		"7 calls since 1 Sep: 4 live, 2 replaced by a newer call, 1 closed on a re-check.",
		"<b>Replaced</b> by a newer call, or ✋ closed on the monthly re-check",
		"✋ <b>CLS</b> Closer · 1 Sep, reaction · SELL · until 2 Oct · +25.0% vs +10.0% · <b>-15.0</b>",
		"🆕 <b>CHG</b> Changer · 2 Oct, re-check · SELL",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scorecard is missing %q:\n%s", want, text)
		}
	}
	if note := s.CompanyNote("CLS"); !strings.Contains(note, "Looked at again on 2 Oct (closed): The ruling it hinged on") ||
		!strings.Contains(note, "Closed on the monthly re-check on 2 Oct") {
		t.Errorf("company note = %q", note)
	}

	// Looked at once, nothing comes back the next week.
	if again := s.Live(now.Add(7*24*time.Hour), 0); len(again) != 0 {
		t.Errorf("calls came back a week later: %v", again)
	}
}

// A change to a BUY BELOW keeps the verdicts' rules: at or above today's
// price it is a BUY, and more than MaxBelow under it a HOLD. A change with
// no price today is not made.
func TestARecheckedBuyBelowKeepsTheRules(t *testing.T) {
	l := Live{Record: Record{Symbol: "X", Chart: "X", Verdict: model.Hold}, Price: 100}
	for entry, want := range map[float64]string{90: model.BuyBelow, 100: model.Buy, 120: model.Buy, 70: model.Hold, 0: model.Hold} {
		r, ok := l.Changed(Rethink{Action: RecheckChange, Verdict: model.BuyBelow, Entry: entry}, time.Now(), nil)
		if !ok || r.Verdict != want {
			t.Errorf("BUY BELOW %v at 100 became %q, want %q", entry, r.Verdict, want)
		}
		if want != model.BuyBelow && r.Trigger != 0 {
			t.Errorf("BUY BELOW %v kept its price as a %s", entry, r.Verdict)
		}
	}
	if _, ok := (Live{Record: l.Record}).Changed(Rethink{Verdict: model.Buy}, time.Now(), nil); ok {
		t.Errorf("a call with no price today was changed")
	}
}

// A block whose action is not keep, change or close, or a change without a
// verdict, is left out: the call stays as it was.
func TestARecheckReplyItCannotReadIsLeftOut(t *testing.T) {
	got := parseRethinks(`=== A 2026-09-01T00:00
ACTION: maybe
WHY: Unsure.
=== B 2026-09-01T00:00
ACTION: change
WHY: Something.
=== C 2026-09-01T00:00
ACTION: **Keep**
WHY: Still fine.`)
	if len(got) != 1 || got["C 2026-09-01T00:00"].Action != RecheckKeep {
		t.Errorf("parsed %+v, want only C kept", got)
	}
}

// A change the scorecard is given with the same verdict is a keep, and a
// call already closed is not touched.
func TestARecheckToTheSameVerdictIsAKeep(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r := Record{At: then, Symbol: "X", Chart: "X", Verdict: model.Buy}
	if err := s.Add(r); err != nil {
		t.Fatal(err)
	}
	next := Record{At: then.Add(30 * 24 * time.Hour), Symbol: "X", Chart: "X", Verdict: model.Buy}
	if ok, err := s.ApplyRecheck(r.Key(), next.At, Rethink{Action: RecheckChange, Why: "Still good."}, &next); !ok || err != nil {
		t.Fatal(ok, err)
	}
	all := s.All()
	if len(all) != 1 || all[0].Closed != nil || all[0].Rechecked.Action != RecheckKeep {
		t.Errorf("records = %+v", all)
	}
}
