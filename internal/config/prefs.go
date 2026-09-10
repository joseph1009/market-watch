package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/joseph1009/market-watch/internal/model"
)

// Prefs is the mutable half of configuration: the watchlists, the feed list and
// the chat to deliver to. The /watchlist and /sources commands rewrite it at
// runtime, so it lives on the data volume as YAML rather than in the image --
// it has to survive redeploys and stay hand-editable.
type Prefs struct {
	// SchemaVersion records which migrations this file has already had applied.
	// Absent means zero, which is any file written before versioning existed.
	SchemaVersion int `yaml:"schema_version"`

	ChatID int64 `yaml:"chat_id,omitempty"`

	// LastBrief holds the message ids of the most recent brief, so the next
	// one can clear it when ReplacePrevious is set. Runtime state rather than
	// preference, but it belongs on the same volume and is written by the same
	// atomic save.
	LastBrief []int64        `yaml:"last_brief,omitempty"`
	Groups    []model.Group  `yaml:"groups"`
	Sources   []model.Source `yaml:"sources"`
}

// LoadPrefs reads the preferences file, seeding it with defaults when absent.
func LoadPrefs(path string) (*Prefs, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		p := DefaultPrefs()
		if err := p.Save(path); err != nil {
			return nil, err
		}
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read prefs %s: %w", path, err)
	}

	var p Prefs
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse prefs %s: %w", path, err)
	}
	p.normalize()

	// Sources and watchlists added to the defaults since this file was written
	// have to reach an existing install, or new coverage only ever appears on a
	// fresh one -- and the file is written on first run, so that means never.
	// Migrations correct what already exists; the merge adds what is absent.
	// Both are needed: neither can do the other's job.
	migrated := p.Migrate()
	added := p.MergeDefaults()
	if len(migrated) > 0 || len(added) > 0 {
		if err := p.Save(path); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

// MergeDefaults adds sources and watchlists the defaults have gained, and
// returns what it added. Anything already present by ID is left exactly as it
// is, so a source the user disabled stays disabled and an edited watchlist
// keeps its edits.
//
// The consequence worth knowing: deleting an entry from prefs.yaml brings it
// back on the next run. Disabling is the supported way to switch a source off,
// which is what /sources off does.
func (p *Prefs) MergeDefaults() []string {
	defaults := DefaultPrefs()
	var added []string

	haveSource := make(map[string]bool, len(p.Sources))
	for _, s := range p.Sources {
		haveSource[s.ID] = true
	}
	for _, s := range defaults.Sources {
		if !haveSource[s.ID] {
			p.Sources = append(p.Sources, s)
			added = append(added, "source:"+s.ID)
		}
	}

	haveGroup := make(map[string]bool, len(p.Groups))
	for _, g := range p.Groups {
		haveGroup[g.ID] = true
	}
	for _, g := range defaults.Groups {
		if !haveGroup[g.ID] {
			p.Groups = append(p.Groups, g)
			added = append(added, "watchlist:"+g.ID)
		}
	}
	return added
}

// Save writes the preferences atomically. The bot rewrites this file while the
// scheduler may be reading it, and a half-written file would lose the watchlist.
func (p *Prefs) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", dir, err)
	}

	raw, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode prefs: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".prefs-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp prefs: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp prefs: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp prefs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp prefs: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace prefs %s: %w", path, err)
	}
	return nil
}

// Group returns the watchlist group with the given ID.
func (p *Prefs) Group(id string) (model.Group, bool) {
	for _, g := range p.Groups {
		if g.ID == id {
			return g, true
		}
	}
	return model.Group{}, false
}

// EnabledSources returns only the feeds currently switched on.
func (p *Prefs) EnabledSources() []model.Source {
	out := make([]model.Source, 0, len(p.Sources))
	for _, s := range p.Sources {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// normalize fills in anything a hand-edited file is allowed to omit.
func (p *Prefs) normalize() {
	for i := range p.Groups {
		if p.Groups[i].ID == "" {
			p.Groups[i].ID = model.GroupID(p.Groups[i].Name)
		}
	}
	for i := range p.Sources {
		if p.Sources[i].ID == "" {
			p.Sources[i].ID = model.GroupID(p.Sources[i].Name)
		}
		if p.Sources[i].Weight == 0 {
			p.Sources[i].Weight = 5
		}
	}
}
