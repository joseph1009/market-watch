package triage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// A placement rated 3 is not made, but kept in reserve for a thin section; one
// rated lower is not kept at all, and neither is a section the article is in
// already.
func TestTheSortingKeepsWhatItRatedThreeInReserve(t *testing.T) {
	articles := []model.Article{
		{ID: "a", Title: "Bitcoin slips under $84,000"},
		{ID: "b", Title: "A crypto columnist's view"},
		{ID: "c", Title: "Nvidia opens an office", GroupIDs: []string{"semis"}},
	}
	tr := &Triager{Completer: &fakeCompleter{reply: fixed("1|3|energy,semis,nowhere\n2|2|energy\n3|3|semis,energy")}}

	got, _, _ := tr.Triage(context.Background(), articles, testGroups())
	if len(got[0].GroupIDs) != 0 || !equal(got[0].Reserve, []string{"energy", "semis"}) {
		t.Errorf("rated 3: groups %v, reserve %v; want none placed, energy and semis in reserve", got[0].GroupIDs, got[0].Reserve)
	}
	if len(got[1].Reserve) != 0 {
		t.Errorf("rated 2: reserve %v, want none", got[1].Reserve)
	}
	if !equal(got[2].Reserve, []string{"energy"}) {
		t.Errorf("name match rated 3: reserve %v, want only the section it is not in", got[2].Reserve)
	}
}

// A section short of news is filled from the reserve, strongest first, up to
// the target and no further. A full section gets nothing, and neither a repeat
// nor a story another section already carries is used.
func TestTopUpFillsOnlyThinSectionsAndOnlyToTheTarget(t *testing.T) {
	articles := []model.Article{
		{ID: "e1", Rating: 4, GroupIDs: []string{"energy"}},
		{ID: "e2", Rating: 4, GroupIDs: []string{"energy"}},
		{ID: "s1", Rating: 4, GroupIDs: []string{"semis"}},
		{ID: "s2", Rating: 2, GroupIDs: []string{"semis"}}, // too weak to be written: does not count
		{ID: "r1", Rating: 3, Reserve: []string{"energy"}},
		{ID: "old", Rating: 3, Reserve: []string{"semis"}, Covered: time.Now()},
		{ID: "named", Rating: 3, GroupIDs: []string{"energy"}, Reserve: []string{"semis"}},
		{ID: "r2", Rating: 3, Reserve: []string{"semis"}},
		{ID: "r3", Rating: 3, Reserve: []string{"semis"}},
	}

	got, added := TopUp(articles, testGroups(), 2)

	byID := map[string]model.Article{}
	for _, a := range got {
		byID[a.ID] = a
	}
	if added != 1 || !byID["r2"].InGroup("semis") || !byID["r2"].ToppedUp {
		t.Fatalf("added %d, r2 = %+v; want r2 alone added to semis", added, byID["r2"])
	}
	for _, id := range []string{"r1", "old", "r3"} {
		if len(byID[id].GroupIDs) != 0 {
			t.Errorf("%s was placed in %v", id, byID[id].GroupIDs)
		}
	}
	if !equal(byID["named"].GroupIDs, []string{"energy"}) {
		t.Errorf("an article already in a section was topped up into %v", byID["named"].GroupIDs)
	}
	if len(articles[7].GroupIDs) != 0 {
		t.Error("TopUp changed the caller's copy of the articles")
	}
}

// The review settles every top-up: kept where it names the section, withdrawn
// where it says "-", names another section, or says nothing. None of that is
// a move, and the rest of the review works as before.
func TestTheReviewKeepsOnlyTheTopUpsItConfirms(t *testing.T) {
	articles := []model.Article{
		{ID: "kept", Title: "NYSE to sell tokenised shares", Rating: 3, GroupIDs: []string{"semis"}, ToppedUp: true},
		{ID: "removed", Title: "Crypto prices today", Rating: 3, GroupIDs: []string{"semis"}, ToppedUp: true},
		{ID: "moved", Title: "Oil slips below $100", Rating: 3, GroupIDs: []string{"semis"}, ToppedUp: true},
		{ID: "half", Title: "Chip plant runs short of gas", Rating: 3, GroupIDs: []string{"semis", "energy"}, ToppedUp: true},
		{ID: "silent", Title: "A blockchain conference opens", Rating: 3, GroupIDs: []string{"semis"}, ToppedUp: true},
		{ID: "normal", Title: "Exxon cuts its outlook", Rating: 4, GroupIDs: []string{"semis"}},
	}
	var prompt string
	fc := &fakeCompleter{reply: func(p string) (string, error) {
		prompt = p
		return "1|semis\n2|-\n3|energy\n4|energy\n6|energy", nil
	}}
	got, moves, _, err := (&Reviewer{Completer: fc}).Review(context.Background(), articles, testGroups())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompt, "now: semis (added to fill a thin section)") {
		t.Errorf("the prompt does not mark the top-ups:\n%s", prompt)
	}
	want := map[string][]string{"kept": {"semis"}, "removed": nil, "moved": nil, "half": {"energy"}, "silent": nil, "normal": {"energy"}}
	for _, a := range got {
		if !equal(a.GroupIDs, want[a.ID]) {
			t.Errorf("%s sits in %v, want %v", a.ID, a.GroupIDs, want[a.ID])
		}
		if a.ToppedUp != (len(want[a.ID]) > 0 && a.ID != "normal") {
			t.Errorf("%s: ToppedUp = %v", a.ID, a.ToppedUp)
		}
	}
	if len(moves) != 1 || moves[0].Title != "Exxon cuts its outlook" {
		t.Errorf("moves = %+v, want only the ordinary one", moves)
	}
}

// A review that fails confirms nothing, so every top-up is withdrawn while
// the sorting's own placements stand.
func TestAFailedReviewWithdrawsTheTopUps(t *testing.T) {
	articles := []model.Article{
		{ID: "a", Title: "Bitcoin slips", Rating: 3, GroupIDs: []string{"semis"}, ToppedUp: true},
		{ID: "b", Title: "Oil rises", Rating: 4, GroupIDs: []string{"energy"}},
	}
	fc := &fakeCompleter{reply: func(string) (string, error) { return "", errors.New("session limit") }}
	got, _, _, err := (&Reviewer{Completer: fc}).Review(context.Background(), articles, testGroups())
	if err == nil {
		t.Error("the failure was not reported")
	}
	if len(got[0].GroupIDs) != 0 || got[0].ToppedUp {
		t.Errorf("an unconfirmed top-up stayed in %v", got[0].GroupIDs)
	}
	if !equal(got[1].GroupIDs, []string{"energy"}) {
		t.Errorf("the sorting's placement became %v", got[1].GroupIDs)
	}
}
