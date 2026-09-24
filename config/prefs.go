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

// Prefs is what the service keeps on the data volume: the chat it delivers to,
// the messages of its last brief, and the watchlist and feed changes made from
// Telegram. It has to survive redeploys, so it lives there rather than in the
// image.
//
// The lists themselves are not in it. They are the files in this directory,
// and Groups and Sources are those files with the changes below applied,
// worked out at load and never saved. A file written before this, which also
// held the whole watchlist and feed list, loads the same way: the old lists are
// ignored, and dropped at the next save.
type Prefs struct {
	ChatID int64 `yaml:"chat_id,omitempty"`

	// LastBrief holds the message ids of the most recent brief, so the next
	// one can clear it when ReplacePrevious is set.
	LastBrief []int64 `yaml:"last_brief,omitempty"`

	// Edits are the companies added and removed with /watchlist, and
	// FeedSwitches the feeds switched on or off with /sources, by id.
	Edits        Edits           `yaml:"watchlist_edits,omitempty"`
	FeedSwitches map[string]bool `yaml:"feed_switches,omitempty"`

	// Groups and Sources are the watchlist and feed list in force.
	Groups  []model.Group  `yaml:"-"`
	Sources []model.Source `yaml:"-"`
}

// DefaultPrefs is a fresh install: the lists as the files have them, and
// nothing changed.
func DefaultPrefs() *Prefs {
	p := &Prefs{}
	if _, err := p.resolve(); err != nil {
		// The files are compiled in and tested; a failure here is a broken
		// build, not a condition to handle.
		panic(err)
	}
	return p
}

// LoadPrefs reads the preferences file. A missing file is a fresh install.
func LoadPrefs(path string) (*Prefs, error) {
	var p Prefs
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read prefs %s: %w", path, err)
	default:
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse prefs %s: %w", path, err)
		}
	}

	pruned, err := p.resolve()
	if err != nil {
		return nil, err
	}
	// Edits the files now carry are dropped from the volume too, so the list
	// of edits is always what the files do not yet say.
	if pruned {
		if err := p.Save(path); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

// resolve works out Groups and Sources from the files and the changes, and
// drops changes the files already say. It reports whether it dropped any.
func (p *Prefs) resolve() (bool, error) {
	base, err := Watchlist()
	if err != nil {
		return false, err
	}
	feeds, err := Feeds()
	if err != nil {
		return false, err
	}

	edits := pruneEdits(base, p.Edits)
	switches := pruneSwitches(feeds, p.FeedSwitches)
	pruned := len(edits.Added) != len(p.Edits.Added) || len(edits.Removed) != len(p.Edits.Removed) ||
		len(switches) != len(p.FeedSwitches)
	p.Edits, p.FeedSwitches = edits, switches

	p.Groups = applyEdits(base, p.Edits)
	p.Sources = applySwitches(feeds, p.FeedSwitches)
	return pruned, nil
}

// AddCompany adds a company to a sector. Adding one that was removed from
// Telegram undoes the removal; adding one the sector already follows is an
// error, so the reply can say so.
func (p *Prefs) AddCompany(sector string, c model.Company) error {
	c = tidy(c)
	g := findGroup(p.Groups, sector)
	if g == nil {
		return fmt.Errorf("no sector called %q; try one of: %s", sector, sectorIDs(p.Groups))
	}
	term := firstTerm(c)
	if g.Has(term) {
		return fmt.Errorf("%s already follows %s", g.Name, term)
	}

	undone := false
	kept := p.Edits.Removed[:0]
	for _, r := range p.Edits.Removed {
		if r.Sector == sector && c.Is(r.Company) {
			undone = true
			continue
		}
		kept = append(kept, r)
	}
	p.Edits.Removed = kept
	if !undone {
		p.Edits.Added = append(p.Edits.Added, Addition{Sector: sector, Company: c})
	}
	_, err := p.resolve()
	return err
}

// RemoveCompany takes a company out of a sector, by symbol or name. Removing
// one added from Telegram undoes the addition.
func (p *Prefs) RemoveCompany(sector, term string) error {
	g := findGroup(p.Groups, sector)
	if g == nil {
		return fmt.Errorf("no sector called %q; try one of: %s", sector, sectorIDs(p.Groups))
	}
	if !g.Has(term) {
		return fmt.Errorf("%s does not follow %s", g.Name, term)
	}

	undone := false
	kept := p.Edits.Added[:0]
	for _, a := range p.Edits.Added {
		if a.Sector == sector && a.Company.Is(term) {
			undone = true
			continue
		}
		kept = append(kept, a)
	}
	p.Edits.Added = kept
	if !undone {
		p.Edits.Removed = append(p.Edits.Removed, Removal{Sector: sector, Company: term})
	}
	_, err := p.resolve()
	return err
}

// ResetEdits drops every watchlist change made from Telegram, and says how
// many there were.
func (p *Prefs) ResetEdits() (int, error) {
	n := len(p.Edits.Added) + len(p.Edits.Removed)
	p.Edits = Edits{}
	_, err := p.resolve()
	return n, err
}

// SwitchFeed turns a feed on or off.
func (p *Prefs) SwitchFeed(id string, on bool) (model.Source, error) {
	for _, s := range p.Sources {
		if s.ID != id {
			continue
		}
		if p.FeedSwitches == nil {
			p.FeedSwitches = map[string]bool{}
		}
		p.FeedSwitches[id] = on
		if _, err := p.resolve(); err != nil {
			return model.Source{}, err
		}
		s.Enabled = on
		return s, nil
	}
	return model.Source{}, fmt.Errorf("no source called %q; send /sources to list them", id)
}

// Save writes the preferences atomically. The bot rewrites this file while the
// scheduler may be reading it, and a half-written file would lose the chat.
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

// Group returns the sector with the given ID.
func (p *Prefs) Group(id string) (model.Group, bool) {
	if g := findGroup(p.Groups, id); g != nil {
		return *g, true
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

func sectorIDs(groups []model.Group) string {
	var out string
	for i, g := range groups {
		if i > 0 {
			out += ", "
		}
		out += g.ID
	}
	return out
}
