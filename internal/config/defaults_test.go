package config

import (
	"strings"
	"testing"
)

// A keyword in two watchlists tags its stories into both, which turns two
// sections into one story told twice. This would have caught "wage growth"
// sitting in Macro & Rates and Consumer at the same time.
func TestNoKeywordBelongsToTwoWatchlists(t *testing.T) {
	owner := map[string]string{}
	for _, g := range DefaultPrefs().Groups {
		for _, k := range g.Keywords {
			key := strings.ToLower(k)
			if first, clash := owner[key]; clash {
				t.Errorf("keyword %q is in both %s and %s", k, first, g.ID)
				continue
			}
			owner[key] = g.ID
		}
	}
}

// The same for tickers: one symbol in two sectors double-sections the company.
func TestNoTickerBelongsToTwoWatchlists(t *testing.T) {
	owner := map[string]string{}
	for _, g := range DefaultPrefs().Groups {
		for _, ticker := range g.Tickers {
			if first, clash := owner[ticker]; clash {
				t.Errorf("ticker %s is in both %s and %s", ticker, first, g.ID)
				continue
			}
			owner[ticker] = g.ID
		}
	}
}

// The symbols deliberately tracked by name only, because as bare words they
// match something that is not the company.
func TestAmbiguousSymbolsAreNotTrackedAsTickers(t *testing.T) {
	banned := map[string]string{
		"MPC": "the Bank of England's Monetary Policy Committee",
		"EMR": "electronic medical record",
		"ALL": "an ordinary word in an all-caps headline",
		"URI": "a web address",
		"MA":  "a US state",
		"CB":  "an abbreviation in wider use",
		"LNG": "already an Energy keyword",
	}
	for _, g := range DefaultPrefs().Groups {
		for _, ticker := range g.Tickers {
			if why, bad := banned[ticker]; bad {
				t.Errorf("%s is tracked as a ticker in %s, but it matches %s", ticker, g.ID, why)
			}
		}
	}
}

// Their companies still have to be tracked, just by name.
func TestAmbiguousSymbolsAreTrackedByName(t *testing.T) {
	want := map[string]string{
		"Marathon Petroleum": "energy",
		"Cheniere":           "energy",
		"Emerson":            "industrials-defense",
		"United Rentals":     "industrials-defense",
		"Allstate":           "financials",
		"Mastercard":         "financials",
		"Chubb":              "financials",
	}
	for name, groupID := range want {
		g := DefaultPrefs().group(groupID)
		if g == nil {
			t.Errorf("watchlist %s is missing", groupID)
			continue
		}
		found := false
		for _, n := range g.Names {
			if strings.EqualFold(n, name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is not tracked by name in %s: %v", name, groupID, g.Names)
		}
	}
}
