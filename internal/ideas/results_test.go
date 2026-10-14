package ideas

import (
	"strings"
	"testing"
	"time"
)

// A results day is measured from the close before it to the close of the
// session after, so a release after the close is caught as well as one
// before the open; one the history does not reach is left out.
func TestResultsMovesSpanTheAnnouncement(t *testing.T) {
	share := Path{day(1, 100, 100), day(2, 100, 100), day(3, 108, 110), day(4, 110, 112), day(10, 90, 90), day(11, 85, 81)}
	bench := Path{day(1, 500, 500), day(2, 500, 500), day(3, 500, 505), day(4, 505, 505), day(10, 500, 500), day(11, 500, 495)}
	dates := []time.Time{
		time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),  // after the close on the 3rd, or before the open
		time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), // the last session: no session after it
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),  // before the history starts
	}
	got := ResultsMoves(dates, share, bench)
	if len(got) != 2 {
		t.Fatalf("got %d moves, want 2: %+v", len(got), got)
	}
	if got[0].Date.Day() != 11 || got[1].Date.Day() != 3 {
		t.Errorf("not newest first: %+v", got)
	}
	// 100 on the 2nd to 112 on the 4th; the index 500 to 505.
	if m := got[1]; m.Share < 0.119 || m.Share > 0.121 || m.Index < 0.0099 || m.Index > 0.0101 {
		t.Errorf("3 Sep = %+v, want +12%% against +1%%", m)
	}
	text := ResultsFacts(got)
	for _, want := range []string{"3 Sep 2026: +12.0% (S&P 500 +1.0%)", "11 Sep 2026: -10.0% (S&P 500 -1.0%)", "It beat the index on 1 of 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("facts missing %q:\n%s", want, text)
		}
	}
}
