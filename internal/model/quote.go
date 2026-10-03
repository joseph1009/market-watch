package model

import (
	"fmt"
	"strings"
	"time"
)

// Quote is what a share last traded at, and how far that is from where it
// closed before.
//
// The brief is written the morning after a US session, so the move a reader
// wants is the one the news produced: the change on the day, not the level.
// Both are kept, because a level without a move says nothing and a move without
// a level cannot be checked.
type Quote struct {
	Symbol   string  `json:"symbol"`
	Price    float64 `json:"price"`
	Previous float64 `json:"previous"`
	Change   float64 `json:"change"`
	Percent  float64 `json:"percent"`
	High     float64 `json:"high,omitempty"`
	Low      float64 `json:"low,omitempty"`

	// Currency is what the price is denominated in. Empty means US dollars,
	// which is what the US quote feed returns and never says. A Hong Kong or
	// Tokyo listing is priced in its own currency, and a level printed without
	// one invites the reader to take 512.40 for dollars.
	Currency string `json:"currency,omitempty"`

	AsOf time.Time `json:"as_of"`
}

// Unit is the currency to print beside the price.
func (q Quote) Unit() string {
	if q.Currency == "" {
		return "USD"
	}
	return q.Currency
}

// Move reads as a person would say it: "+1.4%", "-0.6%", or "flat" for a move
// that rounds to nothing. A sign on a zero reads as a direction that was not
// there.
func (q Quote) Move() string {
	switch {
	case q.Percent >= 0.05:
		return fmt.Sprintf("+%.1f%%", q.Percent)
	case q.Percent <= -0.05:
		return fmt.Sprintf("%.1f%%", q.Percent)
	default:
		return "flat"
	}
}

// Moves lists shares by their move, in the order given: "MCD -4.8% · NKE -2.1%".
func Moves(quotes []Quote) string {
	parts := make([]string, len(quotes))
	for i, q := range quotes {
		parts[i] = q.Symbol + " " + q.Move()
	}
	return strings.Join(parts, " · ")
}

// MarketMove is a share anywhere in the US market that moved far beyond its
// usual on the last session, followed or not: what the brief's line under the
// overview shows. The program picks them from the market's history, as the
// closer look's reactions do.
type MarketMove struct {
	Quote
	Name string `json:"name"`

	// Times is the move against the share's usual daily move, and Busy the
	// value traded against its usual session's.
	Times float64 `json:"times"`
	Busy  float64 `json:"busy"`
}

// MarketQuotes are the moves as quotes, for the line that shows them.
func MarketQuotes(moves []MarketMove) []Quote {
	out := make([]Quote, len(moves))
	for i, m := range moves {
		out[i] = m.Quote
	}
	return out
}
