package config

import (
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/joseph1009/market-watch/internal/model"
)

// The watchlist and the feed list are files in this directory, compiled into
// the binary: sectors.yaml says what each section covers, companies.yaml lists
// the companies followed in each, and sources.yaml the feeds.
//
// They used to be a copy on the data volume, seeded from lists in Go and kept
// current by migrations: a change to the lists reached a running install only
// if someone also wrote a migration for it, and when nobody did, the two drifted
// apart without a word. Now the files are the lists. What is kept on the volume
// is only what was changed from Telegram (Edits, FeedSwitches in prefs.go),
// applied on top at load, and an edit the files already say is dropped then.
var (
	//go:embed sectors.yaml
	sectorsFile []byte
	//go:embed companies.yaml
	companiesFile []byte
	//go:embed sources.yaml
	sourcesFile []byte
)

// Files the lists are kept in, for the sync that writes Telegram edits back.
const (
	CompaniesFile = "companies.yaml"
	SourcesFile   = "sources.yaml"
)

// Watchlist is the sectors in section order, each with its companies, as the
// files have them.
func Watchlist() ([]model.Group, error) {
	return parseWatchlist(sectorsFile, companiesFile)
}

// Feeds is the feed list as sources.yaml has it.
func Feeds() ([]model.Source, error) {
	var feeds []model.Source
	if err := yaml.Unmarshal(sourcesFile, &feeds); err != nil {
		return nil, fmt.Errorf("%s: %w", SourcesFile, err)
	}
	seen := map[string]bool{}
	for i, f := range feeds {
		if f.ID == "" || f.URL == "" {
			return nil, fmt.Errorf("%s: entry %d needs an id and a url", SourcesFile, i+1)
		}
		if seen[f.ID] {
			return nil, fmt.Errorf("%s: %s is listed twice", SourcesFile, f.ID)
		}
		seen[f.ID] = true
		if f.Name == "" {
			feeds[i].Name = f.ID
		}
		if f.Weight == 0 {
			feeds[i].Weight = 5
		}
	}
	return feeds, nil
}

func parseWatchlist(sectors, companies []byte) ([]model.Group, error) {
	var groups []model.Group
	if err := yaml.Unmarshal(sectors, &groups); err != nil {
		return nil, fmt.Errorf("sectors.yaml: %w", err)
	}
	var bySector map[string][]model.Company
	if err := yaml.Unmarshal(companies, &bySector); err != nil {
		return nil, fmt.Errorf("%s: %w", CompaniesFile, err)
	}

	index := map[string]int{}
	for i, g := range groups {
		if g.ID == "" || g.Name == "" || strings.TrimSpace(g.About) == "" {
			return nil, fmt.Errorf("sectors.yaml: entry %d needs an id, a name and an about", i+1)
		}
		if _, dup := index[g.ID]; dup {
			return nil, fmt.Errorf("sectors.yaml: %s is listed twice", g.ID)
		}
		index[g.ID] = i
	}

	symbols := map[string]string{}
	for sector, list := range bySector {
		i, ok := index[sector]
		if !ok {
			return nil, fmt.Errorf("%s: %s is not a sector in sectors.yaml", CompaniesFile, sector)
		}
		for _, c := range list {
			c = tidy(c)
			if c.Name == "" {
				return nil, fmt.Errorf("%s: a company in %s has no name", CompaniesFile, sector)
			}
			if c.Match != "" && c.Match != model.MatchTicker && c.Match != model.MatchName {
				return nil, fmt.Errorf("%s: %s has match %q; it can be %q or %q",
					CompaniesFile, c.Name, c.Match, model.MatchTicker, model.MatchName)
			}
			if c.Symbol != "" {
				if other, dup := symbols[c.Symbol]; dup {
					return nil, fmt.Errorf("%s: %s is in both %s and %s", CompaniesFile, c.Symbol, other, sector)
				}
				symbols[c.Symbol] = sector
			}
			groups[i].Companies = append(groups[i].Companies, c)
		}
	}
	return groups, nil
}

// tidy writes a company the way the rest of the code expects it: the symbol in
// capitals, no stray spaces.
func tidy(c model.Company) model.Company {
	c.Symbol = strings.ToUpper(strings.TrimSpace(c.Symbol))
	c.Name = strings.TrimSpace(c.Name)
	c.Match = strings.ToLower(strings.TrimSpace(c.Match))
	return c
}

// Edits are the watchlist changes made from Telegram, kept on the data volume
// and applied on top of companies.yaml.
type Edits struct {
	Added   []Addition `yaml:"added,omitempty"`
	Removed []Removal  `yaml:"removed,omitempty"`
}

// Addition is a company added to a sector with /watchlist add.
type Addition struct {
	Sector        string `yaml:"sector"`
	model.Company `yaml:",inline"`
}

// Removal is a company taken out of a sector with /watchlist remove, by the
// symbol or name it was removed by.
type Removal struct {
	Sector  string `yaml:"sector"`
	Company string `yaml:"company"`
}

// IsZero reports whether there are no edits, which keeps an empty set out of
// the saved file.
func (e Edits) IsZero() bool { return len(e.Added) == 0 && len(e.Removed) == 0 }

// applyEdits returns the watchlist with the edits made. The base is not
// changed: its slices are shared with every earlier caller.
func applyEdits(base []model.Group, e Edits) []model.Group {
	out := make([]model.Group, len(base))
	for i, g := range base {
		g.Companies = append([]model.Company(nil), g.Companies...)
		out[i] = g
	}
	for _, r := range e.Removed {
		if g := findGroup(out, r.Sector); g != nil {
			kept := g.Companies[:0]
			for _, c := range g.Companies {
				if !c.Is(r.Company) {
					kept = append(kept, c)
				}
			}
			g.Companies = kept
		}
	}
	for _, a := range e.Added {
		if g := findGroup(out, a.Sector); g != nil && !g.Has(firstTerm(a.Company)) {
			g.Companies = append(g.Companies, tidy(a.Company))
		}
	}
	return out
}

// pruneEdits drops the edits the base already says -- a company added that is
// now in the file, one removed that no longer is -- and any for a sector that
// has gone. This is how an edit leaves the volume once the sync has written it
// into companies.yaml and that file is deployed.
func pruneEdits(base []model.Group, e Edits) Edits {
	var out Edits
	for _, a := range e.Added {
		if g := findGroup(base, a.Sector); g != nil && !g.Has(firstTerm(a.Company)) {
			out.Added = append(out.Added, a)
		}
	}
	for _, r := range e.Removed {
		if g := findGroup(base, r.Sector); g != nil && g.Has(r.Company) {
			out.Removed = append(out.Removed, r)
		}
	}
	return out
}

// firstTerm is what identifies a company in an edit: its symbol, or its name
// where it has none.
func firstTerm(c model.Company) string {
	if s := strings.TrimSpace(c.Symbol); s != "" {
		return s
	}
	return c.Name
}

func findGroup(groups []model.Group, id string) *model.Group {
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i]
		}
	}
	return nil
}

// applySwitches returns the feed list with the switches made from Telegram.
func applySwitches(base []model.Source, switches map[string]bool) []model.Source {
	out := append([]model.Source(nil), base...)
	for i, s := range out {
		if on, ok := switches[s.ID]; ok {
			out[i].Enabled = on
		}
	}
	return out
}

// pruneSwitches drops a switch that sources.yaml already says, or for a feed
// that is no longer listed.
func pruneSwitches(base []model.Source, switches map[string]bool) map[string]bool {
	var out map[string]bool
	for _, s := range base {
		if on, ok := switches[s.ID]; ok && on != s.Enabled {
			if out == nil {
				out = map[string]bool{}
			}
			out[s.ID] = on
		}
	}
	return out
}
