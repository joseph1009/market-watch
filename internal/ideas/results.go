package ideas

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ResultsMove is how a share took one results announcement: from the close
// before the day it was filed to the close of the session after, which
// catches a release before the open and one after the close alike, and the
// index over the same sessions.
type ResultsMove struct {
	Date         time.Time
	Share, Index float64
}

// ResultsMoves sets each results date against the share's sessions and the
// index's, newest first, leaving out a date the history does not cover on
// both sides.
func ResultsMoves(dates []time.Time, share, bench Path) []ResultsMove {
	var out []ResultsMove
	for _, d := range dates {
		s, ok := around(share, d)
		if !ok {
			continue
		}
		i, ok := around(bench, d)
		if !ok {
			continue
		}
		out = append(out, ResultsMove{Date: d, Share: s, Index: i})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Date.After(out[b].Date) })
	return out
}

// around is a path's move from the last close before day d to the close of
// the session after the first one on or after it.
func around(p Path, d time.Time) (float64, bool) {
	day := d.UTC().Truncate(24 * time.Hour)
	before, on := -1, -1
	for i, s := range p {
		if s.Close <= 0 {
			continue
		}
		if s.Opened.UTC().Truncate(24 * time.Hour).Before(day) {
			before = i
			continue
		}
		on = i
		break
	}
	if before < 0 || on < 0 {
		return 0, false
	}
	after := on
	if on+1 < len(p) && p[on+1].Close > 0 {
		after = on + 1
	}
	return p[after].Close/p[before].Close - 1, true
}

// ResultsFacts writes the moves for the model: each announcement's move
// against the index's, and in a line how big and which way they have run, so
// today's move or news can be set against the share's habit.
func ResultsFacts(moves []ResultsMove) string {
	if len(moves) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nHow the share took its results, from the close before each announcement to the close the session after, against the S&P 500 over the same sessions:\n")
	up, size := 0, make([]float64, 0, len(moves))
	for _, m := range moves {
		fmt.Fprintf(&b, "- %s: %+.1f%% (S&P 500 %+.1f%%)\n", m.Date.Format("2 Jan 2006"), 100*m.Share, 100*m.Index)
		if m.Share-m.Index > 0 {
			up++
		}
		size = append(size, math.Abs(m.Share-m.Index))
	}
	sort.Float64s(size)
	fmt.Fprintf(&b, "It beat the index on %d of %d; the typical move against it was %.1f points either way.\n",
		up, len(moves), 100*size[len(size)/2])
	return b.String()
}
