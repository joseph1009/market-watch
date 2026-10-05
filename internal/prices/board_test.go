package prices

import (
	"math"
	"strings"
	"testing"
	"time"
)

// boardDay is a Monday, and boardNow the morning after in Singapore, before
// New York opens: the last finished session is boardDay.
var (
	boardDay = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	boardNow = time.Date(2026, 10, 6, 11, 30, 0, 0, time.UTC)
)

// weekdays builds a history of weekday closes ending on the last date, each
// from the day's move in percent.
func weekdays(symbol string, last time.Time, n int, moveOn func(i int) float64) Series {
	var dates []time.Time
	for d := last; len(dates) < n; d = d.AddDate(0, 0, -1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			dates = append([]time.Time{d}, dates...)
		}
	}
	s := Series{Symbol: symbol}
	price := 100.0
	for i, d := range dates {
		if i > 0 {
			price *= 1 + moveOn(i)/100
		}
		s.Bars = append(s.Bars, Bar{Date: d, Close: price})
	}
	return s
}

// wobble is a small daily move that differs by fund, so gaps have a usual
// size to be measured against.
func wobble(seed float64) func(int) float64 {
	return func(i int) float64 { return 0.4 * math.Sin(float64(i)*seed) }
}

func TestANarrowDayStandsOut(t *testing.T) {
	const n = 300
	spy := weekdays("SPY", boardDay, n, wobble(1.3))
	rsp := weekdays("RSP", boardDay, n, func(i int) float64 {
		if i == n-1 {
			return -2 // the giants rose while the typical company fell
		}
		return wobble(1.7)(i)
	})
	spy.Bars[n-1].Close = spy.Bars[n-2].Close * 1.005

	board := MakeBoard(map[string]Series{"SPY": spy, "RSP": rsp}, boardNow)
	if !board.Session.Equal(boardDay) {
		t.Fatalf("session = %s, want %s", board.Session, boardDay)
	}
	if len(board.Funds) != 2 || len(board.Gaps) != 1 {
		t.Fatalf("funds %d, gaps %d; want 2 and 1", len(board.Funds), len(board.Gaps))
	}
	g := board.Gaps[0]
	if math.Abs(g.Day-(-2.5)) > 0.01 {
		t.Errorf("gap = %.2f, want -2.5 points", g.Day)
	}
	if !g.Unusual || g.Window != "" || !strings.HasPrefix(g.Reading, "Narrow") {
		t.Errorf("gap = %+v, want an unusual narrow day", g)
	}
	if session, _ := board.Lines(4); session != "the giants carried the index" {
		t.Errorf("session line = %q, want the giants carried the index", session)
	}
}

func TestAnOrdinaryGapIsNotFlagged(t *testing.T) {
	spy := weekdays("SPY", boardDay, 300, wobble(1.3))
	rsp := weekdays("RSP", boardDay, 300, wobble(1.7))
	board := MakeBoard(map[string]Series{"SPY": spy, "RSP": rsp}, boardNow)
	if len(board.Gaps) != 1 || board.Gaps[0].Unusual {
		t.Errorf("gaps = %+v, want one ordinary gap", board.Gaps)
	}
	if session, month := board.Lines(4); session != "" || month != "" {
		t.Errorf("lines = %q, %q; want none", session, month)
	}
}

// A session still running in New York is not the last session yet.
func TestASessionUnderWayIsNotRead(t *testing.T) {
	spy := weekdays("SPY", boardDay, 300, wobble(1.3))
	during := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC) // 11:00 in New York
	board := MakeBoard(map[string]Series{"SPY": spy}, during)
	if want := boardDay.AddDate(0, 0, -3); !board.Session.Equal(want) {
		t.Errorf("session = %s, want the Friday before, %s", board.Session.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

// Bitcoin trades every day, and is read from Friday's close to Monday's,
// as the US market is.
func TestBitcoinIsReadOnTheUSSessions(t *testing.T) {
	spy := weekdays("SPY", boardDay, 300, wobble(1.3))
	btc := Series{Symbol: "BTC-USD"}
	for d := boardDay.AddDate(0, 0, -420); !d.After(boardDay.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		btc.Bars = append(btc.Bars, Bar{Date: d, Close: 100})
	}
	friday := len(btc.Bars) - 5
	btc.Bars[friday].Close = 100
	btc.Bars[friday+1].Close = 130 // Saturday
	btc.Bars[friday+2].Close = 90  // Sunday
	btc.Bars[friday+3].Close = 110 // Monday
	btc.Bars[friday+4].Close = 50  // Tuesday, not yet a session
	board := MakeBoard(map[string]Series{"SPY": spy, "BTC-USD": btc}, boardNow)
	for _, f := range board.Funds {
		if f.Symbol == "BTC" {
			if math.Abs(f.Day-10) > 0.01 || f.Level != 110 {
				t.Errorf("Bitcoin = %+v, want +10%% from Friday to Monday, at 110", f)
			}
			return
		}
	}
	t.Error("no Bitcoin on the board")
}

// A fund whose history stops before the session is left out, and so are
// its gaps, rather than shown as flat.
func TestAStaleFundIsLeftOut(t *testing.T) {
	spy := weekdays("SPY", boardDay, 300, wobble(1.3))
	rsp := weekdays("RSP", boardDay.AddDate(0, 0, -3), 300, wobble(1.7))
	board := MakeBoard(map[string]Series{"SPY": spy, "RSP": rsp}, boardNow)
	if len(board.Funds) != 1 || len(board.Gaps) != 0 {
		t.Errorf("funds %+v, gaps %+v; want SPY alone", board.Funds, board.Gaps)
	}
}

// The 10-year yield moves in percentage points, and copper against gold is
// read beside it.
func TestCopperAndGoldAreReadBesideTheYield(t *testing.T) {
	spy := weekdays("SPY", boardDay, 300, wobble(1.3))
	cper := weekdays("CPER", boardDay, 300, wobble(1.1))
	gld := weekdays("GLD", boardDay, 300, wobble(0.7))
	cper.Bars[299].Close = cper.Bars[298].Close * 0.97
	tnx := weekdays("^TNX", boardDay, 300, func(int) float64 { return 0 })
	tnx.Bars[299].Close = tnx.Bars[298].Close + 0.08
	board := MakeBoard(map[string]Series{"SPY": spy, "CPER": cper, "GLD": gld, "^TNX": tnx}, boardNow)
	var found bool
	for _, f := range board.Funds {
		if f.Symbol == "TNX" {
			found = true
			if math.Abs(f.Day-0.08) > 1e-9 || f.Move(f.Day) != "+0.08" {
				t.Errorf("yield moved %v (%s), want +0.08 points", f.Day, f.Move(f.Day))
			}
		}
	}
	if !found {
		t.Fatal("no 10-year yield on the board")
	}
	if len(board.Gaps) != 1 {
		t.Fatalf("gaps = %+v, want copper vs gold", board.Gaps)
	}
	if r := board.Gaps[0].Reading; !strings.HasPrefix(r, "Gold over copper") || !strings.Contains(r, "Yet the 10-year yield rose") {
		t.Errorf("reading = %q", r)
	}
}
