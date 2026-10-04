// Package terms links the jargon in the brief and the analysis to what it
// means, and learns the terms the glossary does not hold yet.
//
// The writers do not define terms (the owner's choice, 2026-10-04: the
// definitions crowded out the substance and spelled out words the owner
// already knew).
// Each lists the terms it used instead, with a few words of context. A term
// config/glossary.yaml holds links to the page checked for it there; any
// other links to a Google search for its meaning, from its first appearance.
//
// A term that keeps coming up is learned: seen in two reports, and passed by
// a check that it is a real term with a clear search, it is linked wherever it
// appears from then on, even where the writer forgot to list it. The learned
// terms live on the data volume, and `market-watch --fold` writes them into
// config/glossary.yaml, as it does the watchlist's edits.
package terms

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/joseph1009/market-watch/internal/model"
)

// MaxListed is how many terms of one report are linked. A list longer than
// this is more likely padding than a reader's real questions.
const MaxListed = 25

// everyday are words a writer might list that no reader needs explained, or
// that are too broad to search for a meaning: they are never linked.
var everyday = map[string]bool{
	"revenue": true, "revenues": true, "sales": true, "profit": true, "profits": true,
	"loss": true, "losses": true, "debt": true, "cash": true, "earnings": true,
	"shares": true, "share": true, "stock": true, "stocks": true, "price": true,
	"prices": true, "market": true, "markets": true, "growth": true, "costs": true,
	"ai": true, "artificial intelligence": true, "investors": true, "analysts": true,
	"the fed": true, "fed": true, "rally": true, "sell-off": true, "selloff": true,
}

// Filter drops what should not be linked from a writer's list: everyday words,
// the names in exclude (the company itself, its ticker, its rivals: a name is
// not jargon), anything that is mostly figures, and whatever comes after the
// first MaxListed.
func Filter(listed []model.ListedTerm, exclude []string) []model.ListedTerm {
	skip := map[string]bool{}
	for _, name := range exclude {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			skip[name] = true
		}
	}
	var out []model.ListedTerm
	for _, l := range listed {
		key := strings.ToLower(l.Term)
		if everyday[key] || skip[key] || mostlyFigures(l.Term) {
			continue
		}
		out = append(out, l)
		if len(out) == MaxListed {
			break
		}
	}
	return out
}

func mostlyFigures(s string) bool {
	digits, letters := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			letters++
		}
	}
	return letters < 2 || digits > letters
}

// SearchURL is a Google search for what a term means, its context added so
// that an abbreviation finds the right meaning: "HBM memory chips meaning".
func SearchURL(l model.ListedTerm) string {
	q := l.Term
	if l.Context != "" {
		q += " " + l.Context
	}
	return "https://www.google.com/search?q=" + url.QueryEscape(q+" meaning")
}

// Linked is what a report's terms link to: the known terms first (the
// glossary's checked pages, then the learned ones), then a search for each
// listed term neither holds.
func Linked(known []model.Term, listed []model.ListedTerm) []model.Term {
	have := map[string]bool{}
	for _, t := range known {
		for _, w := range t.Words {
			have[strings.ToLower(w)] = true
		}
	}
	out := append([]model.Term{}, known...)
	for _, l := range listed {
		if key := strings.ToLower(l.Term); !have[key] {
			have[key] = true
			out = append(out, model.Term{URL: SearchURL(l), Words: []string{l.Term}})
		}
	}
	return out
}
