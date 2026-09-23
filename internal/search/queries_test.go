package search

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/internal/config"
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

func TestAWatchlistIsSearchedForByItsSectorSentence(t *testing.T) {
	g := model.Group{
		ID: "energy", Name: "Energy",
		Scope: "Oil, gas, fuel and power.",
		Names: []string{"Exxon"},
	}
	if got := groupQuery(g); got != "Energy news: Oil, gas, fuel and power." {
		t.Errorf("groupQuery = %q", got)
	}
}

func TestAWatchlistWithoutASentenceFallsBackToItsNames(t *testing.T) {
	g := model.Group{
		ID: "custom", Name: "Shipping",
		Names:    []string{"Maersk", "COSCO"},
		Keywords: []string{"container rates", " "},
	}
	if got := groupQuery(g); got != "Shipping news: Maersk, COSCO, container rates" {
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
