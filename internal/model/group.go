package model

import "strings"

// Group is one section of the brief: a sector the reader follows, what it
// covers in plain words, and the companies in it.
//
// The sectors are described in config/sectors.yaml and the companies listed in
// config/companies.yaml; a Group is the two put together, with any watchlist
// edits made from Telegram applied on top.
type Group struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`

	// About says in a few sentences what the sector covers. It is what the
	// sorting and the review judge an article against, and its first sentence
	// is what the news search asks for, so that sentence names the sector's
	// ground and the ones after it add the detail: the themes that belong
	// here, and what belongs in another section instead.
	//
	// A description rather than a list of words to match. A keyword is
	// precise about the word and blind to the meaning: "recall" filed food
	// recalls under Autos, "Target" every price target under Consumer, and a
	// keyword match was a placement the sorting could not undo.
	About string `yaml:"about" json:"about,omitempty"`

	// Companies are the ones followed in this sector.
	Companies []Company `yaml:"-" json:"companies,omitempty"`
}

// Company is one company the reader follows.
type Company struct {
	// Symbol is its US ticker. Empty for a company followed without one, such
	// as a private company or one listed only abroad: it is still matched by
	// name, and has no price.
	Symbol string `yaml:"symbol,omitempty" json:"symbol,omitempty"`

	// Name is how a headline writes it ("Nvidia", not "NVIDIA Corporation"),
	// which is also the name its searches use. Also lists other names it goes
	// by. Both are matched case-blind, on word boundaries.
	Name string   `yaml:"name" json:"name"`
	Also []string `yaml:"also,omitempty" json:"also,omitempty"`

	// Match limits how the company is found in the news: MatchTicker for a
	// name that is an ordinary word ("Target", "Arm"), MatchName for a symbol
	// that is one ("MS"). Empty matches both.
	Match string `yaml:"match,omitempty" json:"match,omitempty"`
}

// The values Company.Match may take.
const (
	MatchTicker = "ticker"
	MatchName   = "name"
)

// Names is every name the company goes by, its main one first.
func (c Company) Names() []string {
	out := make([]string, 0, 1+len(c.Also))
	if c.Name != "" {
		out = append(out, c.Name)
	}
	return append(out, c.Also...)
}

// Is reports whether a term names this company: its symbol, or any of its
// names, ignoring case.
func (c Company) Is(term string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return false
	}
	if c.Symbol != "" && strings.EqualFold(c.Symbol, term) {
		return true
	}
	for _, n := range c.Names() {
		if strings.EqualFold(n, term) {
			return true
		}
	}
	return false
}

// Symbols is every ticker in the sector, whatever its matching: the list
// prices, filings and the moves line are read for.
func (g Group) Symbols() []string {
	var out []string
	for _, c := range g.Companies {
		if c.Symbol != "" {
			out = append(out, c.Symbol)
		}
	}
	return out
}

// MatchSymbols is the tickers to look for in an article's text.
func (g Group) MatchSymbols() []string {
	var out []string
	for _, c := range g.Companies {
		if c.Symbol != "" && c.Match != MatchName {
			out = append(out, c.Symbol)
		}
	}
	return out
}

// MatchNames is the company names to look for in an article's text.
func (g Group) MatchNames() []string {
	var out []string
	for _, c := range g.Companies {
		if c.Match != MatchTicker {
			out = append(out, c.Names()...)
		}
	}
	return out
}

// Has reports whether the sector follows a company by that symbol or name.
func (g Group) Has(term string) bool {
	for _, c := range g.Companies {
		if c.Is(term) {
			return true
		}
	}
	return false
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
