package feed

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/joseph1009/market-watch/internal/model"
)

// Result is one collection run: the articles worth summarizing plus whatever
// went wrong along the way. Errors are reported rather than returned so a
// partial run still produces a report, with the gaps visible.
type Result struct {
	Articles []model.Article
	Errors   []SourceError

	// Fetched is how many articles arrived before dedupe and the limit, which
	// is what makes the run's yield legible in logs.
	Fetched int
}

// AllFailed reports whether every source errored, the one case where sending a
// report would misrepresent an outage as a quiet news day.
func (r Result) AllFailed() bool {
	return len(r.Articles) == 0 && len(r.Errors) > 0
}

// Collect runs the full pipeline: fetch every source, collapse duplicates, tag
// articles against the watchlists, and keep the most useful max of them.
func Collect(ctx context.Context, f *Fetcher, sources []model.Source, groups []model.Group, max int) Result {
	articles, errs := f.Fetch(ctx, sources)

	fetched := len(articles)
	articles = Dedupe(articles, sources)
	articles = Match(articles, groups)
	articles = Limit(articles, max)

	return Result{Articles: articles, Errors: errs, Fetched: fetched}
}

// Dedupe collapses the same story arriving from several feeds down to one
// copy, keeping whichever source ranks highest. Ties go to the earliest
// publication, which is the closest thing available to the original report.
func Dedupe(articles []model.Article, sources []model.Source) []model.Article {
	weights := make(map[string]int, len(sources))
	for _, s := range sources {
		weights[s.ID] = s.Weight
	}

	index := make(map[string]int, len(articles)) // article ID -> position in out
	out := make([]model.Article, 0, len(articles))
	for _, a := range articles {
		pos, seen := index[a.ID]
		if !seen {
			index[a.ID] = len(out)
			out = append(out, a)
			continue
		}

		kept := out[pos]
		if better(a, kept, weights) {
			out[pos] = a
		}
	}
	return out
}

func better(candidate, kept model.Article, weights map[string]int) bool {
	cw, kw := weights[candidate.SourceID], weights[kept.SourceID]
	if cw != kw {
		return cw > kw
	}
	return candidate.Published.Before(kept.Published)
}

// Match tags every article with the watchlist groups it belongs to and the
// tickers it names. Articles matching nothing are kept: they are the raw
// material for the general market overview.
func Match(articles []model.Article, groups []model.Group) []model.Article {
	out := make([]model.Article, len(articles))
	for i, a := range articles {
		text := a.Title + " " + a.Summary
		lower := strings.ToLower(text)

		a.Tickers = nil
		a.GroupIDs = nil
		for _, g := range groups {
			matched := false
			for _, ticker := range g.Tickers {
				if mentionsTicker(text, ticker) {
					a.Tickers = appendUnique(a.Tickers, strings.ToUpper(strings.TrimSpace(ticker)))
					matched = true
				}
			}
			if !matched {
				for _, kw := range g.Keywords {
					if mentionsWord(lower, strings.ToLower(strings.TrimSpace(kw))) {
						matched = true
						break
					}
				}
			}
			if matched {
				a.GroupIDs = appendUnique(a.GroupIDs, g.ID)
			}
		}
		out[i] = a
	}
	return out
}

// mentionsTicker looks for a ticker as a standalone token, case-sensitively.
// Case matters: lowercasing would match "arm" and "it" in ordinary prose and
// tag half the feed into the wrong watchlist. This still trusts an all-caps
// headline, which is the residual false positive worth accepting.
func mentionsTicker(text, ticker string) bool {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return false
	}
	for i := 0; i+len(ticker) <= len(text); i++ {
		if text[i:i+len(ticker)] != ticker {
			continue
		}
		if i > 0 && isWordByte(text[i-1]) {
			continue // inside a longer token
		}
		if end := i + len(ticker); end < len(text) && isWordByte(text[end]) {
			continue
		}
		return true
	}
	return false
}

// mentionsWord matches a keyword on word boundaries so "CPI" does not fire on
// "recipient". Both arguments must already be lowercased.
func mentionsWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		leftOK := start == 0 || !isWordByte(haystack[start-1])
		rightOK := end == len(haystack) || !isWordByte(haystack[end])
		if leftOK && rightOK {
			return true
		}
		i = start + 1
	}
}

// isWordByte reports whether b continues a token. Multi-byte UTF-8 sequences
// count as word bytes, which keeps a match from starting mid-character.
func isWordByte(b byte) bool {
	if b >= utf8Continuation {
		return true
	}
	r := rune(b)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// utf8Continuation is the first byte value outside 7-bit ASCII.
const utf8Continuation = 0x80

// Limit keeps at most n articles. Watchlist matches are preferred over general
// news, and newer over older, so trimming a heavy news day costs the least
// relevant stories rather than a whole section of the report.
func Limit(articles []model.Article, n int) []model.Article {
	sorted := make([]model.Article, len(articles))
	copy(sorted, articles)

	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if ai, bi := len(a.GroupIDs) > 0, len(b.GroupIDs) > 0; ai != bi {
			return ai
		}
		if !a.Published.Equal(b.Published) {
			return a.Published.After(b.Published)
		}
		return a.ID < b.ID // stable across runs when timestamps tie
	})

	if n > 0 && len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
