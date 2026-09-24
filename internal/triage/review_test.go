package triage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/model"
)

// The review moves what is misplaced and leaves the rest where the sorting put
// it. Its reply names only the moves.
func TestReviewMovesWhatIsMisplacedAndLeavesTheRest(t *testing.T) {
	articles := []model.Article{
		{ID: "a", Title: "Morgan Stanley sees Shell hitting new highs", Rating: 3, GroupIDs: []string{"financials"}},
		{ID: "b", Title: "Nvidia beats on data centre revenue", Rating: 5, GroupIDs: []string{"semis"}},
		{ID: "c", Title: "A celebrity buys a vineyard", Rating: 1, GroupIDs: []string{"energy"}},
		{ID: "d", Title: "Oil tanker seized in the Gulf", Rating: 4},
	}
	groups := append(testGroups(), model.Group{ID: "financials", Name: "Financials", About: "Banks."})

	var prompt string
	fc := &fakeCompleter{reply: func(p string) (string, error) {
		prompt = p
		// The celebrity story is rated 1 and never offered; d is item 3.
		return "Shell's outlook is an energy story.\n1|energy\n3|energy,nowhere\n", nil
	}}
	got, moves, _, err := (&Reviewer{Completer: fc}).Review(context.Background(), articles, groups)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(prompt, "vineyard") {
		t.Error("an article rated 1 was sent for review; it reaches no section")
	}
	if !strings.Contains(prompt, "now: financials") || !strings.Contains(prompt, "now: none") {
		t.Errorf("the prompt does not say where each item sits now:\n%s", prompt)
	}
	if want := []string{"energy"}; !equal(got[0].GroupIDs, want) {
		t.Errorf("the broker note sits in %v, want %v", got[0].GroupIDs, want)
	}
	if want := []string{"semis"}; !equal(got[1].GroupIDs, want) {
		t.Errorf("an article the review left alone moved to %v", got[1].GroupIDs)
	}
	if want := []string{"energy"}; !equal(got[3].GroupIDs, want) {
		t.Errorf("the tanker sits in %v, want %v: unknown sections dropped", got[3].GroupIDs, want)
	}
	if len(moves) != 2 {
		t.Errorf("moves = %+v, want two", moves)
	}
	if !equal(articles[0].GroupIDs, []string{"financials"}) {
		t.Error("the review changed the caller's copy of the articles")
	}
}

// "-" takes an article out of every section, where it can still reach the
// overview; an article the sorting rated 3 and placed nowhere is not given a
// section now, since that is where placements were mostly stretches.
func TestReviewCanUnplaceButDoesNotPlaceAWeakArticle(t *testing.T) {
	articles := []model.Article{
		{ID: "a", Title: "Opinion: markets feel frothy", Rating: 3, GroupIDs: []string{"semis"}},
		{ID: "b", Title: "A minor supplier changes its logo", Rating: 3},
	}
	fc := &fakeCompleter{reply: fixed("1|-\n2|semis")}
	got, moves, _, _ := (&Reviewer{Completer: fc}).Review(context.Background(), articles, testGroups())

	if len(got[0].GroupIDs) != 0 {
		t.Errorf("the opinion piece is still in %v", got[0].GroupIDs)
	}
	if len(got[1].GroupIDs) != 0 {
		t.Errorf("a rating-3 article with no section was given %v", got[1].GroupIDs)
	}
	if len(moves) != 1 {
		t.Errorf("moves = %+v, want only the unplacing", moves)
	}
}

// A reply of NONE, or prose, changes nothing.
func TestReviewThatChangesNothingMovesNothing(t *testing.T) {
	articles := []model.Article{{ID: "a", Title: "Oil rises", Rating: 4, GroupIDs: []string{"energy"}}}
	got, moves, _, err := (&Reviewer{Completer: &fakeCompleter{reply: fixed("NONE")}}).
		Review(context.Background(), articles, testGroups())
	if err != nil || len(moves) != 0 || !equal(got[0].GroupIDs, []string{"energy"}) {
		t.Errorf("got %v, moves %v, err %v; want nothing changed", got[0].GroupIDs, moves, err)
	}
}

// A batch that fails leaves its articles where the sorting put them, and the
// error says so; the brief carries on.
func TestAFailedReviewLeavesTheSortingStanding(t *testing.T) {
	articles := []model.Article{{ID: "a", Title: "Oil rises", Rating: 4, GroupIDs: []string{"energy"}}}
	fc := &fakeCompleter{reply: func(string) (string, error) { return "", errors.New("session limit") }}
	got, moves, _, err := (&Reviewer{Completer: fc}).Review(context.Background(), articles, testGroups())
	if err == nil || !strings.Contains(err.Error(), "1 of 1 review batches failed") {
		t.Errorf("err = %v", err)
	}
	if len(moves) != 0 || !equal(got[0].GroupIDs, []string{"energy"}) {
		t.Errorf("a failed review moved %v", got[0].GroupIDs)
	}
}

func equal(a, b []string) bool {
	return strings.Join(a, ",") == strings.Join(b, ",")
}
