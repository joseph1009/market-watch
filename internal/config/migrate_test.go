package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/model"
)

// The gap migrations exist to close: MergeDefaults only adds entries absent by
// ID, so an improvement to a watchlist the user already had could never reach
// them. Both of these shipped and neither took effect on a running install.
func TestMigrationExpandsSemisAndMovesTesla(t *testing.T) {
	// A file as it stood before versioning: no schema_version, the old
	// semiconductor list, and Tesla still in Big Tech.
	p := &Prefs{
		Groups: []model.Group{
			{ID: "semis-ai", Name: "Semiconductors & AI",
				Tickers:  []string{"NVDA", "AMD"},
				Names:    []string{"Nvidia"},
				Keywords: []string{"semiconductor"}},
			{ID: "big-tech", Name: "Big Tech",
				Tickers: []string{"AAPL", "TSLA", "NFLX"},
				Names:   []string{"Apple", "Tesla", "Netflix"}},
		},
	}

	applied := p.Migrate()
	if len(applied) != len(migrations) {
		t.Fatalf("applied %v, want every pending migration", applied)
	}

	semis := p.group("semis-ai")
	for _, want := range []string{"QCOM", "AMAT", "KLAC", "GFS"} {
		if !contains(semis.Tickers, want) {
			t.Errorf("Tickers missing %s: %v", want, semis.Tickers)
		}
	}
	for _, want := range []string{"SK Hynix", "Samsung", "Applied Materials"} {
		if !contains(semis.Names, want) {
			t.Errorf("Names missing %q: %v", want, semis.Names)
		}
	}
	for _, want := range []string{"HBM", "CoWoS", "EUV"} {
		if !contains(semis.Keywords, want) {
			t.Errorf("Keywords missing %q: %v", want, semis.Keywords)
		}
	}
	// The originals survive.
	if !contains(semis.Tickers, "NVDA") {
		t.Errorf("an existing ticker was lost: %v", semis.Tickers)
	}

	big := p.group("big-tech")
	if contains(big.Tickers, "TSLA") || contains(big.Names, "Tesla") {
		t.Errorf("Tesla is still in Big Tech: %v / %v", big.Tickers, big.Names)
	}
	// Netflix leaves too, but in migration 6 and on purpose; Apple is what
	// shows that migration 1 took only Tesla.
	if !contains(big.Tickers, "AAPL") || !contains(big.Names, "Apple") {
		t.Errorf("the migration removed more than Tesla: %v / %v", big.Tickers, big.Names)
	}
}

// Running twice must not duplicate terms or re-do work.
func TestMigrationsAreAppliedOnce(t *testing.T) {
	p := &Prefs{Groups: []model.Group{
		{ID: "semis-ai", Name: "Semis", Tickers: []string{"NVDA"}},
	}}

	p.Migrate()
	first := len(p.group("semis-ai").Tickers)

	if applied := p.Migrate(); len(applied) != 0 {
		t.Errorf("second run applied %v, want nothing", applied)
	}
	if got := len(p.group("semis-ai").Tickers); got != first {
		t.Errorf("tickers grew from %d to %d on a second run", first, got)
	}
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
}

// A fresh install is already correct, so migrations meant to fix older files
// must not run against it.
func TestFreshInstallIsStampedCurrent(t *testing.T) {
	p := DefaultPrefs()
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if applied := p.Migrate(); len(applied) != 0 {
		t.Errorf("a fresh install ran %v", applied)
	}
}

// A hand-edited file that already added a term must not end up with it twice.
func TestMigrationDoesNotDuplicateExistingTerms(t *testing.T) {
	p := &Prefs{Groups: []model.Group{
		{ID: "semis-ai", Name: "Semis", Tickers: []string{"NVDA", "QCOM"}, Names: []string{"nvidia"}},
	}}
	p.Migrate()

	if got := count(p.group("semis-ai").Tickers, "QCOM"); got != 1 {
		t.Errorf("QCOM appears %d times", got)
	}
	// Case-insensitive, so a hand-typed "nvidia" is not joined by "Nvidia".
	if got := countFold(p.group("semis-ai").Names, "nvidia"); got != 1 {
		t.Errorf("nvidia appears %d times: %v", got, p.group("semis-ai").Names)
	}
}

// A migration must not fail on a file missing the group it edits: someone may
// have deleted a watchlist, and an upgrade is not the place to discover it.
func TestMigrationToleratesAMissingGroup(t *testing.T) {
	p := &Prefs{Groups: []model.Group{{ID: "nothing-it-touches", Name: "Unrelated"}}}
	if applied := p.Migrate(); len(applied) != len(migrations) {
		t.Errorf("applied %v, want every migration to run and skip what is absent", applied)
	}
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d", p.SchemaVersion)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func count(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}

func countFold(list []string, want string) int {
	n := 0
	for _, v := range list {
		if strings.EqualFold(v, want) {
			n++
		}
	}
	return n
}

// MarketWatch moved hosts and answered the old address with a redirect that
// does not resolve. A stored source keeps the URL it was seeded with, so the
// default alone would have fixed nothing.
func TestMigrationFollowsMarketWatchToItsNewHost(t *testing.T) {
	p := &Prefs{
		SchemaVersion: 2,
		Sources: []model.Source{
			{ID: "marketwatch-top", URL: "https://feeds.marketwatch.com/marketwatch/topstories/"},
			{ID: "cnbc-top", URL: "https://www.cnbc.com/id/100003114/device/rss/rss.html"},
		},
	}

	p.Migrate()

	if got := p.Sources[0].URL; !strings.Contains(got, "feeds.content.dowjones.io") {
		t.Errorf("MarketWatch URL = %q, want the Dow Jones host", got)
	}
	if !strings.Contains(p.Sources[1].URL, "cnbc.com") {
		t.Errorf("an unrelated source was rewritten: %q", p.Sources[1].URL)
	}
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
}

// Tickers say which companies a watchlist follows and nothing about the sector
// around them. The sentence is what the sorting model places articles against,
// so an existing install has to get it too.
func TestMigrationDescribesEachWatchlistsSector(t *testing.T) {
	p := &Prefs{
		SchemaVersion: 3,
		Groups: []model.Group{
			{ID: "energy", Name: "Energy"},
			{ID: "mine", Name: "My own watchlist", Scope: "Only what I said."},
		},
	}

	p.Migrate()

	if got := p.group("energy").Scope; got == "" || !strings.Contains(got, "refining") {
		t.Errorf("energy scope = %q, want the sector described", got)
	}
	if got := p.group("mine").Scope; got != "Only what I said." {
		t.Errorf("a hand-written scope was overwritten: %q", got)
	}
}

// Every watchlist a fresh install ships with has to carry one, since a
// watchlist without a sentence falls back to matching example words.
func TestEveryDefaultWatchlistDescribesItsSector(t *testing.T) {
	for _, g := range DefaultPrefs().Groups {
		if g.Scope == "" {
			t.Errorf("%s has no scope sentence", g.ID)
		}
	}
}

// Forty filings a day, none of them ever cited. Turning it off in the defaults
// does nothing for an install that already stored it.
func TestMigrationStopsTheFilingFirehose(t *testing.T) {
	p := &Prefs{
		SchemaVersion: 3,
		Sources: []model.Source{
			{ID: "sec-8k", Enabled: true},
			{ID: "sec-press", Enabled: true},
		},
	}

	p.Migrate()

	if p.Sources[0].Enabled {
		t.Error("sec-8k is still enabled")
	}
	if !p.Sources[1].Enabled {
		t.Error("the targeted SEC feed was turned off with it")
	}
}

// The three collisions the first sector brief turned up. The fix has to reach an
// install that already stored them, and must take only those terms.
func TestMigrationDropsTermsThatMatchedOrdinaryWords(t *testing.T) {
	p := &Prefs{
		SchemaVersion: 4,
		Groups: []model.Group{
			{ID: "consumer-retail", Tickers: []string{"TGT"}, Names: []string{"Walmart", "Target"}},
			{ID: "industrials-defense", Tickers: []string{"UPS"}, Names: []string{"FedEx", "UPS"}},
			{ID: "financials", Tickers: []string{"JPM", "MS"}, Names: []string{"Morgan Stanley"}},
		},
	}

	p.Migrate()

	if g := p.group("consumer-retail"); contains(g.Names, "Target") || !contains(g.Tickers, "TGT") {
		t.Errorf("consumer-retail = %v / %v, want the name gone and the ticker kept", g.Names, g.Tickers)
	}
	if g := p.group("industrials-defense"); contains(g.Names, "UPS") || !contains(g.Tickers, "UPS") {
		t.Errorf("industrials-defense = %v / %v, want the name gone and the ticker kept", g.Names, g.Tickers)
	}
	if g := p.group("financials"); contains(g.Tickers, "MS") || !contains(g.Names, "Morgan Stanley") {
		t.Errorf("financials = %v / %v, want the ticker gone and the name kept", g.Tickers, g.Names)
	}
	if !contains(p.group("consumer-retail").Names, "Walmart") || !contains(p.group("financials").Tickers, "JPM") {
		t.Error("the migration removed more than the three terms")
	}
}

// Found by the first brief posted to the channel. The fix has to reach an
// install that stored the old sentence and terms, and must leave the rest of
// the watchlist alone.
func TestMigrationTakesNetflixAndTheCatchAllTermsOutOfBigTech(t *testing.T) {
	p := &Prefs{
		SchemaVersion: 5,
		Groups: []model.Group{{
			ID:       "big-tech",
			Scope:    bigTechScopeV4,
			Tickers:  []string{"AAPL", "NFLX"},
			Names:    []string{"Apple", "Netflix"},
			Keywords: []string{"antitrust", "cloud revenue", "Earnings Guidance"},
		}},
	}

	p.Migrate()

	g := p.group("big-tech")
	if g.Scope != bigTechScopeV6 {
		t.Errorf("Scope = %q, want the new sentence", g.Scope)
	}
	if contains(g.Tickers, "NFLX") || contains(g.Names, "Netflix") {
		t.Errorf("big-tech = %v / %v, want Netflix gone to Media", g.Tickers, g.Names)
	}
	if contains(g.Keywords, "antitrust") || contains(g.Keywords, "earnings guidance") {
		t.Errorf("Keywords = %v, want the two catch-all terms gone", g.Keywords)
	}
	if !contains(g.Keywords, "cloud revenue") || !contains(g.Tickers, "AAPL") || !contains(g.Names, "Apple") {
		t.Errorf("big-tech = %+v, want everything else kept", g)
	}
}

// Loading an existing file is the path that matters: the migration moves
// Netflix out, and the merge that follows brings the new watchlist in with
// Netflix in it, so no install is left with Netflix tracked nowhere.
func TestAnUpgradedInstallGetsTheMediaWatchlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.yaml")
	old := DefaultPrefs()
	old.SchemaVersion = 5
	var kept []model.Group
	for _, g := range old.Groups {
		switch g.ID {
		case "media":
			continue // did not exist yet
		case "big-tech":
			g.Scope = bigTechScopeV4
			g.Tickers = append(g.Tickers, "NFLX")
			g.Names = append(g.Names, "Netflix")
			g.Keywords = []string{"antitrust", "cloud revenue", "earnings guidance"}
		}
		kept = append(kept, g)
	}
	old.Groups = kept
	if err := old.Save(path); err != nil {
		t.Fatal(err)
	}

	p, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}

	media := p.group("media")
	if media == nil || !contains(media.Tickers, "NFLX") || !contains(media.Names, "Netflix") {
		t.Fatalf("media = %+v, want it added with Netflix in it", media)
	}
	if g := p.group("big-tech"); contains(g.Tickers, "NFLX") || g.Scope != bigTechScopeV6 {
		t.Errorf("big-tech = %+v, want Netflix out and the new sentence", g)
	}
}

// A sentence that is not the one migration 4 wrote is not this migration's to
// replace.
func TestMigrationLeavesAnotherBigTechSentenceAlone(t *testing.T) {
	p := &Prefs{
		SchemaVersion: 5,
		Groups:        []model.Group{{ID: "big-tech", Scope: "Only the six giants."}},
	}

	p.Migrate()

	if got := p.group("big-tech").Scope; got != "Only the six giants." {
		t.Errorf("Scope = %q, want it untouched", got)
	}
}

// A fresh install and an upgraded one must end up describing Big Tech the
// same way.
func TestTheDefaultBigTechSentenceIsTheMigratedOne(t *testing.T) {
	for _, g := range DefaultPrefs().Groups {
		if g.ID == "big-tech" && g.Scope != bigTechScopeV6 {
			t.Errorf("default Scope = %q, want the one migration 6 writes", g.Scope)
		}
	}
}
