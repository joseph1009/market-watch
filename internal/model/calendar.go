package model

import "time"

// Calendar is what is due: the economic releases and the companies'
// results the brief looks ahead to, so it can say what to watch before it
// happens rather than only report it afterwards.
type Calendar struct {
	Events   []Release `json:"events,omitempty"`
	Earnings []Results `json:"earnings,omitempty"`
}

// Empty reports whether nothing is due.
func (c Calendar) Empty() bool { return len(c.Events) == 0 && len(c.Earnings) == 0 }

// Release is one scheduled economic release or speech, as ForexFactory's
// calendar lists it.
type Release struct {
	At       time.Time `json:"at"`
	Country  string    `json:"country"` // the currency it moves: USD, EUR, JPY
	Title    string    `json:"title"`
	Impact   string    `json:"impact"` // High or Medium
	Forecast string    `json:"forecast,omitempty"`
	Previous string    `json:"previous,omitempty"`
}

// Results is one company due to report, as Nasdaq's earnings calendar lists
// it.
type Results struct {
	Day       time.Time `json:"day"`
	Symbol    string    `json:"symbol"`
	Name      string    `json:"name"`
	When      string    `json:"when,omitempty"` // "before the open", "after the close", or unknown
	MarketCap float64   `json:"market_cap,omitempty"`
	Forecast  string    `json:"eps_forecast,omitempty"` // consensus earnings per share
	LastYear  string    `json:"eps_last_year,omitempty"`
	Estimates int       `json:"estimates,omitempty"`
	Followed  bool      `json:"followed,omitempty"`
}
