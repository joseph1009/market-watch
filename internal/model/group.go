package model

import "strings"

// Group is a watchlist: a named bucket of tickers and keywords that articles
// are matched against to build one section of the daily report.
type Group struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`

	// Tickers are matched case-sensitively as standalone symbols; Names are the
	// same companies as a headline actually writes them ("Nvidia", not "NVDA"),
	// matched case-insensitively. Both are needed: financial coverage uses the
	// name in prose and the symbol in quotes, and matching only symbols misses
	// most of the stories about a company.
	Tickers  []string `yaml:"tickers,omitempty" json:"tickers,omitempty"`
	Names    []string `yaml:"names,omitempty" json:"names,omitempty"`
	Keywords []string `yaml:"keywords,omitempty" json:"keywords,omitempty"`

	// Scope says in a sentence what the sector covers, for the model that
	// places articles by substance. Tickers and keywords describe a watchlist
	// by example, which is precise about the companies named and silent about
	// everything else: a story about a rival nobody listed, or a supplier two
	// steps back, matched nothing and read as belonging nowhere. A sentence
	// describes the sector itself, so the judgment has something to judge
	// against. Empty falls back to examples drawn from Names and Keywords.
	Scope string `yaml:"scope,omitempty" json:"scope,omitempty"`
}

// GroupID normalizes a display name into an ID usable as a map key and in bot
// commands: lowercase, non-alphanumerics collapsed to single hyphens.
func GroupID(name string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are suppressed
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}
