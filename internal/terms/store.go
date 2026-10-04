package terms

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// File is the learned terms' file in the data directory.
const File = "terms.json"

// MinSightings is how many reports must list a term before it is checked
// and, if it passes, learned. One report's one-off word, a product's name,
// should not end up linked in every brief.
const MinSightings = 2

// The states a learned term goes through.
const (
	Pending  = "pending"  // seen, not yet checked
	Approved = "approved" // checked, and linked wherever it appears
	Rejected = "rejected" // checked, and never linked by this list again
)

// Learned is one term the store has seen.
type Learned struct {
	model.ListedTerm
	Seen      int       `json:"seen"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Where     []string  `json:"where,omitempty"` // the last few reports that listed it
	Status    string    `json:"status"`
	Checked   time.Time `json:"checked,omitempty"`
}

// Store keeps the learned terms in one file. A missing file is an empty
// store.
type Store struct {
	Path string
	mu   sync.Mutex
}

// keepWhere is how many of the reports that listed a term are remembered.
const keepWhere = 5

// Record notes that one report listed these terms, and returns those now seen
// often enough to check that have not been checked yet.
func (s *Store) Record(listed []model.ListedTerm, where string, now time.Time) ([]Learned, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return nil, err
	}
	var due []Learned
	for _, l := range listed {
		key := strings.ToLower(l.Term)
		t, ok := all[key]
		if !ok {
			t = &Learned{ListedTerm: l, FirstSeen: now, Status: Pending}
			all[key] = t
		}
		t.Seen++
		t.LastSeen = now
		if t.Context == "" {
			t.Context = l.Context
		}
		t.Where = append(t.Where, where)
		if len(t.Where) > keepWhere {
			t.Where = t.Where[len(t.Where)-keepWhere:]
		}
		if t.Status == Pending && t.Seen >= MinSightings {
			due = append(due, *t)
		}
	}
	return due, s.save(all)
}

// Settle records a check: each term in checked is approved where it is in
// passed, with the context the check gave it, and rejected otherwise.
func (s *Store) Settle(checked []Learned, passed []model.ListedTerm, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return err
	}
	ok := map[string]model.ListedTerm{}
	for _, p := range passed {
		ok[strings.ToLower(p.Term)] = p
	}
	for _, c := range checked {
		t := all[strings.ToLower(c.Term)]
		if t == nil {
			continue
		}
		t.Checked = now
		if p, pass := ok[strings.ToLower(c.Term)]; pass {
			t.Status = Approved
			if p.Context != "" {
				t.Context = p.Context
			}
		} else {
			t.Status = Rejected
		}
	}
	return s.save(all)
}

// Approved is the learned terms that passed their check, in alphabetical
// order.
func (s *Store) Approved() ([]Learned, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []Learned
	for _, t := range all {
		if t.Status == Approved {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Term) < strings.ToLower(out[j].Term) })
	return out, nil
}

// Active is the approved terms as links, each to a search for its meaning.
func (s *Store) Active() []model.Term {
	approved, err := s.Approved()
	if err != nil {
		return nil
	}
	out := make([]model.Term, 0, len(approved))
	for _, t := range approved {
		out = append(out, model.Term{URL: SearchURL(t.ListedTerm), Words: []string{t.Term}})
	}
	return out
}

func (s *Store) load() (map[string]*Learned, error) {
	all := map[string]*Learned{}
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Learned
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	for _, t := range list {
		all[strings.ToLower(t.Term)] = t
	}
	return all, nil
}

// save writes the terms in alphabetical order, through a temporary file so a
// crash never leaves half of one.
func (s *Store) save(all map[string]*Learned) error {
	list := make([]*Learned, 0, len(all))
	for _, t := range all {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Term) < strings.ToLower(list[j].Term) })
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
