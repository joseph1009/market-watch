package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	s, err := Load(filepath.Join(t.TempDir(), "covered.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func TestMarksWhatAnEarlierBriefCarried(t *testing.T) {
	friday := time.Date(2026, 9, 11, 20, 30, 0, 0, time.UTC)
	articles := []model.Article{{ID: "a", Title: "Oil surges"}, {ID: "b", Title: "Fed holds"}}

	s := tempStore(t)
	if err := s.Record(articles[:1], friday); err != nil {
		t.Fatalf("Record: %v", err)
	}

	marked := s.Mark(articles)
	if marked[0].Covered.IsZero() {
		t.Error("the story Friday carried was not marked")
	}
	if !marked[1].Covered.IsZero() {
		t.Error("an unseen story was marked as covered")
	}
	if got := s.Seen(articles); got != 1 {
		t.Errorf("Seen = %d, want 1", got)
	}
	// The caller's slice must be untouched, since it is the same data the
	// prompt and the rendered sources are built from.
	if !articles[0].Covered.IsZero() {
		t.Error("Mark wrote through to the caller's articles")
	}
}

// What matters is when the reader first saw the story, not the last time it
// turned up in the feeds.
func TestKeepsTheFirstUseNotTheLatest(t *testing.T) {
	friday := time.Date(2026, 9, 11, 20, 30, 0, 0, time.UTC)
	monday := friday.AddDate(0, 0, 3)
	article := model.Article{ID: "a", Title: "Oil surges"}

	s := tempStore(t)
	_ = s.Record([]model.Article{article}, friday)
	_ = s.Record([]model.Article{article}, monday)

	if got := s.Mark([]model.Article{article})[0].Covered; !got.Equal(friday) {
		t.Errorf("Covered = %s, want the first use on %s", got, friday)
	}
}

func TestForgetsStoriesOlderThanRetention(t *testing.T) {
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	s := tempStore(t)
	_ = s.Record([]model.Article{{ID: "old", Title: "Ancient"}}, old)

	_ = s.Record([]model.Article{{ID: "new", Title: "Today"}}, old.Add(Retention+time.Hour))
	if s.Len() != 1 {
		t.Errorf("remembered %d stories, want only the recent one", s.Len())
	}
}

func TestSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "covered.json")

	first, _ := Load(path)
	_ = first.Record([]model.Article{{ID: "a", Title: "Oil surges"}}, time.Now().UTC())

	second, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second.Seen([]model.Article{{ID: "a"}}) != 1 {
		t.Error("the record did not survive a restart")
	}
}

// Losing the record costs one repeated story; failing the brief over it costs
// the brief.
func TestACorruptRecordIsNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "covered.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error for a corrupt file: %v", err)
	}
	if s.Len() != 0 {
		t.Errorf("remembered %d stories from a corrupt file", s.Len())
	}
}
