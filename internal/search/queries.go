package search

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/internal/model"
)

// Query is one search. Label names it in logs and errors: a watchlist id, the
// name of one of the general searches, or "mover:" and a ticker.
type Query struct {
	Label string
	Text  string

	// Max is how many results to ask for. Zero asks for the most a search can
	// return, which costs the same.
	Max int
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

// groupQuery describes a sector to the search engine: its name and the first
// sentence of its description, which is written to name the sector's ground.
// The sentences after it -- which themes belong, what belongs elsewhere -- are
// for the sorting, and would only blur a search. Without a description it
// falls back to the sector's companies.
func groupQuery(g model.Group) string {
	name := strings.TrimSpace(g.Name)
	if name == "" {
		return ""
	}
	if first := firstSentence(g.About); first != "" {
		return clip(name + " news: " + first)
	}

	var terms []string
	for _, c := range g.Companies {
		terms = append(terms, c.Name)
		if len(terms) == 8 {
			break
		}
	}
	if len(terms) == 0 {
		return clip(name + " news")
	}
	return clip(name + " news: " + strings.Join(terms, ", "))
}

// firstSentence is the text up to the first full stop that ends a sentence.
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
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

// MaxMoverQueries bounds the searches for shares that moved sharply, which are
// run on top of MaxQueries: at most five more credits a brief, about 110 more a
// month on weekdays, 440 in all against the free 1,000.
const MaxMoverQueries = 5

// moverResults is what a mover search asks for. One company's day does not
// fill twenty results: in the McDonald's trial below, the results among the
// first ten that were not about McDonald's were the day's market round-ups,
// which the general searches bring already.
const moverResults = 10

// MoverQuery asks why one share moved. Percent says which way.
//
// The company's name goes in beside the ticker. Tried both ways on McDonald's
// the day it fell 4.8% after its investor day (2026-09-23), the question with
// the name returned eight McDonald's stories in ten, among them AP's and
// Axios's, which no other search had found; with the ticker alone, five.
func MoverQuery(name, ticker string, percent float64) Query {
	verb := "rise"
	if percent < 0 {
		verb = "fall"
	}
	subject := ticker
	if n := PlainName(name); n != "" && !strings.EqualFold(n, ticker) {
		subject = n + " (" + ticker + ")"
	}
	return Query{
		Label: "mover:" + ticker,
		Text:  fmt.Sprintf("Why did %s shares %s today?", subject, verb),
		Max:   moverResults,
	}
}

// corporate are the words a registered name carries that nobody uses when
// writing about the company.
var corporate = map[string]bool{
	"inc": true, "incorporated": true, "corp": true, "corporation": true,
	"co": true, "ltd": true, "limited": true, "plc": true, "llc": true, "lp": true,
	"nv": true, "sa": true, "ag": true, "se": true, "holding": true, "holdings": true,
	"&": true,
}

// PlainName turns the name the SEC files a company under -- "MCDONALDS CORP",
// "KKR & Co. Inc.", "Rivian Automotive, Inc. / DE" -- into the one a headline
// uses, by dropping the state suffix and the corporate words at the end.
func PlainName(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	words := strings.Fields(name)
	for len(words) > 1 {
		last := strings.ToLower(strings.Trim(words[len(words)-1], ".,"))
		if !corporate[last] {
			break
		}
		words = words[:len(words)-1]
	}
	return strings.TrimRight(strings.Join(words, " "), " ,.")
}
