package search

import (
	"strings"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/internal/model"
)

// Query is one search. Label names it in logs and errors: a watchlist id, or
// the name of one of the general searches.
type Query struct {
	Label string
	Text  string
}

// General are the searches run whatever the watchlists say, for the news the
// overview is written from: the market as a whole, the economy, and Asia,
// where the reader is.
var General = []Query{
	{Label: "markets", Text: "Stock market news today: US stocks, bonds, the dollar and commodities, and what moved them"},
	{Label: "economy", Text: "World economy news: central banks, inflation, growth, jobs and trade"},
	{Label: "asia", Text: "Asian markets and business news: China, Japan, South Korea, Taiwan, India and Southeast Asia"},
}

// MaxQueries bounds one round of searches, and so what one brief can cost.
//
// Each search is one credit, and the free plan is 1,000 a month. The defaults
// come to fifteen: three general searches and one per watchlist. At fifteen,
// weekday briefs use about 330 a month, which leaves room for reruns with
// /now. Twenty is where adding watchlists stops adding searches; the general
// searches go first, so what is dropped is the last watchlists added.
const MaxQueries = 20

// maxQueryRunes keeps a search short. Tavily asks for queries under 400
// characters, and a longer one describes the sector no better.
const maxQueryRunes = 380

// Queries builds the searches for a set of watchlists: the general ones, then
// one per watchlist.
//
// A watchlist is searched for by its sector sentence where it has one. Tried
// both ways on 2026-09-23, "Energy news: oil, gas, fuel and power..." returned
// sixteen energy stories in twenty, including TotalEnergies and Devon Energy,
// neither of them on the watchlist; "Energy: Exxon, Chevron, ConocoPhillips..."
// returned fourteen, padded with market round-ups. The list of drug makers did
// worse: on a day none of them made news, its top results were CNBC market
// round-ups, while the sector sentence found a Roche phase 3 result. A list of
// names finds the days those names are in the news; a description finds the
// sector, which is what the watchlist is for.
func Queries(groups []model.Group) []Query {
	out := append([]Query(nil), General...)
	for _, g := range groups {
		if len(out) >= MaxQueries {
			break
		}
		if text := groupQuery(g); text != "" {
			out = append(out, Query{Label: g.ID, Text: text})
		}
	}
	return out
}

// groupQuery describes a watchlist to the search engine. Without a sector
// sentence it falls back to the names and themes the watchlist lists.
func groupQuery(g model.Group) string {
	name := strings.TrimSpace(g.Name)
	if name == "" {
		return ""
	}
	if scope := strings.TrimSpace(g.Scope); scope != "" {
		return clip(name + " news: " + scope)
	}

	var terms []string
	for _, t := range append(append([]string{}, g.Names...), g.Keywords...) {
		if t = strings.TrimSpace(t); t != "" {
			terms = append(terms, t)
		}
		if len(terms) == 8 {
			break
		}
	}
	if len(terms) == 0 {
		return clip(name + " news")
	}
	return clip(name + " news: " + strings.Join(terms, ", "))
}

// clip cuts a query to maxQueryRunes on a word boundary.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxQueryRunes {
		return s
	}
	r := []rune(s)[:maxQueryRunes]
	out := string(r)
	if sp := strings.LastIndexByte(out, ' '); sp > 0 {
		out = out[:sp]
	}
	return strings.TrimRight(out, " ,;:")
}
