package market

import (
	"math"
	"sort"
	"time"
)

// Panel is the sessions loaded from the store: every symbol's bars lined up
// on the same dates, oldest first, with a zero where a symbol did not trade.
type Panel struct {
	Dates  []time.Time
	series map[string]*Series
}

// Series is one symbol's bars, by the panel's dates.
type Series struct {
	Symbol              string
	Open, Close, Volume []float64
}

// SeriesFrom builds a series from bars read elsewhere, oldest first: a
// Singapore share's, from the chart source, measured the same way as the US
// market's.
func SeriesFrom(symbol string, bars []Bar) *Series {
	s := &Series{Symbol: symbol}
	for _, b := range bars {
		s.Open = append(s.Open, b.Open)
		s.Close = append(s.Close, b.Close)
		s.Volume = append(s.Volume, b.Volume)
	}
	return s
}

func (s *Series) grow() {
	s.Open = append(s.Open, 0)
	s.Close = append(s.Close, 0)
	s.Volume = append(s.Volume, 0)
}

// Get is a symbol's series, or nil.
func (p *Panel) Get(symbol string) *Series {
	if p == nil {
		return nil
	}
	return p.series[symbol]
}

// Symbols lists what the panel holds, in order.
func (p *Panel) Symbols() []string {
	out := make([]string, 0, len(p.series))
	for sym := range p.series {
		out = append(out, sym)
	}
	sort.Strings(out)
	return out
}

// Last is the panel's latest session, or the zero time.
func (p *Panel) Last() time.Time {
	if p == nil || len(p.Dates) == 0 {
		return time.Time{}
	}
	return p.Dates[len(p.Dates)-1]
}

// Sessions in the stretches a return is measured over.
const (
	Week      = 5
	Month     = 21
	Quarter   = 63
	HalfYear  = 126
	Year      = 252
	TwoYears  = 504
	usualDays = 60
)

// last is the index of the latest session, where the series traded on it;
// otherwise -1. A share that did not trade on the market's last session is
// halted or gone, and its old price is no measure of where it stands.
func (s *Series) last() int {
	i := len(s.Close) - 1
	if i < 0 || s.Close[i] <= 0 {
		return -1
	}
	return i
}

// closeAt is the close n sessions before the latest, or the nearest one
// before it within a week where that session did not trade.
func (s *Series) closeAt(n int) float64 {
	i := s.last() - n
	for back := 0; back < Week && i-back >= 0; back++ {
		if c := s.Close[i-back]; c > 0 {
			return c
		}
	}
	return 0
}

// History is how many sessions back the series goes, counting from its
// first close.
func (s *Series) History() int {
	for i, c := range s.Close {
		if c > 0 {
			return len(s.Close) - i
		}
	}
	return 0
}

// Price is the latest close, or zero.
func (s *Series) Price() float64 {
	if i := s.last(); i >= 0 {
		return s.Close[i]
	}
	return 0
}

// Return is the move over n sessions, as a fraction, or NaN where the
// history is short.
func (s *Series) Return(n int) float64 { return s.ReturnBetween(n, 0) }

// ReturnBetween is the move from n sessions back to m sessions back.
func (s *Series) ReturnBetween(n, m int) float64 {
	if s.last() < 0 || s.History() <= n {
		return math.NaN()
	}
	from, to := s.closeAt(n), s.closeAt(m)
	if from <= 0 || to <= 0 {
		return math.NaN()
	}
	return to/from - 1
}

// Average is the mean close over the n sessions ending m sessions back, or
// zero where fewer than nine in ten of them traded.
func (s *Series) Average(n, m int) float64 {
	end := s.last() - m
	if end < 0 || end-n+1 < 0 {
		return 0
	}
	sum, count := 0.0, 0
	for i := end - n + 1; i <= end; i++ {
		if c := s.Close[i]; c > 0 {
			sum += c
			count++
		}
	}
	if count*10 < n*9 {
		return 0
	}
	return sum / float64(count)
}

// dailyReturns are the log moves from one session to the next over the n
// sessions ending m back, skipping days either side of which did not trade.
func (s *Series) dailyReturns(n, m int) []float64 {
	end := s.last() - m
	if end < 1 {
		return nil
	}
	var out []float64
	for i := max(1, end-n+1); i <= end; i++ {
		if a, b := s.Close[i-1], s.Close[i]; a > 0 && b > 0 {
			out = append(out, math.Log(b/a))
		}
	}
	return out
}

// Volatility is the annualised standard deviation of the daily moves over
// the last n sessions, as a fraction.
func (s *Series) Volatility(n int) float64 {
	r := s.dailyReturns(n, 0)
	if len(r) < 10 {
		return 0
	}
	mean := 0.0
	for _, v := range r {
		mean += v
	}
	mean /= float64(len(r))
	variance := 0.0
	for _, v := range r {
		variance += (v - mean) * (v - mean)
	}
	return math.Sqrt(variance/float64(len(r)-1)) * math.Sqrt(Year)
}

// Usual is the share's usual daily move, as a fraction: the mean size of its
// daily moves over the sixty sessions before the latest, either way. Zero
// where fewer than forty of them traded.
func (s *Series) Usual() float64 {
	r := s.dailyReturns(usualDays, 1)
	if len(r) < 40 {
		return 0
	}
	sum := 0.0
	for _, v := range r {
		sum += math.Abs(v)
	}
	return sum / float64(len(r))
}

// BiggestDay is the largest single-day log move up over the last n sessions.
func (s *Series) BiggestDay(n int) float64 {
	top := 0.0
	for _, v := range s.dailyReturns(n, 0) {
		top = max(top, v)
	}
	return top
}

// DollarVolume is the average value traded a session over the n sessions
// ending m back, or zero where none traded.
func (s *Series) DollarVolume(n, m int) float64 {
	end := s.last() - m
	if end < 0 {
		return 0
	}
	sum, count := 0.0, 0
	for i := max(0, end-n+1); i <= end; i++ {
		if c := s.Close[i]; c > 0 {
			sum += c * s.Volume[i]
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// Last is the latest session's bar, and whether the share traded on it.
func (s *Series) Last() (Bar, bool) {
	i := s.last()
	if i < 0 {
		return Bar{}, false
	}
	return Bar{Open: s.Open[i], Close: s.Close[i], Volume: s.Volume[i]}, true
}
