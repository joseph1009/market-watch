package search

import (
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// Outlet is a publication searches are allowed to return, with the name the
// brief shows for it and the weight it ranks with.
type Outlet struct {
	Domain string
	Name   string
	Weight int
}

// IDPrefix marks a source id as a search result rather than a feed: an article
// found on reuters.com by searching has the source id "web:reuters.com".
const IDPrefix = "web:"

// SourceID is the id this outlet's articles carry through the pipeline.
func (o Outlet) SourceID() string { return IDPrefix + o.Domain }

// IsSearch reports whether a source id belongs to a search result.
func IsSearch(sourceID string) bool { return strings.HasPrefix(sourceID, IDPrefix) }

// Outlets are the publications searches are restricted to.
//
// This is still a list, but of a different kind from the feed list: a domain
// name does not move, go behind bot protection or change format, which are the
// three things that have broken feed addresses. Adding a publication is one
// line and needs nothing checked beyond the name.
//
// Weights follow the feed list's scale, and an outlet that is also polled as a
// feed carries the same weight here, so which route a story arrived by does not
// change how it ranks. Wires and the major business press sit at 8, regional
// and specialist papers of record at 7, trade press at 6, and general or
// commodity outlets at 5.
//
// The restriction is what makes search usable. Without it, a search for chip
// news on 2026-09-23 returned five results, three of them Facebook and Threads
// posts and one a Chinese price aggregator; restricted to this list, the same
// search returned twenty, from Reuters, the FT, CNBC, Barron's and Axios.
var Outlets = []Outlet{
	// Wires and the major business press. Reuters, AP, Bloomberg, the Wall
	// Street Journal and Nikkei Asia offer no feed this service can use,
	// which is the gap search was brought in to close.
	{Domain: "reuters.com", Name: "Reuters", Weight: 8},
	{Domain: "apnews.com", Name: "Associated Press", Weight: 8},
	{Domain: "bloomberg.com", Name: "Bloomberg", Weight: 8},
	{Domain: "wsj.com", Name: "Wall Street Journal", Weight: 8},
	{Domain: "ft.com", Name: "Financial Times", Weight: 8},
	{Domain: "cnbc.com", Name: "CNBC", Weight: 8},
	{Domain: "asia.nikkei.com", Name: "Nikkei Asia", Weight: 8},
	{Domain: "channelnewsasia.com", Name: "CNA", Weight: 8},
	{Domain: "barrons.com", Name: "Barron's", Weight: 7},
	{Domain: "nytimes.com", Name: "New York Times", Weight: 7},
	{Domain: "economist.com", Name: "The Economist", Weight: 7},
	{Domain: "theinformation.com", Name: "The Information", Weight: 7},
	{Domain: "marketwatch.com", Name: "MarketWatch", Weight: 6},
	{Domain: "axios.com", Name: "Axios", Weight: 6},

	// Asia and Europe.
	{Domain: "scmp.com", Name: "South China Morning Post", Weight: 7},
	{Domain: "straitstimes.com", Name: "Straits Times", Weight: 7},
	{Domain: "businesstimes.com.sg", Name: "Business Times", Weight: 7},
	{Domain: "caixinglobal.com", Name: "Caixin", Weight: 7},
	{Domain: "japantimes.co.jp", Name: "Japan Times", Weight: 6},
	{Domain: "dw.com", Name: "Deutsche Welle", Weight: 6},
	{Domain: "bbc.com", Name: "BBC", Weight: 6},
	{Domain: "theguardian.com", Name: "The Guardian", Weight: 5},
	{Domain: "aljazeera.com", Name: "Al Jazeera", Weight: 5},

	// Trade press, one or more for each watchlist. These are where a story
	// about a supplier or a trial result appears before, or instead of, the
	// general press.
	{Domain: "semianalysis.com", Name: "SemiAnalysis", Weight: 7},
	{Domain: "digitimes.com", Name: "DigiTimes", Weight: 6},
	{Domain: "trendforce.com", Name: "TrendForce", Weight: 6},
	{Domain: "tomshardware.com", Name: "Tom's Hardware", Weight: 5},
	{Domain: "theverge.com", Name: "The Verge", Weight: 5},
	{Domain: "techcrunch.com", Name: "TechCrunch", Weight: 5},
	{Domain: "spglobal.com", Name: "S&P Global", Weight: 7},
	{Domain: "oilprice.com", Name: "OilPrice.com", Weight: 5},
	{Domain: "statnews.com", Name: "STAT", Weight: 7},
	{Domain: "fiercepharma.com", Name: "Fierce Pharma", Weight: 6},
	{Domain: "fiercebiotech.com", Name: "Fierce Biotech", Weight: 6},
	{Domain: "endpts.com", Name: "Endpoints News", Weight: 6},
	{Domain: "biopharmadive.com", Name: "BioPharma Dive", Weight: 6},
	{Domain: "defensenews.com", Name: "Defense News", Weight: 6},
	{Domain: "breakingdefense.com", Name: "Breaking Defense", Weight: 6},
	{Domain: "americanbanker.com", Name: "American Banker", Weight: 6},
	{Domain: "autonews.com", Name: "Automotive News", Weight: 6},
	{Domain: "coindesk.com", Name: "CoinDesk", Weight: 6},
	{Domain: "theblock.co", Name: "The Block", Weight: 6},
	{Domain: "retaildive.com", Name: "Retail Dive", Weight: 5},
	{Domain: "variety.com", Name: "Variety", Weight: 5},
	{Domain: "hollywoodreporter.com", Name: "Hollywood Reporter", Weight: 5},
	{Domain: "deadline.com", Name: "Deadline", Weight: 5},
}

// Domains is the restriction list sent with every search.
func Domains() []string {
	out := make([]string, len(Outlets))
	for i, o := range Outlets {
		out[i] = o.Domain
	}
	return out
}

// Sources describes every outlet for the scorer, which needs a weight for each
// source id it sees. Like the SEC filings entry, these are not feeds: they have
// no address, never appear in /sources, and are never fetched.
func Sources() []model.Source {
	out := make([]model.Source, len(Outlets))
	for i, o := range Outlets {
		out[i] = model.Source{ID: o.SourceID(), Name: o.Name, Weight: o.Weight}
	}
	return out
}

// outletFor finds the outlet an address belongs to. A subdomain belongs to its
// parent, so markets.ft.com is the Financial Times.
//
// A host on no list should not arrive, since searches are restricted, but if
// one does it is named by its host and ranks with no source weight rather
// than borrowing one it has not earned.
func outletFor(rawURL string) Outlet {
	h := host(rawURL)
	for _, o := range Outlets {
		if h == o.Domain || strings.HasSuffix(h, "."+o.Domain) {
			return o
		}
	}
	return Outlet{Domain: h, Name: h}
}

// titleSeparators are what a headline puts between itself and the outlet's
// name: "Oil falls on Gulf supply - Reuters", "... | OilPrice.com".
var titleSeparators = []string{" - ", " | ", " — ", " – "}

// trimOutlet removes the outlet's name from the end of a headline.
//
// Feeds give the headline alone; search gives the page title, which usually
// ends in the outlet's name. Left on, the name is a word every headline from
// that outlet shares, which nudges unrelated stories from it toward looking
// alike and the same story from a feed and a search away from it.
func trimOutlet(title string, o Outlet) string {
	label, name := domainLabel(o.Domain), words(o.Name)
	// Twice at most: "Headline - Crude Oil Prices Today | OilPrice.com" loses
	// its outlet, and the section name before it is left for the reader.
	for i := 0; i < 2; i++ {
		cut := -1
		for _, sep := range titleSeparators {
			if j := strings.LastIndex(title, sep); j > cut {
				cut = j
			}
		}
		if cut <= 0 {
			break
		}
		// Whole words, not substrings: the FT's label is "ft", which is
		// inside "shift" and "Microsoft".
		tail := words(title[cut:])
		if !hasRun(tail, name) && !hasRun(tail, []string{label}) {
			break
		}
		title = strings.TrimSpace(title[:cut])
	}
	return title
}

// domainLabel is the part of a domain that names the outlet: "nikkei" in
// asia.nikkei.com, "businesstimes" in businesstimes.com.sg.
func domainLabel(domain string) string {
	parts := strings.Split(strings.ToLower(domain), ".")
	parts = parts[:len(parts)-1] // the country or generic ending
	if n := len(parts); n > 1 && (parts[n-1] == "co" || parts[n-1] == "com") {
		parts = parts[:n-1] // co.jp, com.sg
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// words splits text into lowercase runs of letters and digits.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// hasRun reports whether seq appears in list as consecutive entries.
func hasRun(list, seq []string) bool {
	if len(seq) == 0 || seq[0] == "" {
		return false
	}
	for i := 0; i+len(seq) <= len(list); i++ {
		match := true
		for j := range seq {
			if list[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
