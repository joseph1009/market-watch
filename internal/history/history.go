// Package history remembers which stories earlier briefs already carried.
//
// Articles stay eligible for a week, so without this the same story can be
// written up on Monday exactly as it was on Friday, with nothing to tell the
// reader they have read it before. The record is deliberately thin: an article
// id, when it was first used, and the headline for anyone reading the file.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Retention is how long a story is remembered. Comfortably longer than the
// week an article stays eligible, so a story cannot age out of the record while
// it is still eligible to be reported again.
const Retention = 21 * 24 * time.Hour

// Entry is one story an earlier brief used.
type Entry struct {
	FirstUsed time.Time `json:"first_used"`
	Title     string    `json:"title"`
}

// Store is the record on the data volume.
type Store struct {
	Path string

	entries map[string]Entry
}

// Load reads the record. A missing file is an empty history, which is what a
// first run has.
func Load(path string) (*Store, error) {
	s := &Store{Path: path, entries: map[string]Entry{}}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &s.entries); err != nil {
		// A corrupt record must not stop the brief: the cost of losing it is
		// that a story may repeat once, which is the situation before any of
		// this existed.
		s.entries = map[string]Entry{}
	}
	return s, nil
}

// Mark flags the articles an earlier brief already used, so the prompt can say
// so. It returns a copy; the caller's slice is untouched.
func (s *Store) Mark(articles []model.Article) []model.Article {
	out := make([]model.Article, len(articles))
	copy(out, articles)
	for i := range out {
		if e, seen := s.entries[out[i].ID]; seen {
			out[i].Covered = e.FirstUsed
		}
	}
	return out
}

// Seen reports how many of these articles have been used before, which is the
// number worth logging: a high count on a quiet day explains a thin brief.
func (s *Store) Seen(articles []model.Article) int {
	n := 0
	for _, a := range articles {
		if _, ok := s.entries[a.ID]; ok {
			n++
		}
	}
	return n
}

// Record adds articles a brief has just used and drops what has aged out. The
// first use is kept rather than the latest, since what matters is when the
// reader first saw the story.
func (s *Store) Record(articles []model.Article, now time.Time) error {
	for _, a := range articles {
		if _, seen := s.entries[a.ID]; seen {
			continue
		}
		s.entries[a.ID] = Entry{FirstUsed: now, Title: a.Title}
	}

	cutoff := now.Add(-Retention)
	for id, e := range s.entries {
		if e.FirstUsed.Before(cutoff) {
			delete(s.entries, id)
		}
	}
	return s.save()
}

// Len is how many stories are remembered.
func (s *Store) Len() int { return len(s.entries) }

// save writes through a temporary file, so an interrupted write cannot leave a
// truncated record behind.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}

	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Titles lists what is remembered, newest first. For inspection rather than the
// brief.
func (s *Store) Titles() []string {
	type row struct {
		when  time.Time
		title string
	}
	rows := make([]row, 0, len(s.entries))
	for _, e := range s.entries {
		rows = append(rows, row{e.FirstUsed, e.Title})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].when.After(rows[j].when) })

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.title)
	}
	return out
}
