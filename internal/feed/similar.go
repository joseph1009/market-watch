package feed

import (
	"strings"
	"unicode"
)

// DefaultSimilarity is the fraction of shared words above which two headlines
// are treated as the same story. Tuned against real feeds: below this, genuinely
// different stories about one company start merging; above it, the same wire
// story rewritten by two desks stays split.
const DefaultSimilarity = 0.6

// minTitleTokens guards the short-headline problem. "Apple event today" and
// "Apple earnings today" share two of three words, which is a high score on a
// pair that is not the same story at all.
const minTitleTokens = 4

// stopWords are words that carry no topical meaning. The list is deliberately
// short: dropping "not", "says" or "denies" would merge a report with its own
// denial, which is the one pair that must never collapse.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"of": true, "to": true, "in": true, "on": true, "for": true, "at": true,
	"by": true, "from": true, "with": true, "as": true, "into": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"it": true, "its": true, "this": true, "that": true, "these": true,
	"has": true, "have": true, "had": true, "will": true, "would": true,
}

// outletSuffixes are the "- CNBC" tails feeds append to headlines. Left in,
// they are shared words that make unrelated stories from one outlet look alike.
var outletSuffixes = []string{" - ", " | ", " — ", " – "}

// titleTokens reduces a headline to the words worth comparing.
func titleTokens(title string) map[string]bool {
	cleaned := stripOutletSuffix(title)

	tokens := strings.FieldsFunc(strings.ToLower(cleaned), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	out := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		// Single characters survive tokenizing possessives and hyphenation and
		// carry no meaning of their own.
		if len(t) < 2 || stopWords[t] {
			continue
		}
		out[t] = true
	}
	return out
}

// stripOutletSuffix removes a trailing " - Outlet Name" if one is present. Only
// a short tail is removed: a dash in the middle of a headline is punctuation,
// not attribution.
func stripOutletSuffix(title string) string {
	for _, sep := range outletSuffixes {
		if i := strings.LastIndex(title, sep); i > 0 {
			tail := title[i+len(sep):]
			if len(strings.Fields(tail)) <= 4 {
				return title[:i]
			}
		}
	}
	return title
}

// similarity is the Jaccard overlap of two token sets: shared words over total
// distinct words. Chosen over anything cleverer because it is inspectable --
// when a merge looks wrong, the reason is two lists of words.
func similarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	shared := 0
	smaller, larger := a, b
	if len(b) < len(a) {
		smaller, larger = b, a
	}
	for token := range smaller {
		if larger[token] {
			shared++
		}
	}

	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// contradictionMarkers reverse a headline's meaning. "Iran says it struck two
// American vessels" and "US denies Iran struck two American vessels" share
// seven of ten words -- a 0.70 overlap, comfortably over any useful threshold --
// while carrying opposite claims. Word counting cannot separate those, so the
// denial is treated as a distinguishing fact rather than a shared one.
var contradictionMarkers = map[string]bool{
	"denies": true, "denied": true, "denial": true, "denying": true,
	"rejects": true, "rejected": true, "disputes": true, "disputed": true,
	"refutes": true, "refuted": true, "false": true, "unfounded": true,
	"cancels": true, "cancelled": true, "canceled": true, "halts": true,
	"halted": true, "delays": true, "delayed": true, "scraps": true,
	"withdraws": true, "withdrawn": true, "retracts": true, "retracted": true,
}

func contradicts(tokens map[string]bool) bool {
	for t := range tokens {
		if contradictionMarkers[t] {
			return true
		}
	}
	return false
}

// sameStory reports whether two headlines describe one story. Both must carry
// enough words to judge; a short headline is left alone rather than guessed at.
func sameStory(a, b map[string]bool, threshold float64) bool {
	if len(a) < minTitleTokens || len(b) < minTitleTokens {
		return false
	}
	// One headline reporting a claim and the other denying it are not the same
	// story, however much vocabulary they share.
	if contradicts(a) != contradicts(b) {
		return false
	}
	return similarity(a, b) >= threshold
}
