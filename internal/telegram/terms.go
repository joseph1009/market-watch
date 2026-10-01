package telegram

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/internal/model"
)

// linkTerms links the first mention of each glossary term in a section to the
// page that explains it. The writer still says what a term means in plain
// words; the link is for the reader who wants more. Once a section, so a
// section reads as prose with a few links in it rather than a page of them.
//
// Only plain text is touched: nothing inside a tag, a link (the citations
// are links) or bold text (the sub-headings and the highlighted figures).
func linkTerms(blocks []string, terms []model.Term) []string {
	if len(terms) == 0 {
		return blocks
	}
	done := map[int]bool{}
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = linkBlock(b, terms, done)
	}
	return out
}

type span struct{ start, end int }

// freeText is where a block's plain text lies: outside tags, links and bold.
func freeText(s string) []span {
	var spans []span
	protected, start := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		if protected == 0 && i > start {
			spans = append(spans, span{start, i})
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			return spans
		}
		tag := strings.ToLower(s[i+1 : i+end])
		switch {
		case tag == "b" || tag == "a" || strings.HasPrefix(tag, "a "):
			protected++
		case tag == "/b" || tag == "/a":
			if protected > 0 {
				protected--
			}
		}
		i += end
		start = i + 1
	}
	if protected == 0 && start < len(s) {
		spans = append(spans, span{start, len(s)})
	}
	return spans
}

func linkBlock(s string, terms []model.Term, done map[int]bool) string {
	spans := freeText(s)
	if len(spans) == 0 {
		return s
	}
	lower := asciiLower(s)

	type hit struct{ at, length, term int }
	var hits []hit
	for ti, t := range terms {
		if done[ti] {
			continue
		}
		best := hit{at: -1}
		for _, w := range t.Words {
			at := findWord(s, lower, w, spans)
			if at >= 0 && (best.at < 0 || at < best.at || (at == best.at && len(w) > best.length)) {
				best = hit{at, len(w), ti}
			}
		}
		if best.at >= 0 {
			hits = append(hits, best)
		}
	}
	// Earliest first; where two terms claim overlapping text, the first
	// keeps it and the other waits for a later mention.
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var keep []hit
	last := -1
	for _, h := range hits {
		if h.at < last {
			continue
		}
		keep = append(keep, h)
		last = h.at + h.length
	}
	for i := len(keep) - 1; i >= 0; i-- {
		h := keep[i]
		word := s[h.at : h.at+h.length]
		s = s[:h.at] + `<a href="` + escape(terms[h.term].URL) + `">` + word + "</a>" + s[h.at+h.length:]
		done[h.term] = true
	}
	return s
}

// findWord finds w standing alone in the free text. A word written in
// capitals matches only in capitals; any other, whatever its case.
func findWord(s, lower, w string, spans []span) int {
	exact := w == strings.ToUpper(w) && strings.ToLower(w) != w
	hay, needle := lower, asciiLower(escape(w))
	if exact {
		hay, needle = s, escape(w)
	}
	for _, sp := range spans {
		from := sp.start
		for from < sp.end {
			i := strings.Index(hay[from:sp.end], needle)
			if i < 0 {
				break
			}
			at := from + i
			end := at + len(needle)
			if !wordRuneBefore(hay[:at]) && !wordRuneAfter(hay[end:]) {
				return at
			}
			from = at + 1
		}
	}
	return -1
}

func wordRuneBefore(s string) bool {
	r, size := utf8.DecodeLastRuneInString(s)
	return size > 0 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func wordRuneAfter(s string) bool {
	r, size := utf8.DecodeRuneInString(s)
	return size > 0 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// asciiLower lowers A to Z only, so every byte keeps its place.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
