package search

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

func TestQueriesPutGeneralSearchesFirstThenOnePerWatchlist(t *testing.T) {
	groups := config.DefaultPrefs().Groups
	got := Queries(groups)

	if want := len(General) + len(groups); len(got) != want {
		t.Fatalf("%d queries, want %d", len(got), want)
	}
	for i, q := range General {
		if got[i] != q {
			t.Errorf("query %d = %+v, want the general %+v", i, got[i], q)
		}
	}
	for i, g := range groups {
		q := got[len(General)+i]
		if q.Label != g.ID {
			t.Errorf("query for %s labelled %q", g.ID, q.Label)
		}
		if utf8.RuneCountInString(q.Text) > maxQueryRunes {
			t.Errorf("%s: %d characters, over %d", g.ID, utf8.RuneCountInString(q.Text), maxQueryRunes)
		}
	}
}

// The search asks for the description's first sentence only: the ones after it
// are for the sorting, and "belongs to Media & Entertainment" would only blur a
// search for energy news.
func TestASectorIsSearchedForByItsFirstSentence(t *testing.T) {
	g := model.Group{
		ID: "energy", Name: "Energy",
		About:     "Oil, gas, fuel and power.\n  That takes in Brent and WTI. Netflix belongs elsewhere.",
		Companies: []model.Company{{Name: "Exxon"}},
	}
	if got := groupQuery(g); got != "Energy news: Oil, gas, fuel and power." {
		t.Errorf("groupQuery = %q", got)
	}
}

func TestASectorWithoutADescriptionFallsBackToItsCompanies(t *testing.T) {
	g := model.Group{
		ID: "custom", Name: "Shipping",
		Companies: []model.Company{{Name: "Maersk"}, {Symbol: "COSCO", Name: "COSCO"}},
	}
	if got := groupQuery(g); got != "Shipping news: Maersk, COSCO" {
		t.Errorf("groupQuery = %q", got)
	}
	if got := groupQuery(model.Group{ID: "bare", Name: "Bare"}); got != "Bare news" {
		t.Errorf("a watchlist with only a name = %q", got)
	}
}

// Adding watchlists must not add cost without bound.
func TestQueriesAreCapped(t *testing.T) {
	var groups []model.Group
	for i := 0; i < 40; i++ {
		groups = append(groups, model.Group{ID: fmt.Sprint("g", i), Name: fmt.Sprint("Group ", i)})
	}
	got := Queries(groups)
	if len(got) != MaxQueries {
		t.Errorf("%d queries, want the cap of %d", len(got), MaxQueries)
	}
	if got[0] != General[0] {
		t.Error("the general searches should survive the cap")
	}
}

func TestClipCutsOnAWordBoundary(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := clip(long)
	if utf8.RuneCountInString(got) > maxQueryRunes {
		t.Errorf("clipped to %d characters", utf8.RuneCountInString(got))
	}
	if strings.HasSuffix(got, " ") || !strings.HasSuffix(got, "word") {
		t.Errorf("clip ended mid-word or on a space: %q", got[len(got)-10:])
	}
}

// A mover search names the company the way a headline would, and asks for
// fewer results than a sector search: one company's day does not fill twenty.
func TestMoverQueryAsksWhyInPlainWords(t *testing.T) {
	q := MoverQuery("MCDONALDS CORP", "MCD", -4.8)
	if q.Text != "Why did MCDONALDS (MCD) shares fall?" {
		t.Errorf("Text = %q", q.Text)
	}
	if q.Label != "mover:MCD" || q.Max != moverResults {
		t.Errorf("Label = %q, Max = %d", q.Label, q.Max)
	}
	if got := MoverQuery("", "RIOT", 6.1).Text; got != "Why did RIOT shares rise?" {
		t.Errorf("without a name, Text = %q", got)
	}
}

func TestPlainNameDropsTheCorporateWords(t *testing.T) {
	tests := map[string]string{
		"MCDONALDS CORP":                            "MCDONALDS",
		"KKR & Co. Inc.":                            "KKR",
		"Rivian Automotive, Inc. / DE":              "Rivian Automotive",
		"ASML HOLDING NV":                           "ASML",
		"UnitedHealth Group Inc":                    "UnitedHealth Group",
		"Taiwan Semiconductor Manufacturing Co Ltd": "Taiwan Semiconductor Manufacturing",
		"Inc": "Inc",
	}
	for in, want := range tests {
		if got := PlainName(in); got != want {
			t.Errorf("PlainName(%q) = %q, want %q", in, got, want)
		}
	}
}
