package feed

import (
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func testSources() []model.Source {
	return []model.Source{
		{ID: "sec-8k", Name: "SEC EDGAR 8-K", Weight: 10, Enabled: true},
		{ID: "cnbc", Name: "CNBC", Weight: 8, Enabled: true},
		{ID: "aggregator", Name: "Aggregator", Weight: 4, Enabled: true},
	}
}

func TestDedupeKeepsTheHighestWeightedSource(t *testing.T) {
	url := "https://example.com/markets/story-1"
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	// The same story, reaching us from three feeds with different tracking
	// parameters. Only the SEC copy should survive.
	articles := []model.Article{
		{ID: model.ArticleID(url + "?utm_source=rss"), URL: url, SourceID: "aggregator", Published: base},
		{ID: model.ArticleID(url), URL: url, SourceID: "sec-8k", Published: base},
		{ID: model.ArticleID("http://www.example.com/markets/story-1/"), URL: url, SourceID: "cnbc", Published: base},
	}

	got := Dedupe(articles, testSources())
	if len(got) != 1 {
		t.Fatalf("got %d articles, want 1", len(got))
	}
	if got[0].SourceID != "sec-8k" {
		t.Errorf("kept %q, want the highest-weighted source sec-8k", got[0].SourceID)
	}
}

func TestDedupeBreaksWeightTiesOnTheEarliestPublication(t *testing.T) {
	url := "https://example.com/story"
	early := time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)

	articles := []model.Article{
		{ID: model.ArticleID(url), SourceID: "cnbc", Published: early.Add(2 * time.Hour)},
		{ID: model.ArticleID(url), SourceID: "cnbc", Published: early},
	}

	got := Dedupe(articles, testSources())
	if len(got) != 1 {
		t.Fatalf("got %d articles, want 1", len(got))
	}
	if !got[0].Published.Equal(early) {
		t.Errorf("Published = %s, want the earliest copy %s", got[0].Published, early)
	}
}

func TestDedupeLeavesDistinctStoriesAlone(t *testing.T) {
	articles := []model.Article{
		{ID: model.ArticleID("https://example.com/a"), SourceID: "cnbc"},
		{ID: model.ArticleID("https://example.com/b"), SourceID: "cnbc"},
	}
	if got := Dedupe(articles, testSources()); len(got) != 2 {
		t.Errorf("got %d articles, want 2", len(got))
	}
}

func testGroups() []model.Group {
	return []model.Group{
		{ID: "semis-ai", Name: "Semiconductors & AI", Tickers: []string{"NVDA", "ARM"},
			Names: []string{"Nvidia"}, Keywords: []string{"AI chip", "foundry"}},
		{ID: "macro-rates", Name: "Macro & Rates", Keywords: []string{"CPI", "rate cut", "Federal Reserve"}},
	}
}

// Headlines name the company and quote the symbol. Matching only symbols left
// whole watchlists empty against real feeds, so names carry equal weight.
func TestMatchFindsCompaniesByNameNotJustTicker(t *testing.T) {
	articles := []model.Article{
		{Title: "Nvidia beats on data center revenue"},
		{Title: "nvidia extends its rally", Summary: "Shares rose again."},
		{Title: "Analysts raise targets", Summary: "Nvidia's guidance impressed."},
	}
	for _, a := range Match(articles, testGroups()) {
		if !a.InGroup("semis-ai") {
			t.Errorf("%q did not match semis-ai by name", a.Title)
		}
	}
}

// Name matching is case-insensitive, which is exactly why it must still respect
// word boundaries -- otherwise "Intel" tags every story mentioning intelligence.
func TestMatchNamesRespectWordBoundaries(t *testing.T) {
	groups := []model.Group{{ID: "semis-ai", Name: "Semis", Names: []string{"Intel"}}}
	for _, title := range []string{
		"Artificial intelligence spending accelerates",
		"Intelligence agencies warn on cyber risk",
	} {
		got := Match([]model.Article{{Title: title}}, groups)
		if len(got[0].GroupIDs) != 0 {
			t.Errorf("%q matched %v, want no match", title, got[0].GroupIDs)
		}
	}

	got := Match([]model.Article{{Title: "Intel ships a new foundry process"}}, groups)
	if !got[0].InGroup("semis-ai") {
		t.Error("the real company name did not match")
	}
}

// A name match is a group match, but it is not a ticker sighting -- only an
// actual symbol should populate Tickers.
func TestMatchDoesNotInventTickersFromNames(t *testing.T) {
	got := Match([]model.Article{{Title: "Nvidia extends its rally"}}, testGroups())
	if !got[0].InGroup("semis-ai") {
		t.Fatal("name did not match the group")
	}
	if len(got[0].Tickers) != 0 {
		t.Errorf("Tickers = %v, want none from a name-only match", got[0].Tickers)
	}
}

func TestMatchTagsArticlesByTickerAndKeyword(t *testing.T) {
	articles := []model.Article{
		{Title: "NVDA beats on data center revenue"},
		{Title: "Fed signals a rate cut", Summary: "The Federal Reserve hinted at easing."},
		{Title: "Local bakery opens second branch"},
	}

	got := Match(articles, testGroups())

	if want := []string{"semis-ai"}; !equalStrings(got[0].GroupIDs, want) {
		t.Errorf("GroupIDs = %v, want %v", got[0].GroupIDs, want)
	}
	if want := []string{"NVDA"}; !equalStrings(got[0].Tickers, want) {
		t.Errorf("Tickers = %v, want %v", got[0].Tickers, want)
	}
	if want := []string{"macro-rates"}; !equalStrings(got[1].GroupIDs, want) {
		t.Errorf("GroupIDs = %v, want %v", got[1].GroupIDs, want)
	}
	// Unmatched articles are kept -- they feed the general overview.
	if len(got[2].GroupIDs) != 0 {
		t.Errorf("GroupIDs = %v, want none", got[2].GroupIDs)
	}
}

// The reason ticker matching is case-sensitive: several real tickers are also
// ordinary English words, and lowercasing would tag most of the feed.
func TestMatchDoesNotTreatOrdinaryWordsAsTickers(t *testing.T) {
	articles := []model.Article{
		{Title: "Runner breaks her arm during the marathon"},
		{Title: "Analysts arm themselves with new forecasts"},
	}
	for _, a := range Match(articles, testGroups()) {
		if len(a.GroupIDs) != 0 {
			t.Errorf("%q matched %v, want no match", a.Title, a.GroupIDs)
		}
	}
}

func TestMatchFindsTickersInPunctuatedContexts(t *testing.T) {
	for _, title := range []string{
		"Nvidia (NASDAQ: NVDA) reports tonight",
		"$NVDA hits a record",
		"NVDA's guidance disappoints",
		"Chart of the day: NVDA.",
	} {
		got := Match([]model.Article{{Title: title}}, testGroups())
		if len(got[0].Tickers) != 1 {
			t.Errorf("%q -> Tickers %v, want [NVDA]", title, got[0].Tickers)
		}
	}
}

func TestMatchKeywordsRespectWordBoundaries(t *testing.T) {
	// "CPI" inside "recipient" must not tag the article into Macro & Rates.
	got := Match([]model.Article{{Title: "Award recipient named"}}, testGroups())
	if len(got[0].GroupIDs) != 0 {
		t.Errorf("GroupIDs = %v, want none", got[0].GroupIDs)
	}
}

func TestLimitPrefersWatchlistMatchesThenRecency(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "a", Published: base.Add(3 * time.Hour)},                              // newest, unmatched
		{ID: "b", Published: base, GroupIDs: []string{"semis-ai"}},                 // oldest, matched
		{ID: "c", Published: base.Add(1 * time.Hour), GroupIDs: []string{"macro"}}, // matched
	}

	got := Limit(articles, 2)
	if len(got) != 2 {
		t.Fatalf("got %d articles, want 2", len(got))
	}
	if got[0].ID != "c" || got[1].ID != "b" {
		t.Errorf("kept %s,%s -- want the matched articles c,b ahead of the newer unmatched a", got[0].ID, got[1].ID)
	}
}

func TestLimitOfZeroKeepsEverything(t *testing.T) {
	articles := []model.Article{{ID: "a"}, {ID: "b"}}
	if got := Limit(articles, 0); len(got) != 2 {
		t.Errorf("got %d articles, want 2 (an unset limit must not drop news)", len(got))
	}
}

func TestResultAllFailedOnlyWhenNothingArrived(t *testing.T) {
	partial := Result{Articles: []model.Article{{ID: "a"}}, Errors: []SourceError{{SourceID: "cnbc"}}}
	if partial.AllFailed() {
		t.Error("a partial run reported AllFailed; one dead feed must not cancel the report")
	}
	total := Result{Errors: []SourceError{{SourceID: "cnbc"}}}
	if !total.AllFailed() {
		t.Error("a run with no articles and only errors must report AllFailed")
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
