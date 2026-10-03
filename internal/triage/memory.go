package triage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// An article stays in the feeds for days, and until 2026-10-03 each brief
// sorted it again: on 1 October, 344 of the 629 articles sorted were more
// than a day old, so most had been rated the evening before. The rating and
// the sections read the article against the sectors' descriptions, and
// neither changes overnight. So the sorting remembers each verdict and gives
// it again, and only articles it has not seen go to the model.
//
// A verdict is kept for the sorting prompt it was given under, which holds
// the sectors' descriptions and companies. A change to either, or to the
// prompt's wording, is a new prompt, and every article is read again.

// MemoryRetention is how long a verdict is kept: the week an article stays
// eligible (feed.MaxArticleAge), and a day's margin.
const MemoryRetention = 8 * 24 * time.Hour

// remembered is one article's verdict as kept on the data volume.
type remembered struct {
	Rating int       `json:"rating"`
	Groups []string  `json:"groups,omitempty"`
	Prompt string    `json:"prompt"` // the sorting prompt it was given under, hashed
	Rated  time.Time `json:"rated"`
}

// Memory is the sorting's earlier verdicts, by article id.
type Memory struct {
	Path string

	mu       sync.Mutex
	verdicts map[string]remembered
	recalled int // how many the last pass gave again
}

// LoadMemory reads the verdicts kept at path. A missing or unreadable file
// is an empty memory: the cost is one brief that sorts everything, as every
// brief did before.
func LoadMemory(path string) (*Memory, error) {
	m := &Memory{Path: path, verdicts: map[string]remembered{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if json.Unmarshal(data, &m.verdicts) != nil {
		m.verdicts = map[string]remembered{}
	}
	return m, nil
}

// Recalled is how many articles the last pass did not send to the model.
func (m *Memory) Recalled() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recalled
}

// recallFor is the verdict given an article under this prompt, if there is
// one.
func (m *Memory) recallFor(id, prompt string) (verdict, bool) {
	if m == nil {
		return verdict{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.verdicts[id]
	if !ok || r.Prompt != prompt {
		return verdict{}, false
	}
	return verdict{rating: r.Rating, groups: r.Groups}, true
}

// rememberAt keeps a verdict the model has just given.
func (m *Memory) rememberAt(id, prompt string, v verdict, now time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verdicts[id] = remembered{Rating: v.rating, Groups: v.groups, Prompt: prompt, Rated: now}
}

// saveAt notes how many the pass recalled, drops what has aged out, and
// writes the rest through a temporary file, so an interrupted write cannot
// leave a truncated memory behind.
func (m *Memory) saveAt(now time.Time, recalled int) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recalled = recalled
	for id, r := range m.verdicts {
		if now.Sub(r.Rated) > MemoryRetention {
			delete(m.verdicts, id)
		}
	}
	data, err := json.Marshal(m.verdicts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o755); err != nil {
		return err
	}
	tmp := m.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.Path)
}

// promptKey names a sorting prompt shortly.
func promptKey(system string) string {
	sum := sha256.Sum256([]byte(system))
	return hex.EncodeToString(sum[:8])
}
