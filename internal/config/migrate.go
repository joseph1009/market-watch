package config

import (
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// CurrentSchemaVersion is the newest migration below. A file written by
// DefaultPrefs is stamped with it, so a fresh install never runs migrations
// that only exist to correct older files.
const CurrentSchemaVersion = 1

// migration is a one-time correction to an existing preferences file.
//
// MergeDefaults covers what can be added safely -- a source or watchlist absent
// by ID -- but it deliberately never touches an entry that already exists, so
// an edit to a watchlist the user already has could not reach them. That gap
// was not theoretical: the semiconductor expansion and the move of Tesla out of
// Big Tech both shipped and neither took effect on a running install.
//
// Migrations close it. Each is applied at most once, recorded by version, and
// states its change explicitly rather than syncing from the defaults -- because
// the defaults keep moving, and a migration has to mean the same thing in a
// year as it does today.
type migration struct {
	version int
	name    string
	apply   func(*Prefs)
}

var migrations = []migration{
	{
		version: 1,
		name:    "expand semiconductor coverage and move Tesla to Autos & EV",
		apply: func(p *Prefs) {
			if g := p.group("semis-ai"); g != nil {
				g.Tickers = addMissing(g.Tickers,
					"QCOM", "MRVL", "AMAT", "LRCX", "KLAC", "GFS", "SMCI", "ANET")
				g.Names = addMissing(g.Names,
					"Advanced Micro Devices", "Marvell", "Applied Materials",
					"Lam Research", "KLA", "Supermicro", "Arista", "SK Hynix", "Samsung")
				g.Keywords = addMissing(g.Keywords,
					"HBM", "CoWoS", "advanced packaging", "EUV", "semiconductor equipment",
					"AI accelerator", "custom silicon", "wafer", "wafer yield", "chip capacity")
			}
			// Tesla matched both Big Tech and Autos, so every Tesla story was
			// told twice. Autos is the more specific home.
			if g := p.group("big-tech"); g != nil {
				g.Tickers = removeFold(g.Tickers, "TSLA")
				g.Names = removeFold(g.Names, "Tesla")
			}
		},
	},
}

// Migrate applies every migration newer than the file's recorded version and
// returns what it did, so the run can say so out loud.
func (p *Prefs) Migrate() []string {
	var applied []string
	for _, m := range migrations {
		if m.version <= p.SchemaVersion {
			continue
		}
		m.apply(p)
		p.SchemaVersion = m.version
		applied = append(applied, m.name)
	}

	// A file that predates versioning reports 0 and is brought up to date by
	// the loop above; one already current is stamped anyway, so a later reader
	// can tell the difference between "migrated" and "never seen".
	if p.SchemaVersion < CurrentSchemaVersion {
		p.SchemaVersion = CurrentSchemaVersion
	}
	return applied
}

// group returns a pointer so a migration can edit in place.
func (p *Prefs) group(id string) *model.Group {
	for i := range p.Groups {
		if p.Groups[i].ID == id {
			return &p.Groups[i]
		}
	}
	return nil
}

// addMissing appends the terms not already present, comparing case-insensitively
// so a hand-edited "nvidia" is not duplicated as "Nvidia".
func addMissing(list []string, terms ...string) []string {
	for _, term := range terms {
		found := false
		for _, existing := range list {
			if strings.EqualFold(existing, term) {
				found = true
				break
			}
		}
		if !found {
			list = append(list, term)
		}
	}
	return list
}

func removeFold(list []string, term string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !strings.EqualFold(v, term) {
			out = append(out, v)
		}
	}
	return out
}
