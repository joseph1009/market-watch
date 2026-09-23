package model

import "time"

// Candidate is a company the day's news kept mentioning that no watchlist
// tracks.
//
// It is deliberately not called an opportunity. The brief has news and, one
// day, prices; it has no valuation, no fundamentals and no idea what the reader
// owns. What it can honestly say is "these names came up, this is what
// happened, here is where it was reported" -- and leave the judgment where it
// belongs.
type Candidate struct {
	// Name is the company as the news wrote it, Listed as the exchange
	// registered it. Both are kept: the first is what the reader will
	// recognize, the second is what proves the ticker is real.
	Name   string `json:"name"`
	Listed string `json:"listed,omitempty"`

	Ticker   string `json:"ticker,omitempty"`
	Exchange string `json:"exchange,omitempty"`

	// Private marks a company with no listing to buy -- OpenAI, Anthropic,
	// DeepSeek. They dominate the feeds and cannot be acted on, so they are
	// named and labelled rather than quietly dropped.
	Private bool `json:"private,omitempty"`

	// Why is the catalyst in one line: what happened, not what to do.
	Why string `json:"why"`

	// Articles are the stories behind it, in citation order.
	Articles []Article `json:"-"`

	// Rating is the best triage rating among those articles, and Sources how
	// many distinct outlets carried it. Together they are the evidence bar.
	Rating  int `json:"rating,omitempty"`
	Sources int `json:"sources,omitempty"`

	// Quote is what the share did on the session the brief covers: from the
	// quote feed for a US listing, from the last two closes of the daily
	// history for one anywhere else. Nil where neither source knows the name,
	// and the line then carries no move rather than a blank.
	Quote *Quote `json:"quote,omitempty"`

	// FirstSeen is when this name first appeared as a candidate, and Days how
	// many briefs have now carried it. A name on its third day is a developing
	// story; a name on its first is an event.
	FirstSeen time.Time `json:"first_seen"`
	Days      int       `json:"days"`
}

// Symbol is how the candidate is written for a reader: "700.HK", "NVDA".
func (c Candidate) Symbol() string {
	switch {
	case c.Ticker == "":
		return ""
	case c.Exchange == "" || c.Exchange == "US":
		return c.Ticker
	default:
		return c.Ticker + "." + c.Exchange
	}
}
