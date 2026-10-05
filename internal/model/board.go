package model

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Board is the market in its parts on the last US session: the broad market,
// the sectors, a few industries and the macro funds, each with its moves, and
// the gaps between pairs of them that say what kind of day it was.
type Board struct {
	// Session is the US session the moves are on.
	Session time.Time   `json:"session"`
	Funds   []BoardFund `json:"funds,omitempty"`
	Gaps    []Gap       `json:"gaps,omitempty"`
}

// BoardFund is one fund, index or price on the board.
type BoardFund struct {
	Symbol string `json:"symbol"` // as shown: "SPY", "TNX", "BTC"
	Name   string `json:"name"`   // "S&P 500"
	Group  string `json:"group"`  // "The broad market"

	// Yield marks a rate, whose moves are in percentage points of yield
	// rather than percent of the level.
	Yield bool `json:"yield,omitempty"`

	Level float64 `json:"level"`
	Day   float64 `json:"day"`
	Week  float64 `json:"week"`
	Month float64 `json:"month"`
}

// Gap is one fund's move less another's, set against the usual size of that
// gap, and what it says.
type Gap struct {
	Name string `json:"name"` // "Equal weight vs the S&P 500"
	A    string `json:"a"`    // the first fund, as shown
	B    string `json:"b"`

	// Day and Month are the first fund's move less the second's, in
	// percentage points. UsualDay and UsualMonth are the typical size of
	// each over the past year: zero where the history is too short to say.
	Day        float64 `json:"day"`
	Month      float64 `json:"month"`
	UsualDay   float64 `json:"usual_day,omitempty"`
	UsualMonth float64 `json:"usual_month,omitempty"`

	// Window is what the gap is read on: the day, or the month where the
	// month's gap is the larger against its usual size. Unusual is set where
	// it is at least UnusualTimes that size.
	Unusual bool   `json:"unusual,omitempty"`
	Window  string `json:"window,omitempty"` // "" for the day, "month"

	Reading string `json:"reading"` // what it says, in a sentence
	Short   string `json:"short"`   // the same in a few words, for the chat
}

// UnusualTimes is how many times its usual size a gap must be to stand out.
// At one and a half, a gap of thirteen stands out on about one day in eight,
// so most days something does, and rarely more than a handful.
const UnusualTimes = 1.5

// Times is the gap as a multiple of its usual size, over the window it is
// read on; zero where there is no usual size.
func (g Gap) Times() float64 {
	gap, usual := g.Day, g.UsualDay
	if g.Window == "month" {
		gap, usual = g.Month, g.UsualMonth
	}
	if usual <= 0 {
		return 0
	}
	return math.Abs(gap) / usual
}

// Standouts are the unusual gaps, the largest against their usual first, at
// most n of them.
func (b Board) Standouts(n int) []Gap {
	var out []Gap
	for _, g := range b.Gaps {
		if g.Unusual {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Times() > out[j].Times() })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Lines are the standouts in a few words each, for the lines under the
// overview: those of the last session, then those of the past month, as
// "big tech led · the giants carried the index". Either is empty where
// nothing stood out over it.
func (b Board) Lines(n int) (session, month string) {
	var day, past []string
	for _, g := range b.Standouts(n) {
		if g.Window == "month" {
			past = append(past, g.Short)
		} else {
			day = append(day, g.Short)
		}
	}
	return strings.Join(day, " · "), strings.Join(past, " · ")
}

// Groups are the board's funds by group, in the order first met.
func (b Board) Groups() (names []string, funds map[string][]BoardFund) {
	funds = map[string][]BoardFund{}
	for _, f := range b.Funds {
		if _, ok := funds[f.Group]; !ok {
			names = append(names, f.Group)
		}
		funds[f.Group] = append(funds[f.Group], f)
	}
	return names, funds
}

// Move writes one of a fund's moves: "+1.2%", "-0.4%", "flat", or for a
// yield the change in percentage points, "+0.05".
func (f BoardFund) Move(v float64) string {
	if f.Yield {
		if math.Abs(v) < 0.005 {
			return "flat"
		}
		return fmt.Sprintf("%+.2f", v)
	}
	return Quote{Percent: v}.Move()
}

// LevelText writes where a fund stands: "5.30%" for a yield, "$85,601" for
// Bitcoin, "$763.99" for the rest.
func (f BoardFund) LevelText() string {
	switch {
	case f.Yield:
		return fmt.Sprintf("%.2f%%", f.Level)
	case f.Level >= 10000:
		return "$" + thousands(int64(math.Round(f.Level)))
	default:
		return fmt.Sprintf("$%.2f", f.Level)
	}
}

// Points writes a gap in percentage points: "+0.8 pts", "-1.2 pts".
func Points(v float64) string {
	if math.Abs(v) < 0.05 {
		return "0.0 pts"
	}
	return fmt.Sprintf("%+.1f pts", v)
}

func thousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
