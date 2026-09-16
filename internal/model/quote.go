package model

import (
	"fmt"
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
	Symbol   string    `json:"symbol"`
	Price    float64   `json:"price"`
	Previous float64   `json:"previous"`
	Change   float64   `json:"change"`
	Percent  float64   `json:"percent"`
	High     float64   `json:"high,omitempty"`
	Low      float64   `json:"low,omitempty"`
	AsOf     time.Time `json:"as_of"`
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
