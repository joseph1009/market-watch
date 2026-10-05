package prices

import (
	"math"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The board's groups, in the order the page shows them.
const (
	GroupBroad      = "The broad market"
	GroupSectors    = "The 11 sectors"
	GroupIndustries = "Industries to watch"
	GroupMacro      = "Commodities, rates, the dollar and Bitcoin"
)

// boardFund is one line of the board: the symbol the chart source knows it
// by, the one the reader is shown, and its name.
type boardFund struct {
	Chart, Shown, Name, Group string
	Yield                     bool
}

// boardFunds are the market in its parts, read from the daily charts. They
// are funds rather than indices for the reason Benchmarks gives, but for the
// 10-year yield, which the chart source carries as a level, and Bitcoin,
// which trades every day and is read on the US session's dates.
var boardFunds = []boardFund{
	{"SPY", "SPY", "S&P 500", GroupBroad, false},
	{"RSP", "RSP", "S&P 500, equal weight", GroupBroad, false},
	{"QQQ", "QQQ", "Nasdaq 100", GroupBroad, false},
	{"IWM", "IWM", "US small companies", GroupBroad, false},
	{"EFA", "EFA", "Other rich countries", GroupBroad, false},
	{"EEM", "EEM", "Emerging markets", GroupBroad, false},

	{"XLK", "XLK", "Technology", GroupSectors, false},
	{"XLC", "XLC", "Communication services", GroupSectors, false},
	{"XLY", "XLY", "Consumer discretionary", GroupSectors, false},
	{"XLP", "XLP", "Consumer staples", GroupSectors, false},
	{"XLF", "XLF", "Financials", GroupSectors, false},
	{"XLV", "XLV", "Healthcare", GroupSectors, false},
	{"XLI", "XLI", "Industrials", GroupSectors, false},
	{"XLE", "XLE", "Energy", GroupSectors, false},
	{"XLB", "XLB", "Materials", GroupSectors, false},
	{"XLU", "XLU", "Utilities", GroupSectors, false},
	{"XLRE", "XLRE", "Real estate", GroupSectors, false},

	{"SMH", "SMH", "Semiconductors", GroupIndustries, false},
	{"IGV", "IGV", "Software", GroupIndustries, false},
	{"KRE", "KRE", "Regional banks", GroupIndustries, false},
	{"XBI", "XBI", "Biotech", GroupIndustries, false},

	{"USO", "USO", "Crude oil", GroupMacro, false},
	{"GLD", "GLD", "Gold", GroupMacro, false},
	{"CPER", "CPER", "Copper", GroupMacro, false},
	{"^TNX", "TNX", "10-year Treasury yield", GroupMacro, true},
	{"TLT", "TLT", "Long Treasury bonds", GroupMacro, false},
	{"UUP", "UUP", "US dollar", GroupMacro, false},
	{"BTC-USD", "BTC", "Bitcoin", GroupMacro, false},
}

// BoardSymbols are the chart symbols the board reads.
func BoardSymbols() []string {
	out := make([]string, len(boardFunds))
	for i, f := range boardFunds {
		out[i] = f.Chart
	}
	return out
}

// OnBoard reports whether a quote symbol is one the board shows, so the
// brief's prompt need not list it twice.
func OnBoard(symbol string) bool {
	for _, f := range boardFunds {
		if strings.EqualFold(f.Chart, symbol) || strings.EqualFold(f.Shown, symbol) {
			return true
		}
	}
	return false
}

// moves are a pair's moves over one window, as a reading is given them.
type moves struct {
	A, B, Gap float64
	// Yield is the 10-year yield's change over the same window, in
	// percentage points, where HasYield.
	Yield    float64
	HasYield bool
}

// pair is one gap the board reads: A's move less B's, and what each side of
// it says, in a sentence and in a few words.
type pair struct {
	Name string
	A, B string // chart symbols
	Read func(m moves) (reading, short string)
}

// either reads a gap by which side came out ahead.
func either(aheadLong, aheadShort, behindLong, behindShort string) func(moves) (string, string) {
	return func(m moves) (string, string) {
		if m.Gap >= 0 {
			return aheadLong, aheadShort
		}
		return behindLong, behindShort
	}
}

// pairs are the relationships the board reads. Each is a question a market
// professional asks of a day: was it broad, was it hungry for risk, did the
// bond market agree.
var pairs = []pair{
	{"Equal weight vs the S&P 500", "RSP", "SPY", either(
		"Broad: the typical company did better than the giants that dominate the index.", "the typical share beat the giants",
		"Narrow: the biggest companies carried the index, and the typical one did worse.", "the giants carried the index")},
	{"Small vs large companies", "IWM", "SPY", either(
		"Appetite for risk: small companies, more tied to the US economy and to borrowing costs, did better.", "small companies led",
		"Caution: money stayed with the largest companies.", "large companies led")},
	{"Nasdaq 100 vs the S&P 500", "QQQ", "SPY", either(
		"Big technology companies led the market.", "big tech led",
		"Big technology companies lagged the market.", "big tech lagged")},
	{"Cyclical vs defensive", "XLY", "XLP", either(
		"Wants over needs: shops, carmakers and travel beat household staples, a bet that growth holds up.", "cyclicals over defensives",
		"Needs over wants: household staples beat shops, carmakers and travel, as when growth worries.", "defensives over cyclicals")},
	{"Chips vs software", "SMH", "IGV", either(
		"Money went to chipmakers over software companies.", "chips over software",
		"Money went to software companies over chipmakers.", "software over chips")},
	{"Regional banks vs all financials", "KRE", "XLF", either(
		"Smaller lenders did better: less worry about their loans, their deposits and the US economy.", "regional banks led",
		"Smaller lenders lagged: a sign of worry about their loans, their deposits or the US economy.", "regional banks lagged")},
	{"Biotech vs healthcare", "XBI", "XLV", either(
		"Speculative biotech beat the steadier health companies: appetite for risk, often on hopes of lower rates.", "biotech led",
		"Speculative biotech lagged the steadier health companies.", "biotech lagged")},
	{"Energy shares vs oil", "XLE", "USO", func(m moves) (string, string) {
		switch {
		case m.B < 0 && m.Gap >= 0:
			return "Energy shares did better than oil as it fell: investors expect the fall not to last.", "energy shares shrugged off oil's fall"
		case m.B < 0:
			return "Energy shares did worse than oil as it fell: investors expect lower oil to last.", "energy shares fell further than oil"
		case m.Gap >= 0:
			return "Energy shares did better than oil as it rose: investors expect higher oil to last.", "energy shares outran oil"
		default:
			return "Energy shares did worse than oil as it rose: investors doubt the rise will last.", "energy shares doubted oil's rise"
		}
	}},
	{"Copper vs gold", "CPER", "GLD", func(m moves) (string, string) {
		reading, short := "Copper over gold: a bet on growth, since copper goes into factories and buildings.", "copper over gold"
		if m.Gap < 0 {
			reading, short = "Gold over copper: caution about growth, since gold is held for safety.", "gold over copper"
		}
		if m.HasYield && math.Abs(m.Yield) >= 0.005 {
			agrees := (m.Gap >= 0) == (m.Yield >= 0)
			switch {
			case agrees && m.Yield >= 0:
				reading += " The 10-year yield rose with it, so bonds agree."
			case agrees:
				reading += " The 10-year yield fell with it, so bonds agree."
			case m.Yield >= 0:
				reading += " Yet the 10-year yield rose, so bonds do not agree."
			default:
				reading += " Yet the 10-year yield fell, so bonds do not agree."
			}
		}
		return reading, short
	}},
	{"Shares vs long bonds", "SPY", "TLT", func(m moves) (string, string) {
		switch {
		case m.A >= 0 && m.B >= 0:
			return "Shares and bonds rose together: lower interest rates lifted both.", "shares and bonds rose together"
		case m.A < 0 && m.B < 0:
			return "Shares and bonds fell together: higher interest rates weighed on both.", "shares and bonds fell together"
		case m.A >= 0:
			return "Out of bonds and into shares: appetite for risk.", "out of bonds, into shares"
		default:
			return "Out of shares and into bonds: a flight to safety.", "a flight to bonds"
		}
	}},
	{"Emerging markets vs the dollar", "EEM", "UUP", either(
		"Emerging markets beat the dollar: appetite for risk abroad, helped when the dollar is weak.", "emerging markets over the dollar",
		"A firmer dollar weighed on emerging markets, whose debts are often in dollars.", "the dollar over emerging markets")},
	{"US vs other rich countries", "SPY", "EFA", either(
		"US shares beat those of other rich countries.", "the US over other rich markets",
		"Other rich countries' shares beat the US.", "other rich markets over the US")},
	{"Bitcoin vs the Nasdaq 100", "BTC-USD", "QQQ", either(
		"Bitcoin beat big technology: speculative appetite.", "Bitcoin over big tech",
		"Bitcoin lagged big technology.", "big tech over Bitcoin")},
}

// usualSessions is how far back a gap's usual size is measured: about a
// trading year.
const usualSessions = 252

// leastSamples is the fewest gaps a usual size is worth measuring from.
const leastSamples = 60

// MakeBoard reads the board from the funds' daily histories, keyed by chart
// symbol, on the last US session finished by now. The S&P 500 fund decides
// the sessions, so every fund is read over the same days: Bitcoin, which
// trades at weekends, from its close on the Friday to its close on the
// Monday. A fund whose history stops before the session is left out, and
// with it its gaps.
func MakeBoard(series map[string]Series, now time.Time) model.Board {
	spy, ok := series[MarketSymbol]
	if !ok {
		return model.Board{}
	}
	dates := sessions(spy.Bars, now)
	if len(dates) < 2 {
		return model.Board{}
	}
	session, previous := dates[len(dates)-1], dates[len(dates)-2]
	board := model.Board{Session: session}

	fresh := map[string]Series{}
	for _, f := range boardFunds {
		s, ok := series[f.Chart]
		if !ok {
			continue
		}
		// The close it is read at must be the session's, or at least later
		// than the session before: an older one would show a day it did not
		// trade as a day it did not move.
		last, ok := barAt(s.Bars, session)
		if !ok || !last.Date.After(previous) {
			continue
		}
		day, ok1 := move(s.Bars, previous, session, f.Yield)
		week, ok2 := move(s.Bars, session.AddDate(0, 0, -7), session, f.Yield)
		month, ok3 := move(s.Bars, session.AddDate(0, 0, -30), session, f.Yield)
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		fresh[f.Chart] = s
		board.Funds = append(board.Funds, model.BoardFund{
			Symbol: f.Shown, Name: f.Name, Group: f.Group, Yield: f.Yield,
			Level: last.Close, Day: day, Week: week, Month: month,
		})
	}

	shown := map[string]string{}
	for _, f := range boardFunds {
		shown[f.Chart] = f.Shown
	}
	tnx, hasTNX := fresh["^TNX"]
	for _, p := range pairs {
		a, okA := fresh[p.A]
		b, okB := fresh[p.B]
		if !okA || !okB {
			continue
		}
		g := model.Gap{Name: p.Name, A: shown[p.A], B: shown[p.B]}
		read := func(from time.Time) moves {
			ma, _ := move(a.Bars, from, session, false)
			mb, _ := move(b.Bars, from, session, false)
			m := moves{A: ma, B: mb, Gap: ma - mb}
			if hasTNX {
				m.Yield, m.HasYield = move(tnx.Bars, from, session, true)
			}
			return m
		}
		day, month := read(previous), read(session.AddDate(0, 0, -30))
		g.Day, g.Month = day.Gap, month.Gap
		g.UsualDay = usual(a.Bars, b.Bars, dates, func(d time.Time, i int) time.Time { return dates[i-1] })
		g.UsualMonth = usual(a.Bars, b.Bars, dates, func(d time.Time, _ int) time.Time { return d.AddDate(0, 0, -30) })

		// Read on the day, or on the month where the month's gap is the
		// larger against its usual size.
		on := day
		overDay := g.Times()
		if g.Window = "month"; g.Times() > overDay {
			on = month
		} else {
			g.Window = ""
		}
		g.Unusual = g.Times() >= model.UnusualTimes
		g.Reading, g.Short = p.Read(on)
		board.Gaps = append(board.Gaps, g)
	}
	return board
}

// sessions are the dates of the finished sessions in a New York history: a
// session dated today is left out until New York's close, since until then
// it is not one.
func sessions(bars []Bar, now time.Time) []time.Time {
	ny := now.In(newYork())
	today := time.Date(ny.Year(), ny.Month(), ny.Day(), 0, 0, 0, 0, time.UTC)
	closed := ny.Hour() >= 16
	var out []time.Time
	for _, b := range bars {
		if b.Close <= 0 || b.Date.After(today) || (b.Date.Equal(today) && !closed) {
			continue
		}
		out = append(out, b.Date)
	}
	return out
}

// barAt is the last bar on or before a date.
func barAt(bars []Bar, date time.Time) (Bar, bool) {
	var found Bar
	ok := false
	for _, b := range bars {
		if b.Date.After(date) {
			break
		}
		if b.Close > 0 {
			found, ok = b, true
		}
	}
	return found, ok
}

// move is the change from the close on or before one date to the close on
// or before another: in percent, or for a yield in percentage points.
func move(bars []Bar, from, to time.Time, yield bool) (float64, bool) {
	start, ok1 := barAt(bars, from)
	end, ok2 := barAt(bars, to)
	if !ok1 || !ok2 || start.Close <= 0 {
		return 0, false
	}
	if yield {
		return end.Close - start.Close, true
	}
	return (end.Close - start.Close) / start.Close * 100, true
}

// usual is the typical size of a gap over the past year of sessions: the
// root of its mean square, measured from each session back to the date
// from gives. Zero where there are too few sessions to say.
func usual(a, b []Bar, dates []time.Time, from func(d time.Time, i int) time.Time) float64 {
	start := max(1, len(dates)-usualSessions)
	var sum float64
	n := 0
	for i := start; i < len(dates); i++ {
		d := dates[i]
		ma, okA := move(a, from(d, i), d, false)
		mb, okB := move(b, from(d, i), d, false)
		if !okA || !okB {
			continue
		}
		gap := ma - mb
		sum += gap * gap
		n++
	}
	if n < leastSamples {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

// newYork is where a session's date is decided.
func newYork() *time.Location {
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		return loc
	}
	return time.FixedZone("EST", -5*60*60)
}
