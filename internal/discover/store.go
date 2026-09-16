package discover

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Retention is how long a name is remembered after it last appeared. Long
// enough to tell a story that keeps returning from one that ran for a week,
// short enough that a name from last quarter does not read as current.
const Retention = 60 * 24 * time.Hour

// seen is what the record keeps about one name.
type seen struct {
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Days      int       `json:"days"`
	Name      string    `json:"name"`
}

// Store remembers which names have already been surfaced, so a name on its
// third day reads as a developing story rather than as news.
type Store struct {
	Path string

	names map[string]seen
}

// Load reads the record; a missing file is an empty one.
func LoadStore(path string) (*Store, error) {
	s := &Store{Path: path, names: map[string]seen{}}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.names); err != nil {
		s.names = map[string]seen{} // a corrupt record costs the day counts, nothing else
	}
	return s, nil
}

// Note records today's candidates and fills in how long each has been running.
func (s *Store) Note(candidates []model.Candidate, now time.Time) []model.Candidate {
	out := make([]model.Candidate, len(candidates))
	copy(out, candidates)

	for i, c := range out {
		key := candidateKey(c)
		record, known := s.names[key]
		switch {
		case !known:
			record = seen{FirstSeen: now, Days: 1, Name: c.Name}
		case sameDay(record.LastSeen, now):
			// A second brief on one day is not a second day.
		default:
			record.Days++
		}
		record.LastSeen = now
		record.Name = c.Name
		s.names[key] = record

		out[i].FirstSeen = record.FirstSeen
		out[i].Days = record.Days
	}

	cutoff := now.Add(-Retention)
	for key, record := range s.names {
		if record.LastSeen.Before(cutoff) {
			delete(s.names, key)
		}
	}
	return out
}

// Save writes the record atomically.
func (s *Store) Save() error {
	data, err := json.MarshalIndent(s.names, "", "  ")
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

// Len is how many names are remembered.
func (s *Store) Len() int { return len(s.names) }

// candidateKey identifies a company across days. The ticker is the stable
// identity where there is one; a private company has only its name, which the
// news may write differently from one day to the next, so it is normalized.
func candidateKey(c model.Candidate) string {
	if sym := c.Symbol(); sym != "" {
		return strings.ToUpper(sym)
	}
	return strings.Join(significantWords(c.Name), " ")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
