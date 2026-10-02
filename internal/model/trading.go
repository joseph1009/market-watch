package model

import (
	"fmt"
	"math"
	"time"
)

// Trading is what a share has done over months, rather than what it did on one
// day.
//
// The accounts describe a company as it was on a balance sheet date. A single
// quote describes it at one instant. Neither says what the market has been
// paying for it, and that is a fact of its own: a price a fifth below its own
// hundred-day average is a different situation from the same price a fifth
// above, and the filings cannot tell them apart.
//
// Everything here is descriptive. A moving average says where the price has
// been, never where it is going, and nothing in this struct is a signal.
type Trading struct {
	Symbol   string
	Currency string

	// AsOf is the last session in the history, From the first, and Days the
	// number of sessions between them. A statistic computed from forty days is
	// not the same claim as one computed from five hundred, so the count
	// travels with the figures.
	AsOf time.Time
	From time.Time
	Days int

	Last float64

	// Partial says the last session was still running when this was read. It
	// matters most for volume: a part of a day set against whole-day averages
	// always looks like a quiet market, which may be the opposite of the truth.
	Partial bool

	// Returns are the moves over the stretches a reader thinks in, oldest
	// stretch last. A stretch the history cannot cover is left out rather than
	// computed from whatever the first bar happens to be.
	Returns []Return

	// MA50 and MA200 are the average closing price over the last fifty and two
	// hundred sessions. Zero where the history is too short to have one.
	MA50  float64
	MA200 float64

	High52 float64
	HighAt time.Time
	Low52  float64
	LowAt  time.Time

	// VolumeLast is the most recent session's volume, against the averages that
	// say whether it was a busy day. Volume is the part of a chart that says
	// how many people were involved in setting the price.
	VolumeLast  float64
	VolumeAvg30 float64
	VolumeAvg90 float64

	// VWAP is the average price actually paid, each day's close weighted by the
	// shares that changed hands. It answers "what has this been trading at"
	// better than an average of closes does, because a price nobody traded at
	// counts for as little as it deserves.
	VWAP30 float64
	VWAP90 float64

	// Volatility is the annualised standard deviation of daily moves, as a
	// percentage: the usual size of this share's swings, not their direction.
	Volatility float64

	// Market is the S&P 500's moves over the same stretches as Returns, where
	// they were read. A share up 30% in a year when the market rose 25% has
	// barely led it, and the share's own figures cannot say so.
	Market []Return
}

// MarketOver is the S&P 500's move over the named stretch, if it was read.
func (t Trading) MarketOver(over string) (Return, bool) {
	for _, r := range t.Market {
		if r.Over == over {
			return r, true
		}
	}
	return Return{}, false
}

// Return is a price move over a named stretch of time.
type Return struct {
	Over    string
	Percent float64

	// From is the close the move is measured from, on Since: the last session
	// at or before the start of the stretch, which after a weekend or holiday
	// is a day or two earlier than the calendar says.
	From  float64
	Since time.Time
}

// Sign reads a percentage as a person would say it.
func (r Return) Sign() string { return signed(r.Percent) }

// Against says where the last price sits relative to a level, as a percentage,
// and is empty when there is no level to compare with.
func (t Trading) Against(level float64) string {
	if level <= 0 || t.Last <= 0 {
		return ""
	}
	return signed((t.Last - level) / level * 100)
}

// Busy is the last session's volume as a multiple of the ninety-day average:
// how unusual a day it was. Zero where there is no honest comparison to make,
// which includes a session that has not finished.
func (t Trading) Busy() float64 {
	if t.Partial || t.VolumeAvg90 <= 0 || t.VolumeLast <= 0 {
		return 0
	}
	return t.VolumeLast / t.VolumeAvg90
}

// Stale reports whether the last session is old enough that these figures
// describe a market that has since moved on -- a holiday week, a delisting, or
// a fetch that quietly returned yesterday's file for a month.
func (t Trading) Stale(now time.Time) bool {
	return !t.AsOf.IsZero() && now.Sub(t.AsOf) > 7*24*time.Hour
}

func signed(pct float64) string {
	switch {
	case math.IsNaN(pct) || math.IsInf(pct, 0):
		return ""
	case pct >= 0.05:
		return fmt.Sprintf("+%.1f%%", pct)
	case pct <= -0.05:
		return fmt.Sprintf("%.1f%%", pct)
	default:
		return "flat"
	}
}
