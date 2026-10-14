package ideas

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// A company has one live call. A different verdict replaces it, closing it
// on the new one's day; the same verdict again is not recorded, and the live
// call carries on from its own day.
func TestACompanyHasOneLiveCall(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	d := func(day int) time.Time { return time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC) }
	if err := s.Add(Record{At: d(1), Symbol: "MU", Chart: "MU", Verdict: model.Hold, Source: SourceAnalysis}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Record{At: d(2), Symbol: "MU", Chart: "MU", Verdict: model.Buy, Source: SourceAnalysis}); err != nil {
		t.Fatal(err)
	}
	if added, err := s.AddOnce(Record{At: d(4), Symbol: "mu", Chart: "MU", Verdict: model.Buy, Source: SourceTheme}); added || err != nil {
		t.Fatalf("the same verdict again was added: %v, %v", added, err)
	}
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(all), all)
	}
	if c := all[0].Closed; c == nil || c.Reason != ClosedReplaced || !c.At.Equal(d(2)) {
		t.Errorf("the HOLD was not closed as replaced on 2 Sep: %+v", c)
	}
	if all[1].Closed != nil {
		t.Errorf("the BUY is not live: %+v", all[1].Closed)
	}
}

// A record kept before the rule is put in order once, on loading: the later
// of two different calls replaces the earlier, and a repeat of the live call
// is closed as a repeat, which is not counted.
func TestOldRecordsAreGivenOneLiveCall(t *testing.T) {
	path := t.TempDir() + "/scorecard.json"
	if err := os.WriteFile(path, []byte(`[
		{"at":"2026-09-30T03:00:00Z","name":"MICRON TECHNOLOGY INC","symbol":"MU","chart":"MU","verdict":"HOLD","price":0},
		{"at":"2026-10-01T03:00:00Z","name":"MICRON TECHNOLOGY INC","symbol":"MU","chart":"MU","verdict":"BUY","price":0},
		{"at":"2026-10-03T03:00:00Z","name":"MICRON TECHNOLOGY INC","symbol":"MU","chart":"MU","verdict":"BUY","price":0},
		{"at":"2026-10-03T04:00:00Z","name":"NVIDIA CORP","symbol":"NVDA","chart":"NVDA","verdict":"HOLD","price":0}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadScorecard(path)
	if err != nil {
		t.Fatal(err)
	}
	all := s.All()
	reasons := []string{}
	for _, r := range all {
		if r.Closed == nil {
			reasons = append(reasons, "live")
		} else {
			reasons = append(reasons, r.Closed.Reason)
		}
	}
	if got := strings.Join(reasons, ","); got != "replaced,live,repeat,live" {
		t.Errorf("closed = %s, want replaced,live,repeat,live", got)
	}
	// Saved, so it is done once.
	again, _ := LoadScorecard(path)
	if again.All()[2].Closed == nil {
		t.Errorf("the tidy-up was not saved")
	}
}

// A live call off course on every check for DropAfter is dropped, with its
// result frozen on the day; one that comes back on course starts the count
// again. A HOLD is on course within Clearly of the index.
func TestACallOffCourseForEightWeeksIsDropped(t *testing.T) {
	s, err := LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Add(
		Record{At: start, Name: "Lagger", Symbol: "LAG", Chart: "LAG", Verdict: model.Buy},
		Record{At: start, Name: "Bouncer", Symbol: "BNC", Chart: "BNC", Verdict: model.Buy},
		Record{At: start, Name: "Steady", Symbol: "STD", Chart: "STD", Verdict: model.Hold},
	); err != nil {
		t.Fatal(err)
	}
	at := func(days int) time.Time { return start.Add(time.Duration(days) * 24 * time.Hour) }
	session := func(days int, close float64) Session {
		return Session{Opened: at(days).Add(13*time.Hour + 30*time.Minute), Open: close, Close: close}
	}
	// The index goes nowhere. LAG sits 10% down throughout; BNC is down at
	// first, back up in week five; STD drifts 3% up, within a HOLD's band.
	week := func(n int, lag, bnc, std float64) (map[string]Path, Path) {
		paths := map[string]Path{
			"LAG": {session(1, 100), session(n*7, lag)},
			"BNC": {session(1, 100), session(n*7, bnc)},
			"STD": {session(1, 100), session(n*7, std)},
		}
		return paths, Path{session(1, 500), session(n*7, 500)}
	}
	for n := 2; n <= 11; n++ {
		bnc := 90.0
		if n == 5 {
			bnc = 110
		}
		paths, bench := week(n, 90, bnc, 103)
		if err := s.Refresh(at(n*7), paths, bench, nil); err != nil {
			t.Fatal(err)
		}
	}
	all := s.All()
	lag, bnc, std := all[0], all[1], all[2]
	// Off course from week 2, dropped at week 10, eight weeks on.
	if lag.Closed == nil || lag.Closed.Reason != ClosedDropped || !lag.Closed.At.Equal(at(70)) {
		t.Fatalf("LAG = %+v, want dropped on day 70", lag.Closed)
	}
	if st := lag.Standing; st == nil || !st.Final || st.Called > -0.099 || st.Called < -0.101 {
		t.Errorf("LAG's final standing = %+v, want -10 points, final", st)
	}
	// Back on course in week 5, so its count began again in week 6.
	if bnc.Closed != nil {
		t.Errorf("BNC was dropped though it came back on course: %+v", bnc.Closed)
	}
	if std.Closed != nil || std.Standing == nil || !std.Standing.OnCourse {
		t.Errorf("STD, a HOLD 3 points from the index, = %+v, %+v", std.Closed, std.Standing)
	}

	// The dropped call keeps its result whatever the share does later, and
	// is listed under Dropped, not among the live calls.
	paths, bench := week(12, 150, 90, 103)
	text := summaryText(s, at(84), paths, bench, nil, time.UTC)
	for _, want := range []string{
		"3 calls since 1 Sep: 2 live, 1 dropped.",
		"<b>Dropped</b>, after eight weeks off course: the final result\n⛔ <b>LAG</b> Lagger · 1 Sep, old look · BUY · until 10 Nov · -10.0% vs +0.0% · <b>-10.0</b>",
		"✅ <b>STD</b> Steady",
		"❌ <b>BNC</b> Bouncer · 1 Sep, old look · -10.0% vs +0.0% · <b>-10.0</b> · <i>off course 6 weeks</i>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scorecard is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<b>BUY</b>\n✅ <b>LAG</b>") || strings.Contains(text, "❌ <b>LAG</b>") {
		t.Errorf("the dropped call is listed among the live ones:\n%s", text)
	}
	if misses := s.Misses(at(77), 0); len(misses) != 1 || misses[0].Symbol != "BNC" {
		t.Errorf("misses = %v, want the live BNC only", misses)
	}
}
