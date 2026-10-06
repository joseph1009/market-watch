package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/model"
)

// The files hold what the service followed when they replaced the lists in Go
// and the copy on the volume (2026-09-24): twelve sectors in this order, 95
// tickers, and 49 feeds.
func TestTheFilesHoldTheWatchlistAndFeeds(t *testing.T) {
	groups, err := Watchlist()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	symbols := 0
	for _, g := range groups {
		ids = append(ids, g.ID)
		symbols += len(g.Symbols())
	}
	want := "semis-ai big-tech energy financials healthcare industrials-defense consumer-retail autos-ev crypto macro-rates geopolitics-trade media"
	if got := strings.Join(ids, " "); got != want {
		t.Errorf("sectors = %s\nwant       %s", got, want)
	}
	if symbols != 98 {
		t.Errorf("%d tickers, want 98", symbols)
	}

	feeds, err := Feeds()
	if err != nil {
		t.Fatal(err)
	}
	on := 0
	for _, f := range feeds {
		if f.Enabled {
			on++
		}
	}
	if len(feeds) != 49 || on != 41 {
		t.Errorf("%d feeds, %d on; want 49 and 41", len(feeds), on)
	}
}

// The first sentence of a sector's description is what the news search asks
// for, so it has to stand on its own.
func TestEverySectorOpensWithASentenceASearchCanUse(t *testing.T) {
	groups, _ := Watchlist()
	for _, g := range groups {
		first := strings.SplitN(g.About, ". ", 2)[0]
		if len(first) < 60 || len(first) > 330 {
			t.Errorf("%s opens with a sentence of %d characters: %q", g.ID, len(first), first)
		}
	}
}

// The words that match ordinary prose are kept from matching: the reasons are
// in companies.yaml, and each cost a night of misfiled articles to find.
func TestOrdinaryWordsAreNotMatchedAsNames(t *testing.T) {
	groups, _ := Watchlist()
	names := map[string]bool{}
	for _, g := range groups {
		for _, n := range g.MatchNames() {
			names[strings.ToLower(n)] = true
		}
	}
	for _, word := range []string{"target", "ups", "arm", "paramount"} {
		if names[word] {
			t.Errorf("%q is matched as a name; it matches ordinary prose", word)
		}
	}
}

func TestAWatchlistFileWithAnUnknownSectorIsRefused(t *testing.T) {
	_, err := parseWatchlist(sectorsFile, []byte("no-such-sector:\n  - {symbol: X, name: X}\n"))
	if err == nil || !strings.Contains(err.Error(), "no-such-sector") {
		t.Errorf("err = %v, want the unknown sector named", err)
	}
}

// A file written before the lists moved out of it held the whole watchlist and
// feed list. It still loads: the chat is kept, the lists come from the files,
// and the old copies go at the next save.
func TestAPrefsFileFromBeforeLoadsAndLosesItsLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.yaml")
	old := `schema_version: 6
chat_id: 4242
last_brief: [7, 8]
groups:
  - id: semis-ai
    name: Semiconductors & AI
    tickers: [NVDA]
    keywords: [semiconductor]
sources:
  - id: cnbc-top
    name: CNBC Top News
    url: https://example.com
    weight: 8
    enabled: false
`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadPrefs(path)
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}
	if p.ChatID != 4242 || len(p.LastBrief) != 2 {
		t.Errorf("ChatID=%d LastBrief=%v, want the old values kept", p.ChatID, p.LastBrief)
	}
	if g, _ := p.Group("semis-ai"); len(g.Companies) < 10 {
		t.Errorf("semis-ai has %d companies, want the file's list", len(g.Companies))
	}
	for _, s := range p.Sources {
		if s.ID == "cnbc-top" && !s.Enabled {
			t.Error("the old file's feed list was used; the files decide")
		}
	}

	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	if strings.Contains(string(saved), "groups:") || strings.Contains(string(saved), "sources:") {
		t.Errorf("the old lists survived a save:\n%s", saved)
	}
}

// Changes made from Telegram sit on top of the files, survive a restart, and
// leave the files' own lists alone.
func TestEditsApplyOnTopOfTheFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.yaml")
	p, _ := LoadPrefs(path)

	if err := p.AddCompany("industrials-defense", model.Company{Symbol: "pltr", Name: "Palantir"}); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveCompany("semis-ai", "INTC"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SwitchFeed("marketwatch-top", false); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}

	again, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := again.Group("industrials-defense"); !g.Has("PLTR") {
		t.Error("the added company did not survive a restart")
	}
	if g, _ := again.Group("semis-ai"); g.Has("INTC") {
		t.Error("the removed company came back")
	}
	for _, s := range again.EnabledSources() {
		if s.ID == "marketwatch-top" {
			t.Error("the feed switched off came back on")
		}
	}
	base, _ := Watchlist()
	if !findGroup(base, "semis-ai").Has("INTC") {
		t.Error("an edit changed the files' own list")
	}
}

// Adding back a company removed from Telegram undoes the removal rather than
// recording both.
func TestAddingBackWhatWasRemovedUndoesIt(t *testing.T) {
	p := DefaultPrefs()
	if err := p.RemoveCompany("semis-ai", "Intel"); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCompany("semis-ai", model.Company{Symbol: "INTC", Name: "Intel"}); err != nil {
		t.Fatal(err)
	}
	if !p.Edits.IsZero() {
		t.Errorf("edits = %+v, want none: the two cancel", p.Edits)
	}
	if err := p.AddCompany("semis-ai", model.Company{Symbol: "NVDA", Name: "Nvidia"}); err == nil {
		t.Error("adding a company already followed was accepted")
	}
	if err := p.RemoveCompany("nowhere", "NVDA"); err == nil || !strings.Contains(err.Error(), "semis-ai") {
		t.Errorf("err = %v, want the sectors listed", err)
	}
}

// Once the files say what an edit said -- after a sync and a deploy -- the
// edit is dropped from the volume, so the edits on Fly are only ever what the
// files do not yet say.
func TestAnEditTheFilesAlreadySayIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.yaml")
	stale := `chat_id: 4242
watchlist_edits:
  added:
    - {sector: semis-ai, symbol: NVDA, name: Nvidia}
    - {sector: industrials-defense, symbol: PLTR, name: Palantir}
  removed:
    - {sector: semis-ai, company: NotListed}
feed_switches:
  cnbc-top: true
  marketwatch-top: false
`
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Edits.Added) != 1 || p.Edits.Added[0].Symbol != "PLTR" || len(p.Edits.Removed) != 0 {
		t.Errorf("edits = %+v, want only Palantir left", p.Edits)
	}
	if len(p.FeedSwitches) != 1 || p.FeedSwitches["marketwatch-top"] {
		t.Errorf("switches = %v, want only marketwatch-top off", p.FeedSwitches)
	}
	saved, _ := os.ReadFile(path)
	if strings.Contains(string(saved), "NotListed") {
		t.Error("the dropped edits were not saved away")
	}
}

// The sync writes the edits into the files: one line added, one line and its
// comment taken out, one word switched, and nothing else touched.
func TestFoldChangesOnlyTheLinesEdited(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{CompaniesFile, SourcesFile} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultPrefs()
	_ = p.AddCompany("industrials-defense", model.Company{Symbol: "PLTR", Name: "Palantir"})
	_ = p.AddCompany("macro-rates", model.Company{Name: "Federal Reserve"})
	_ = p.RemoveCompany("consumer-retail", "TGT")
	_, _ = p.SwitchFeed("marketwatch-top", false)

	done, err := Fold(dir, *p)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 4 {
		t.Errorf("changes = %q, want four", done)
	}

	before, _ := os.ReadFile(CompaniesFile)
	after, _ := os.ReadFile(filepath.Join(dir, CompaniesFile))
	added, removed := lineDiff(string(before), string(after))
	wantAdded := "  - {symbol: PLTR, name: Palantir}|macro-rates:|  - {name: Federal Reserve}"
	if strings.Join(added, "|") != wantAdded {
		t.Errorf("added lines = %q", added)
	}
	if len(removed) != 4 || !strings.Contains(strings.Join(removed, "\n"), "TGT") || removed[3] != "macro-rates: []" {
		t.Errorf("removed lines = %q, want Target, its comment, and the empty macro list", removed)
	}
	groups, err := parseWatchlist(sectorsFile, after)
	if err != nil {
		t.Fatalf("the folded file does not parse: %v", err)
	}
	if !findGroup(groups, "industrials-defense").Has("PLTR") || findGroup(groups, "consumer-retail").Has("TGT") {
		t.Error("the folded file does not say what the edits said")
	}

	before, _ = os.ReadFile(SourcesFile)
	after, _ = os.ReadFile(filepath.Join(dir, SourcesFile))
	added, removed = lineDiff(string(before), string(after))
	if len(added) != 1 || added[0] != "  enabled: false" || len(removed) != 1 {
		t.Errorf("sources changed %q for %q, want one switch", removed, added)
	}
}

// lineDiff is the lines only in b and only in a, in order.
func lineDiff(a, b string) (added, removed []string) {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[l]++
		}
		return m
	}
	ca, cb := count(a), count(b)
	for _, l := range strings.Split(b, "\n") {
		if cb[l] > ca[l] {
			added = append(added, l)
			cb[l]--
		}
	}
	for _, l := range strings.Split(a, "\n") {
		if ca[l] > count(b)[l] {
			removed = append(removed, l)
			ca[l]--
		}
	}
	return added, removed
}
