package market

import (
	"strings"
	"unicode"

	"github.com/joseph1009/market-watch/internal/prices"
)

// PlainName is a listed name as a headline would write it: Nasdaq's
// "Agilent Technologies Inc. Common Stock" is "Agilent Technologies".
func PlainName(listed string) string {
	name := listed
	// Everything from the security's description on is not the company.
	for _, cut := range []string{
		" Common Stock", " Common Shares", " Ordinary Shares", " Class A", " Class B", " Class C",
		" American Depositary", " Depositary Shares", " Subordinate Voting", " Shares of Beneficial",
	} {
		if i := strings.Index(name, cut); i > 0 {
			name = name[:i]
		}
	}
	fields := strings.Fields(strings.NewReplacer(",", " ", "(", " ", ")", " ").Replace(name))
	for len(fields) > 1 {
		last := strings.ToLower(strings.Trim(fields[len(fields)-1], "."))
		if !legalSuffix[last] {
			break
		}
		fields = fields[:len(fields)-1]
	}
	return strings.Join(fields, " ")
}

// legalSuffix are the words at the end of a registered name that no headline
// writes.
var legalSuffix = map[string]bool{
	"inc": true, "incorporated": true, "corp": true, "corporation": true, "co": true, "company": true,
	"ltd": true, "limited": true, "plc": true, "llc": true, "lp": true, "l.p": true, "sa": true, "s.a": true,
	"nv": true, "n.v": true, "ag": true, "se": true, "holdings": true, "holding": true, "group": true,
	"the": true, "&": true,
}

// genericWords are words too common to stand for a company alone: "First"
// is not First Solar, nor "American" American Express; and the market's own
// vocabulary, which a headline uses for itself: "Trump" is rarely Trump
// Media, "stock" never Stock Yards, "people" never People Inc.
var genericWords = map[string]bool{
	"stock": true, "stocks": true, "market": true, "markets": true, "trump": true, "people": true,
	"target": true, "block": true, "match": true, "strategy": true, "data": true, "bitcoin": true,
	"crypto": true, "world": true, "china": true, "chinese": true, "japan": true, "india": true,
	"asia": true, "europe": true, "dollar": true, "oil": true, "wall": true, "street": true, "tech": true,
	"technology": true, "chip": true, "chips": true, "trade": true, "high": true, "big": true, "u.s.": true,
	"news": true, "post": true, "dow": true, "bill": true, "sea": true, "slide": true, "state": true,
	"group": true, "medical": true, "credit": true, "advance": true, "expand": true, "agree": true,
	"independent": true, "reliance": true, "next": true, "here": true, "change": true, "focus": true,
	"work": true, "long": true, "house": true, "safety": true, "business": true, "rising": true,
	"american": true, "first": true, "general": true, "united": true, "national": true, "international": true,
	"global": true, "new": true, "western": true, "eastern": true, "southern": true, "northern": true,
	"pacific": true, "atlantic": true, "energy": true, "capital": true, "financial": true, "health": true,
	"healthcare": true, "digital": true, "advanced": true, "applied": true, "universal": true, "royal": true,
	"public": true, "great": true, "texas": true, "allied": true, "standard": true, "home": true, "old": true,
	"enterprise": true, "main": true, "federal": true, "consolidated": true, "air": true, "bank": true,
	"trust": true, "power": true, "real": true, "north": true, "south": true, "west": true, "east": true,
	"central": true, "community": true, "select": true, "summit": true, "the": true, "life": true,
	"green": true, "blue": true, "red": true, "black": true, "silver": true, "gold": true,
	"regional": true, "premier": true, "prime": true, "core": true, "smart": true, "open": true, "best": true,
}

// Mentions counts, by symbol, the headlines that name each listing: by its
// plain name, and by its first word alone where that is distinctive enough to
// stand for it ("Nvidia", "Agilent"). A first word is not where another
// company's name has it too ("China", "U.S.", the Stanley of Morgan
// Stanley), or is a word headlines use for other things; nor is a one-word
// name that is such a word ("People").
// Missing a company's mention costs an industry a little; counting every
// headline about the president for Trump Media makes one popular.
func Mentions(titles []string, listings []Listing) map[string]int {
	lowered := make([]string, len(titles))
	for i, t := range titles {
		lowered[i] = strings.ToLower(t)
	}
	namedIn := map[string]map[string]bool{} // word: the names that have it
	for _, l := range listings {
		if !prices.CommonShare(l.Symbol) {
			continue
		}
		plain := strings.ToLower(PlainName(l.Name))
		for _, word := range strings.Fields(plain) {
			if namedIn[word] == nil {
				namedIn[word] = map[string]bool{}
			}
			namedIn[word][plain] = true
		}
	}
	out := map[string]int{}
	for _, l := range listings {
		plain := strings.ToLower(PlainName(l.Name))
		if len([]rune(plain)) < 3 || genericWords[plain] {
			continue
		}
		terms := []string{plain}
		first, _, more := strings.Cut(plain, " ")
		if more && len([]rune(first)) >= 4 && !genericWords[first] && len(namedIn[first]) == 1 {
			terms = append(terms, first)
		}
		for _, t := range lowered {
			for _, term := range terms {
				if containsWord(t, term) {
					out[l.Symbol]++
					break
				}
			}
		}
	}
	return out
}

// containsWord reports whether term appears in text standing alone: not
// "arm" in "pharmacy".
func containsWord(text, term string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], term)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(term)
		before := start == 0 || !isWordRune(lastRune(text[:start]))
		after := end == len(text) || !isWordRune(firstRune(text[end:]))
		if before && after {
			return true
		}
		from = start + 1
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0
	}
	return rs[len(rs)-1]
}
