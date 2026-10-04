package terms

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Names, everyday words and figures are not jargon, and a long list is cut.
func TestTheListIsFiltered(t *testing.T) {
	listed := []model.ListedTerm{
		{Term: "HBM", Context: "memory chips"}, {Term: "Micron"}, {Term: "revenue"},
		{Term: "Q4 2026"}, {Term: "MU"}, {Term: "forward P/E"},
	}
	var got []string
	for _, l := range Filter(listed, []string{"Micron Technology", "micron", "MU"}) {
		got = append(got, l.Term)
	}
	if strings.Join(got, ",") != "HBM,forward P/E" {
		t.Errorf("Filter = %q", got)
	}
	var many []model.ListedTerm
	for i := 0; i < 40; i++ {
		many = append(many, model.ListedTerm{Term: "term" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	if n := len(Filter(many, nil)); n != MaxListed {
		t.Errorf("kept %d of 40, want %d", n, MaxListed)
	}
}

// A term the glossary holds keeps its page; any other gets a search, its
// context in it.
func TestUnknownTermsAreLinkedToASearch(t *testing.T) {
	known := []model.Term{{URL: "https://example.com/eps", Words: []string{"EPS"}}}
	linked := Linked(known, []model.ListedTerm{{Term: "eps"}, {Term: "HBM", Context: "memory chips"}, {Term: "forward P/E"}})
	if len(linked) != 3 || linked[0].URL != "https://example.com/eps" {
		t.Fatalf("linked = %+v", linked)
	}
	if linked[1].URL != "https://www.google.com/search?q=HBM+memory+chips+meaning" {
		t.Errorf("HBM: %s", linked[1].URL)
	}
	if linked[2].URL != "https://www.google.com/search?q=forward+P%2FE+meaning" {
		t.Errorf("forward P/E: %s", linked[2].URL)
	}
}

type checkReply string

func (r checkReply) Complete(context.Context, string, string) (string, model.Usage, error) {
	return string(r), model.Usage{}, nil
}

// A term is due for its check on its second sighting, not its first; the
// check's verdict sticks, and only the approved are linked.
func TestATermIsLearnedOnItsSecondSightingIfItPasses(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), File)}
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	first := []model.ListedTerm{{Term: "CoWoS", Context: "chip packaging"}, {Term: "Blackwell"}}
	if due, err := s.Record(first, "the analysis of MU", now); err != nil || len(due) != 0 {
		t.Fatalf("first sighting: due %v, err %v", due, err)
	}
	due, err := s.Record([]model.ListedTerm{{Term: "cowos"}, {Term: "Blackwell"}}, "the brief of Mon 5 Oct", now)
	if err != nil || len(due) != 2 {
		t.Fatalf("second sighting: due %v, err %v", due, err)
	}
	passed, err := Checker{Completer: checkReply("- CoWoS | TSMC chip packaging\n- Nvidia | not asked")}.Check(context.Background(), due)
	if err != nil || len(passed) != 1 || passed[0].Context != "TSMC chip packaging" {
		t.Fatalf("passed = %+v, err %v", passed, err)
	}
	if err := s.Settle(due, passed, now); err != nil {
		t.Fatal(err)
	}
	active := s.Active()
	if len(active) != 1 || active[0].Words[0] != "CoWoS" || !strings.Contains(active[0].URL, "TSMC+chip+packaging") {
		t.Errorf("active = %+v", active)
	}
	// Checked once, never due again.
	if due, _ := s.Record([]model.ListedTerm{{Term: "Blackwell"}}, "again", now); len(due) != 0 {
		t.Errorf("a rejected term came due again: %v", due)
	}
}
