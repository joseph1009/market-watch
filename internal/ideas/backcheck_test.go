package ideas

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The scorecard writes down how each call stands, picks the misses worth a
// look from that, takes the back-check's findings onto the record, and
// gives the verdicts the record of calls like theirs with the lessons from
// the misjudged ones.
func TestTheBackcheckLoop(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := then.Add(30 * 24 * time.Hour)
	if err := s.Add(
		Record{At: then, Name: "Winner", Symbol: "WIN", Chart: "WIN", Verdict: model.Buy, Confidence: "medium", Source: SourceTheme},
		Record{At: then, Name: "Loser", Symbol: "LOS", Chart: "LOS", Verdict: model.Buy, Confidence: "medium", Source: SourceTheme,
			Case: "Orders outrun the price.", Risk: "A customer cuts spending."},
		Record{At: then, Name: "Riser", Symbol: "RIS", Chart: "RIS", Verdict: model.Sell, Confidence: "low", Source: SourceReaction},
		Record{At: now.Add(-10 * 24 * time.Hour), Name: "Recent", Symbol: "REC", Chart: "REC", Verdict: model.Buy, Source: SourceTheme},
	); err != nil {
		t.Fatal(err)
	}
	paths := map[string]Path{
		"WIN": {day(1, 100, 100), day(30, 130, 130)},
		"LOS": {day(1, 100, 100), day(30, 90, 90)},
		"RIS": {day(1, 100, 100), day(30, 125, 125)},
		"REC": {{Opened: now.Add(-9 * 24 * time.Hour), Open: 100, Close: 100}, day(30, 80, 80)},
	}
	bench := Path{day(1, 500, 500), {Opened: now.Add(-9 * 24 * time.Hour), Open: 500, Close: 500}, day(30, 550, 550)}

	if err := s.Refresh(now, paths, bench, nil); err != nil {
		t.Fatal(err)
	}
	if st := s.All()[0].Standing; st == nil || !st.OnCourse || st.Called < 0.199 || st.Called > 0.201 {
		t.Fatalf("the winner stands %+v, want +20 points on course", st)
	}

	// The worst first, and not the one ten days in.
	misses := s.Misses(now, 0)
	// LOS is 20 points the wrong way, RIS 15.
	if len(misses) != 2 || misses[0].Symbol != "LOS" || misses[1].Symbol != "RIS" {
		t.Fatalf("misses = %v, want LOS then RIS", misses)
	}

	c := &fakeCompleter{reply: "=== " + misses[0].Key() + `
CAUSE: misjudged
KNOWABLE: yes
WHY: Its biggest customer had already cut its spending plan in August.
LESSON: Check the largest customer's latest spending plan before calling a supplier a BUY.
=== ` + misses[1].Key() + `
CAUSE: Sector.
KNOWABLE: no
WHY: Its whole industry rose 20% on a rate cut.
LESSON: none`}
	var list []Miss
	for _, r := range misses {
		m, ok := NewMiss(r, now, paths[r.Chart], bench, nil)
		if !ok {
			t.Fatalf("%s could not be priced", r.Symbol)
		}
		list = append(list, m)
	}
	found, _, err := (&Backcheck{Completer: c, Batch: 5}).Check(context.Background(), list, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"=== " + misses[0].Key(), "Loser, LOS, called BUY on 1 September 2026 at medium confidence, a weekly theme pick",
		"Its case: Orders outrun the price.", "The risk it named: A customer cuts spending.",
		"the share from 100.00 to 90.00 USD, -10.0%; the S&P 500 +10.0%. That is 20.0 points the wrong way.",
		"Its case was not kept",
	} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the back-check request is missing %q:\n%s", want, c.prompt)
		}
	}
	if rv := found[misses[1].Key()]; rv.Cause != CauseSector || rv.Knowable || rv.Lesson != "" {
		t.Errorf("sector review = %+v", rv)
	}
	for key, rv := range found {
		if ok, err := s.SetReview(key, rv); !ok || err != nil {
			t.Fatalf("SetReview(%s) = %v, %v", key, ok, err)
		}
	}

	// Checked once, a miss is not checked again within RecheckAfter.
	if again := s.Misses(now.Add(24*time.Hour), 0); len(again) != 0 {
		t.Errorf("checked misses came back the next day: %v", again)
	}

	note := s.RecordNote()
	for _, want := range []string{
		"weekly themes, from the numbers, BUY: 1 of 3 on course, on average -10.0 points",
		"reactions to the news, SELL: 0 of 1 on course",
		"- medium confidence: 1 of 2 on course",
		"1 its sector, 1 misjudged",
		"LOS, BUY on 1 Sep: Check the largest customer's latest spending plan",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the record note is missing %q:\n%s", want, note)
		}
	}
	if company := s.CompanyNote("LOS"); !strings.Contains(company, "BUY on 1 Sep 2026 (weekly, medium confidence): -20.0 points") ||
		!strings.Contains(company, "Checked afterwards: misjudged.") {
		t.Errorf("company note = %q", company)
	}

	// The scorecard tags each miss with its cause and points to /backcheck,
	// which has the rest.
	text := summaryText(s, now, paths, bench, nil, time.UTC)
	for _, want := range []string{
		"<b>Why the misses missed</b>, of 2 checked: 1 its sector, 1 misjudged. Send /backcheck for each one.",
		"❌ <b>LOS</b> Loser · 1 Sep, weekly · -10.0% vs +10.0% · <b>-20.0</b> · <i>misjudged</i>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scorecard is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Back-checks") || strings.Contains(text, "Lesson:") {
		t.Errorf("the back-checks came with the scorecard:\n%s", text)
	}

	checks := strings.Join(s.Backchecks(now, paths, bench, nil, time.UTC), "\n")
	for _, want := range []string{
		"<b>🔍 Back-checks</b>",
		"<b>Why the misses missed</b>, of 2 checked: 1 its sector, 1 misjudged.",
		"<b>LOS</b> Loser · BUY 1 Sep · <b>-20.0</b> · <i>misjudged</i>, <i>there to be seen</i>\nIts biggest customer had already cut its spending plan in August.\n<i>Lesson:</i> Check the largest customer's latest spending plan",
		"<b>RIS</b> Riser · SELL 1 Sep · <b>-15.0</b> · <i>its sector</i>\nIts whole industry rose 20% on a rate cut.",
	} {
		if !strings.Contains(checks, want) {
			t.Errorf("/backcheck is missing %q:\n%s", want, checks)
		}
	}
	if strings.Index(checks, "<b>LOS</b>") > strings.Index(checks, "<b>RIS</b>") {
		t.Errorf("/backcheck is not worst first:\n%s", checks)
	}
}

// With nothing checked yet, /backcheck says what it is and when it runs.
func TestBackcheckBeforeAnyCheck(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(s.Backchecks(time.Now(), nil, nil, nil, time.UTC), "\n")
	if !strings.Contains(got, "Each Monday, before the week's picks") || !strings.Contains(got, "None yet") {
		t.Errorf("an empty /backcheck = %q", got)
	}
}

// The verdicts read the record above the companies, each company's earlier
// calls under it, and write the record the call rests on and, leaning to
// buy, both cases weighed.
func TestAVerdictWeighsTheRecordAndBothCases(t *testing.T) {
	ideas := []model.Idea{{Name: "Dip Co", Ticker: "DIP", Exchange: "US", Kind: model.IdeaTheme, Accounts: true,
		Trading: &model.Trading{Last: 50}, Past: "Earlier calls on it, from the scorecard:\n- BUY on 1 Sep 2026 (weekly)"}}
	c := &fakeCompleter{reply: `=== DIP
VERDICT: BUY BELOW
CONFIDENCE: high
ENTRY: 46 -- its five-year median multiple
TRACK: Revenue has grown 20% a year for five years and the share rose after six of its last eight results.
NOW: At 50 it beats the index by 3 points at best.
WAIT: At 46 the multiple is back to its median, worth 8 points.
CHOSE: Waiting buys the same growth for less.`}
	got, _, err := (&Judge{Completer: c, Batch: 5}).Judge(context.Background(), ideas, nil, nil, nil, "Your record so far: weekly BUY 1 of 4.")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	g := got[0]
	if !strings.HasPrefix(g.Track, "Revenue has grown") || !strings.HasPrefix(g.BuyNow, "At 50") || !strings.HasPrefix(g.Wait, "At 46") || g.Chose != "Waiting buys the same growth for less." {
		t.Errorf("verdict = %+v", g)
	}
	for _, want := range []string{"Your record so far: weekly BUY 1 of 4.", "Earlier calls on it, from the scorecard:"} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, c.prompt)
		}
	}
	if strings.Index(c.prompt, "Your record so far") > strings.Index(c.prompt, "=== DIP") {
		t.Errorf("the record should come above the companies:\n%s", c.prompt)
	}
}
