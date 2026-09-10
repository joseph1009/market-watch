package config

import (
	"path/filepath"
	"testing"

	"github.com/joseph1009/market-watch/internal/model"
)

// New sources and watchlists have to reach an existing install. The prefs file
// is written on first run, so without this they would only ever appear on a
// machine that had never run the service.
func TestLoadPrefsAddsNewDefaultsToAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.yaml")

	// An install from before the sector watchlists existed.
	old := &Prefs{
		ChatID:  4242,
		Groups:  []model.Group{{ID: "semis-ai", Name: "Semiconductors & AI"}},
		Sources: []model.Source{{ID: "cnbc-top", Name: "CNBC Top News", Weight: 8, Enabled: true}},
	}
	if err := old.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadPrefs(path)
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}

	if got.ChatID != 4242 {
		t.Errorf("ChatID = %d, want the existing 4242 preserved", got.ChatID)
	}
	if _, ok := got.Group("energy"); !ok {
		t.Error("the energy watchlist was not added")
	}
	var haveEIA bool
	for _, s := range got.Sources {
		if s.ID == "eia-today" {
			haveEIA = true
		}
	}
	if !haveEIA {
		t.Error("the EIA source was not added")
	}

	// And it has to survive, or every run re-adds and re-saves.
	reloaded, err := LoadPrefs(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Sources) != len(got.Sources) {
		t.Errorf("sources changed on reload: %d then %d", len(got.Sources), len(reloaded.Sources))
	}
}

// A source the user switched off must not come back on.
func TestMergeDefaultsRespectsExistingSettings(t *testing.T) {
	p := &Prefs{
		Sources: []model.Source{{ID: "cnbc-top", Name: "CNBC Top News", Weight: 2, Enabled: false}},
	}
	p.MergeDefaults()

	for _, s := range p.Sources {
		if s.ID != "cnbc-top" {
			continue
		}
		if s.Enabled {
			t.Error("a disabled source was re-enabled by the merge")
		}
		if s.Weight != 2 {
			t.Errorf("Weight = %d, want the user's 2 kept", s.Weight)
		}
	}
}

func TestMergeDefaultsReportsWhatItAdded(t *testing.T) {
	p := &Prefs{}
	added := p.MergeDefaults()
	if len(added) == 0 {
		t.Fatal("nothing was added to an empty prefs file")
	}
	if len(p.Groups) != len(DefaultPrefs().Groups) {
		t.Errorf("got %d groups, want all %d defaults", len(p.Groups), len(DefaultPrefs().Groups))
	}
}
