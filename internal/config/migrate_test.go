package config

import (
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
	if len(applied) != 1 {
		t.Fatalf("applied %v, want one migration", applied)
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
	if !contains(big.Tickers, "AAPL") || !contains(big.Names, "Netflix") {
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

// A migration must not fail on a file missing the group it edits.
func TestMigrationToleratesAMissingGroup(t *testing.T) {
	p := &Prefs{Groups: []model.Group{{ID: "energy", Name: "Energy"}}}
	if applied := p.Migrate(); len(applied) != 1 {
		t.Errorf("applied %v, want the migration to run and do nothing", applied)
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
