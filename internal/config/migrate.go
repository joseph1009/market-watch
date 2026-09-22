package config

import (
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// CurrentSchemaVersion is the newest migration below. A file written by
// DefaultPrefs is stamped with it, so a fresh install never runs migrations
// that only exist to correct older files.
const CurrentSchemaVersion = 6

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
	{
		version: 2,
		name:    "deepen sector coverage and narrow keywords to catalysts",
		apply: func(p *Prefs) {
			// Five symbols are tracked by name only, because as bare words they
			// match things that are not the company: MPC is the Bank of
			// England's Monetary Policy Committee, EMR an electronic medical
			// record, ALL an ordinary word in an all-caps headline, URI a web
			// address, MA a US state, CB and LNG both abbreviations in wider
			// use. LNG is additionally already an Energy keyword.
			if g := p.group("energy"); g != nil {
				g.Tickers = addMissing(g.Tickers, "EQT", "VLO", "PSX")
				g.Names = addMissing(g.Names,
					"EQT Corporation", "Valero", "Phillips 66",
					"Marathon Petroleum", "Cheniere")
				g.Keywords = addMissing(g.Keywords,
					"OPEC+", "SPR", "crack spread", "refinery utilization",
					"LNG exports", "production cuts")
			}
			if g := p.group("financials"); g != nil {
				g.Tickers = addMissing(g.Tickers, "PGR", "AIG", "PYPL", "BX", "KKR", "APO")
				g.Names = addMissing(g.Names,
					"Progressive", "PayPal", "Blackstone", "Apollo Global",
					"Allstate", "Mastercard", "Chubb")
				g.Keywords = addMissing(g.Keywords,
					"deposit flight", "NIM", "credit losses", "private credit", "bank stress")
			}
			if g := p.group("healthcare"); g != nil {
				g.Tickers = addMissing(g.Tickers,
					"AMGN", "GILD", "REGN", "BIIB", "VRTX", "ELV", "HUM")
				g.Names = addMissing(g.Names,
					"Amgen", "Gilead", "Regeneron", "Biogen", "Vertex Pharmaceuticals",
					"Elevance", "Humana")
				g.Keywords = addMissing(g.Keywords,
					"PDUFA", "Phase 2", "patent expiry", "drug recall")
				// "recall" alone matched every product recall in every sector.
				g.Keywords = removeFold(g.Keywords, "recall")
			}
			if g := p.group("industrials-defense"); g != nil {
				g.Tickers = addMissing(g.Tickers, "ETN", "CMI")
				g.Names = addMissing(g.Names,
					"Eaton", "Cummins", "Emerson", "United Rentals")
				g.Keywords = addMissing(g.Keywords,
					"industrial capex", "capacity expansion", "backlog",
					"infrastructure spending")
			}
			if g := p.group("consumer-retail"); g != nil {
				g.Keywords = addMissing(g.Keywords, "consumer pricing", "price increases")
			}
		},
	},
	{
		version: 3,
		name:    "follow MarketWatch to its new feed host",
		apply: func(p *Prefs) {
			// MarketWatch moved its feeds to Dow Jones and answers the old
			// address with a redirect that does not resolve -- "host not
			// found", reaching us as HTTP 400. A stored source keeps the URL it
			// was seeded with, so changing the default alone would have fixed
			// nothing for an existing install.
			for i, s := range p.Sources {
				if s.ID == "marketwatch-top" &&
					strings.Contains(s.URL, "feeds.marketwatch.com") {
					p.Sources[i].URL = "https://feeds.content.dowjones.io/public/rss/mw_topstories"
				}
			}
		},
	},
	{
		version: 4,
		name:    "describe each watchlist as a sector, and stop the 8-K firehose",
		apply: func(p *Prefs) {
			// Scope is the exception to the rule above about not syncing from
			// the defaults. That rule protects lists of terms, where taking
			// today's version would silently undo the reader's own edits. A
			// scope is one sentence describing a sector, it has no prior
			// value to overwrite, and if the wording improves later the newer
			// sentence is the one an upgrading install should get.
			for _, base := range basePrefs().Groups {
				if g := p.group(base.ID); g != nil && g.Scope == "" {
					g.Scope = base.Scope
				}
			}

			// See defaults.go for why: forty filings a day, none of them ever
			// cited. Disabled rather than removed, so it can be turned back on.
			for i, s := range p.Sources {
				if s.ID == "sec-8k" {
					p.Sources[i].Enabled = false
				}
			}
		},
	},
	{
		version: 5,
		name:    "drop three terms that matched ordinary words",
		apply: func(p *Prefs) {
			// Found by the first brief written against the sector sentences.
			// Every article placed by judgment that night held up; every one
			// misfiled came in on one of these. With the sector sentence doing
			// the wide catching, a term that is also an ordinary word now
			// costs more than it adds. See defaults.go for each.
			if g := p.group("consumer-retail"); g != nil {
				g.Names = removeFold(g.Names, "Target")
			}
			if g := p.group("industrials-defense"); g != nil {
				g.Names = removeFold(g.Names, "UPS")
			}
			if g := p.group("financials"); g != nil {
				g.Tickers = removeFold(g.Tickers, "MS")
			}
		},
	},
	{
		version: 6,
		name:    "give film and TV a watchlist of their own, and move Netflix to it",
		apply: func(p *Prefs) {
			// Found by the first brief posted to the channel: three stories on
			// Paramount's merger with Warner Bros, and one on British
			// broadcasters, were sorted into Big Tech. The sorting is told to
			// file a company beside its rivals, Netflix was in Big Tech, and
			// every studio competes with Netflix. Saying "not media" in Big
			// Tech's sentence did not move them; a watchlist for media did.
			//
			// Netflix goes the way Tesla went in migration 1, to the more
			// specific home. The Media & Entertainment watchlist itself is new,
			// so MergeDefaults adds it after this runs. The two terms that
			// matched any company's news go too.
			//
			// Only the sentence migration 4 wrote is replaced. An install that
			// reached migration 4 after this change already has the new one,
			// and nothing else should be overwritten.
			if g := p.group("big-tech"); g != nil {
				if g.Scope == bigTechScopeV4 {
					g.Scope = bigTechScopeV6
				}
				g.Tickers = removeFold(g.Tickers, "NFLX")
				g.Names = removeFold(g.Names, "Netflix")
				g.Keywords = removeFold(g.Keywords, "antitrust")
				g.Keywords = removeFold(g.Keywords, "earnings guidance")
			}
		},
	},
}

// The Big Tech sentence as migration 4 wrote it and as migration 6 rewrites it.
// Spelled out, so the migration means the same whatever the defaults say later.
const (
	bigTechScopeV4 = "The largest software and internet companies: cloud, advertising, devices, app stores and streaming, and the competition cases and regulation aimed at them."
	bigTechScopeV6 = "The largest software and internet companies: cloud, advertising, AI models and assistants, devices and app stores, and the competition cases and regulation aimed at them."
)

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
