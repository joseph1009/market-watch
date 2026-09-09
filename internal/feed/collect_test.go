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

func TestLimitPrefersWatchlistMatchesOverRecency(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	now := base.Add(4 * time.Hour)
	articles := []model.Article{
		{ID: "a", SourceID: "cnbc", Published: now},                               // newest, unmatched
		{ID: "b", SourceID: "cnbc", Published: base, GroupIDs: []string{"semis"}}, // oldest, matched
		{ID: "c", SourceID: "cnbc", Published: base.Add(time.Hour), Tickers: []string{"NVDA"}},
	}

	got := Limit(articles, testSources(), 2, now)
	if len(got) != 2 {
		t.Fatalf("got %d articles, want 2", len(got))
	}
	if got[0].ID != "c" {
		t.Errorf("first = %s, want c -- an explicit ticker is the strongest claim", got[0].ID)
	}
	if got[1].ID != "b" {
		t.Errorf("second = %s, want the matched b ahead of the newer unmatched a", got[1].ID)
	}
}

// The reason the score exists: recency alone let an aggregator's rewrite
// displace the primary source it was rewriting.
func TestLimitPrefersAHeavierSourceOverAFresherOne(t *testing.T) {
	now := time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "aggregator", SourceID: "aggregator", Published: now, Summary: "x"},
		{ID: "filing", SourceID: "sec-8k", Published: now.Add(-3 * time.Hour), Summary: "x"},
	}

	got := Limit(articles, testSources(), 1, now)
	if got[0].ID != "filing" {
		t.Errorf("kept %q, want the higher-weighted source despite being older", got[0].ID)
	}
}

func TestLimitRewardsCorroboration(t *testing.T) {
	now := time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "alone", SourceID: "cnbc", Published: now},
		{ID: "carried-widely", SourceID: "cnbc", Published: now, Corroborations: 3},
	}

	got := Limit(articles, testSources(), 1, now)
	if got[0].ID != "carried-widely" {
		t.Errorf("kept %q, want the story several outlets carried", got[0].ID)
	}
}

func TestLimitOfZeroKeepsEverything(t *testing.T) {
	articles := []model.Article{{ID: "a"}, {ID: "b"}}
	if got := Limit(articles, testSources(), 0, time.Now()); len(got) != 2 {
		t.Errorf("got %d articles, want 2 (an unset limit must not drop news)", len(got))
	}
}

func TestDedupeCollapsesTheSameStoryFromDifferentOutlets(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	// One story, three outlets, three URLs. URL matching alone left these
	// taking three slots in the brief.
	articles := []model.Article{
		{ID: "1", Title: "Brent crude tops $100 a barrel as US-Iran tensions escalate",
			URL: "https://cnbc.com/a", SourceID: "cnbc", Published: base.Add(time.Hour)},
		{ID: "2", Title: "Brent crude tops $100 per barrel amid escalating US-Iran tensions",
			URL: "https://aggregator.com/b", SourceID: "aggregator", Published: base},
		{ID: "3", Title: "Fed holds rates steady in September decision",
			URL: "https://cnbc.com/c", SourceID: "cnbc", Published: base},
	}

	got := Dedupe(articles, testSources())
	if len(got) != 2 {
		t.Fatalf("got %d articles, want the oil story collapsed to one", len(got))
	}
	if got[0].SourceID != "cnbc" {
		t.Errorf("survivor came from %q, want the heavier source", got[0].SourceID)
	}
	if got[0].Corroborations != 1 {
		t.Errorf("Corroborations = %d, want 1 other outlet", got[0].Corroborations)
	}
	// The earliest copy dates the story, even when a later one survives.
	if !got[0].Published.Equal(base) {
		t.Errorf("Published = %s, want the earliest copy's %s", got[0].Published, base)
	}
}

// Outlets word headlines differently: one names the ticker, another only the
// company. The story qualifies if any copy matched.
func TestDedupeUnionsWatchlistMatches(t *testing.T) {
	articles := []model.Article{
		{ID: "1", Title: "Nvidia beats expectations on data center revenue growth",
			SourceID: "cnbc", GroupIDs: []string{"semis-ai"}},
		{ID: "2", Title: "NVDA beats expectations on data center revenue growth",
			SourceID: "aggregator", GroupIDs: []string{"semis-ai"}, Tickers: []string{"NVDA"}},
	}

	got := Dedupe(articles, testSources())
	if len(got) != 1 {
		t.Fatalf("got %d articles, want 1", len(got))
	}
	if len(got[0].Tickers) != 1 || got[0].Tickers[0] != "NVDA" {
		t.Errorf("Tickers = %v, want the ticker carried over from the copy that named it", got[0].Tickers)
	}
}

// Corroboration counts outlets, not articles -- one outlet running its own
// story twice is not independent confirmation.
func TestDedupeCountsOutletsNotArticles(t *testing.T) {
	articles := []model.Article{
		{ID: "1", Title: "Brent crude tops $100 a barrel as tensions escalate", SourceID: "cnbc"},
		{ID: "2", Title: "Brent crude tops $100 a barrel amid tensions escalating", SourceID: "cnbc"},
		{ID: "3", Title: "Brent crude tops $100 per barrel as tensions escalate", SourceID: "cnbc"},
	}

	got := Dedupe(articles, testSources())
	if len(got) != 1 {
		t.Fatalf("got %d articles, want 1", len(got))
	}
	if got[0].Corroborations != 0 {
		t.Errorf("Corroborations = %d, want 0 from a single outlet", got[0].Corroborations)
	}
}

func TestDedupeKeepsDistinctStoriesAboutOneCompany(t *testing.T) {
	articles := []model.Article{
		{ID: "1", Title: "Apple unveils its first foldable iPhone at September event", SourceID: "cnbc"},
		{ID: "2", Title: "Apple raises streaming subscription prices across all tiers", SourceID: "cnbc"},
		{ID: "3", Title: "Apple names John Ternus chief executive ahead of launch", SourceID: "cnbc"},
	}

	if got := Dedupe(articles, testSources()); len(got) != 3 {
		t.Errorf("got %d articles, want 3 distinct Apple stories kept apart", len(got))
	}
}

// A report and its denial share most of their words and must never merge.
func TestDedupeKeepsAClaimApartFromItsDenial(t *testing.T) {
	articles := []model.Article{
		{ID: "1", Title: "Iran says it struck two American vessels near Hormuz", SourceID: "cnbc"},
		{ID: "2", Title: "US denies Iran struck two American vessels near Hormuz", SourceID: "cnbc"},
	}

	if got := Dedupe(articles, testSources()); len(got) != 2 {
		t.Errorf("got %d articles, want the claim and the denial kept apart", len(got))
	}
}

func TestSimilarityScoresSharedWords(t *testing.T) {
	a := titleTokens("Brent crude tops $100 a barrel as tensions escalate")
	if got := similarity(a, a); got != 1.0 {
		t.Errorf("similarity with itself = %v, want 1", got)
	}
	b := titleTokens("Gold falls despite Middle East escalation")
	if got := similarity(a, b); got > 0.3 {
		t.Errorf("similarity of unrelated headlines = %v, want it low", got)
	}
}

func TestTitleTokensStripsOutletSuffixes(t *testing.T) {
	tokens := titleTokens("Brent crude tops $100 a barrel - CNBC")
	if tokens["cnbc"] {
		t.Errorf("outlet name survived into the tokens: %v", tokens)
	}
	// A dash inside the headline is punctuation, not attribution.
	kept := titleTokens("Oil - the one thing that defined the trading day")
	if !kept["defined"] {
		t.Errorf("a mid-headline dash truncated the title: %v", kept)
	}
}

// Short headlines share words by chance, so they are left alone rather than
// guessed at: "Apple event today" and "Apple earnings today" are not one story.
func TestShortHeadlinesAreNeverMerged(t *testing.T) {
	articles := []model.Article{
		{ID: "1", Title: "Apple event today", SourceID: "cnbc"},
		{ID: "2", Title: "Apple earnings today", SourceID: "aggregator"},
	}
	if got := Dedupe(articles, testSources()); len(got) != 2 {
		t.Errorf("got %d articles, want short headlines left apart", len(got))
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

func TestDropStaleRemovesArchiveItems(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "today", Published: now.Add(-2 * time.Hour)},
		{ID: "friday", Published: now.Add(-3 * 24 * time.Hour)},
		{ID: "archive-2018", Published: time.Date(2018, 5, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "undated"}, // the fetcher already stamped these as now
	}

	got := DropStale(articles, now, MaxArticleAge)
	if len(got) != 3 {
		t.Fatalf("got %d articles, want 3", len(got))
	}
	for _, a := range got {
		if a.ID == "archive-2018" {
			t.Error("a years-old release survived into a daily brief")
		}
	}
}

// The fix for the worst false merge found against real feeds: agencies title
// recurring releases to a template, so consecutive months looked like one story.
func TestDedupeKeepsRecurringReleasesApart(t *testing.T) {
	july := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "1", Title: "Personal Income and Outlays, July 2026", SourceID: "bea", Published: july},
		{ID: "2", Title: "Personal Income and Outlays, June 2026", SourceID: "bea", Published: july.AddDate(0, -1, 0)},
		{ID: "3", Title: "Personal Income and Outlays, May 2026", SourceID: "bea", Published: july.AddDate(0, -2, 0)},
	}

	if got := Dedupe(articles, testSources()); len(got) != 3 {
		t.Errorf("got %d articles, want 3 -- months apart is not one story", len(got))
	}
}

// Identical headlines months apart are separate events, not duplicates: the Fed
// titles every rate decision "Federal Reserve issues FOMC statement".
func TestDedupeKeepsIdenticalTitlesFromDifferentMeetings(t *testing.T) {
	sept := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "1", Title: "Federal Reserve issues FOMC statement", SourceID: "fed-monetary", Published: sept},
		{ID: "2", Title: "Federal Reserve issues FOMC statement", SourceID: "fed-monetary", Published: sept.AddDate(0, -2, 0)},
	}

	if got := Dedupe(articles, testSources()); len(got) != 2 {
		t.Errorf("got %d articles, want both meetings kept", len(got))
	}
}

// Same-day coverage from different outlets is still collapsed -- the date guard
// must not disable duplicate detection where it genuinely applies.
func TestDedupeStillCollapsesSameDayCoverage(t *testing.T) {
	noon := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "1", Title: "Brent crude tops $100 a barrel as US-Iran tensions escalate",
			SourceID: "cnbc", Published: noon},
		{ID: "2", Title: "Brent crude tops $100 per barrel amid escalating US-Iran tensions",
			SourceID: "aggregator", Published: noon.Add(90 * time.Minute)},
	}

	got := Dedupe(articles, testSources())
	if len(got) != 1 {
		t.Fatalf("got %d articles, want same-day coverage collapsed", len(got))
	}
	if got[0].Corroborations != 1 {
		t.Errorf("Corroborations = %d, want 1", got[0].Corroborations)
	}
}
