package search

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Restricting the searches to named outlets keeps out social media, but not
// the outlets' own pages that are not reports: a section front, a stock-quote
// page, a live blog, the recording of a whole programme. In the first brief
// written with search (2026-09-24), four of the 41 stories only search had
// found were one of these -- a DigiTimes section page, a Guardian live blog
// whose headline had moved on to a different story, and two CNBC programmes
// titled only by the show and the date. Each gave the reader a link that did
// not lead to what the sentence said, and gave the triage a title with nothing
// in it to judge.
//
// A video page about one story is kept. Three CNBC clips in that brief -- the
// weak five-year auction, CLSA on chip demand, Bank of America's chief on
// inflation -- carried facts no article in the day's reading did.

// nonStory are path segments that mark a page collecting stories rather than
// telling one. A live blog is among them because its headline follows its
// latest entry: by the time the reader follows the link, it says something
// else.
var nonStory = map[string]bool{
	"topic": true, "topics": true, "tag": true, "tags": true,
	"section": true, "sections": true, "category": true, "categories": true,
	"quote": true, "quotes": true, "author": true, "authors": true,
	"live": true, "search": true,
}

// isStory reports whether a result is a single report rather than a page
// about many. The title is the one the reader would see, with the outlet's
// name already removed.
func isStory(link, title string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	var segments []string
	for _, s := range strings.Split(strings.ToLower(u.Path), "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) == 0 {
		return false // a home page
	}
	for _, s := range segments {
		if nonStory[s] {
			return false
		}
	}
	// A report's address carries a date, an id or a headline written out with
	// hyphens. One with none of them -- /markets, /investing/stock/tsm -- is a
	// section or a quote page. The exception is an id under a folder that only
	// holds reports: the BBC's /news/articles/ ids are letters and digits, and
	// about one in thirty has no digit at all.
	last := segments[len(segments)-1]
	if !strings.ContainsAny(u.Path, "0123456789") && !strings.ContainsAny(last, "-_") &&
		(len(segments) < 2 || !reports[segments[len(segments)-2]]) {
		return false
	}
	return !isProgramme(title)
}

// reports are folders whose every entry is one report, named by an id.
var reports = map[string]bool{
	"article": true, "articles": true, "content": true, "story": true, "stories": true,
}

// dated finds a calendar date in a title: "September 23, 2026", "Wed 23 Sep",
// "23/09/26", "2026-09-23".
var dated = regexp.MustCompile(`(?i)\b(?:(?:mon|tue|tues|wed|wednes|thu|thur|thurs|fri|sat|satur|sun)(?:day)?,?\s+)?` +
	`(?:(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?\s+\d{1,2}(?:st|nd|rd|th)?(?:,?\s+\d{4})?` +
	`|\d{1,2}\s+(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?(?:\s+\d{4})?` +
	`|\d{1,2}/\d{1,2}/\d{2,4}|\d{4}-\d{2}-\d{2})\b`)

// programmeWords is the most a title may say besides its date and still be
// only a programme's name: "Post Market Wrap: September 23, 2026", "CCTV
// Script 23/09/26". A headline that happens to carry a date says more.
const programmeWords = 3

// isProgramme reports whether a title is a show's name and a date, the title a
// recording of a whole programme gets.
func isProgramme(title string) bool {
	loc := dated.FindStringIndex(title)
	if loc == nil {
		return false
	}
	rest := title[:loc[0]] + " " + title[loc[1]:]
	words := 0
	for _, f := range strings.Fields(rest) {
		if strings.IndexFunc(f, unicode.IsLetter) >= 0 {
			words++
		}
	}
	return words <= programmeWords
}
