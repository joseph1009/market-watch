package fundamentals

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/joseph1009/market-watch/internal/model"
)

// Recent news is the half of the question the accounts cannot answer.
//
// A filing describes a period that has already closed, months after it closed.
// Whether a company is worth owning turns at least as much on what has happened
// since -- a contract won, a product delayed, a regulator's letter, a rival's
// price cut -- and none of that reaches EDGAR until the next quarter, if ever.
//
// Two rules keep it honest. What the press reports is a claim, not a filed
// fact, and the analysis is told to attribute rather than assert it. And a
// headline is not evidence of relevance: the vendor tags loosely, so a piece
// that never names this company is dropped rather than shown to the reader
// under its ticker.
const (
	// maxHeadlines is what a reader will actually read, and enough for the
	// model to see a pattern rather than a single story.
	maxHeadlines = 12

	// minNameWord is the shortest word from a company name worth matching on.
	// Below this the matches are prepositions.
	minNameWord = 3

	// maxPerDay is how much of the list one day may take. Without it a busy
	// session fills every slot: ten headlines from this morning, and nothing
	// from the fortnight in which the thing they are reacting to happened.
	maxPerDay = 3
)

// Headlines is the part of the news client this needs.
type Headlines interface {
	Company(ctx context.Context, symbol string, now time.Time) ([]model.Article, error)
}

// AddNews fills in what has been written about the company lately.
//
// Best-effort, like the business description: the accounts are the part that
// cannot be had anywhere else, and a news outage must not cost them.
func AddNews(ctx context.Context, h Headlines, snap *Snapshot, now time.Time) error {
	if h == nil || snap == nil {
		return nil
	}
	articles, err := h.Company(ctx, snap.Ticker, now)
	if err != nil {
		return err
	}
	snap.News = Relevant(articles, snap.Ticker, snap.Company, maxHeadlines)
	return nil
}

// SetNews keeps, of articles gathered from anywhere -- the news feed, a
// search -- the ones actually about the company, as AddNews does.
func SetNews(snap *Snapshot, articles []model.Article) {
	snap.News = Relevant(articles, snap.Ticker, snap.Company, maxHeadlines)
}

// Relevant keeps the articles that are actually about this company, newest
// first, with near-duplicates collapsed and no single day taking the whole
// list.
//
// Aggregated feeds repeat the same story under a dozen bylines, and a list of
// ten headlines that are one headline tells the reader nothing while looking
// like it tells them a lot. The day cap is the same problem in the other
// direction: the most recent morning always has enough copy to fill the
// section on its own, and what a reader needs is the fortnight.
func Relevant(articles []model.Article, ticker, company string, limit int) []model.Article {
	words := nameWords(company)

	var candidates []model.Article
	seen := map[string]bool{}
	for _, a := range articles {
		if !about(a, ticker, words) {
			continue
		}
		key := normalise(a.Title)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, a)
	}

	var out, overflow []model.Article
	perDay := map[string]int{}
	for _, a := range candidates {
		day := a.Published.Format(time.DateOnly)
		if perDay[day] >= maxPerDay {
			overflow = append(overflow, a)
			continue
		}
		perDay[day]++
		out = append(out, a)
		if len(out) == limit {
			return out
		}
	}

	// A quiet fortnight may not fill the list at three a day. Rather than
	// return four headlines when twelve were available, the ones held back are
	// put back in order.
	for _, a := range overflow {
		if len(out) == limit {
			break
		}
		out = append(out, a)
	}
	sortByDate(out)
	return out
}

// sortByDate puts the list back in reading order, newest first, after the
// overflow has been folded in.
func sortByDate(articles []model.Article) {
	sort.SliceStable(articles, func(i, j int) bool {
		return articles[i].Published.After(articles[j].Published)
	})
}

// about reports whether an article names this company, by ticker or by name.
//
// The ticker must appear in capitals and standing alone: "MU" in a headline is
// Micron, but "mu" inside a word is nothing, and a lower-cased match would make
// tickers like IT or ALL match half the feed.
func about(a model.Article, ticker string, words []string) bool {
	text := a.Title + " " + a.Summary
	if ticker != "" && hasToken(text, ticker) {
		return true
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// hasToken reports whether a symbol appears as a word of its own, so that NVDA
// matches "NVDA earnings" and "(NVDA)" but not "NVDAX".
func hasToken(text, symbol string) bool {
	for at := 0; ; {
		i := strings.Index(text[at:], symbol)
		if i < 0 {
			return false
		}
		i += at
		before := i == 0 || !isWordRune(rune(text[i-1]))
		end := i + len(symbol)
		after := end == len(text) || !isWordRune(rune(text[end]))
		if before && after {
			return true
		}
		at = i + 1
	}
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// nameWords are the distinctive words of a company name, lower-cased: the ones
// worth searching a headline for once the legal furniture is gone.
func nameWords(company string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(company)) {
		w = strings.Trim(w, ".,()-/&")
		if len([]rune(w)) < minNameWord || corporateWords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// corporateWords say that a company is a company, and so say nothing about
// which one. Matching a headline on "holdings" would return the whole market.
var corporateWords = map[string]bool{
	"inc": true, "incorporated": true, "corp": true, "corporation": true,
	"ltd": true, "limited": true, "plc": true, "llc": true, "company": true,
	"holdings": true, "holding": true, "group": true, "the": true, "and": true,
	"class": true, "adr": true, "ads": true, "common": true, "shares": true,
	"international": true, "technologies": true, "technology": true,
	"industries": true, "systems": true, "solutions": true, "enterprises": true,
}

// normalise reduces a headline to what it says, so two wordings of the same
// story collapse together.
func normalise(title string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}
