package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// fakeCompleter records what it was asked and replies with a canned response.
type fakeCompleter struct {
	reply  string
	usage  model.Usage
	model  string
	err    error
	system string
	prompt string
	calls  int
}

func (f *fakeCompleter) Complete(ctx context.Context, system, prompt string) (Completion, error) {
	f.calls++
	f.system, f.prompt = system, prompt
	if f.err != nil {
		return Completion{}, f.err
	}
	return Completion{Text: f.reply, Usage: f.usage, Model: f.model}, nil
}

func testTime() time.Time { return time.Date(2026, 9, 9, 20, 30, 0, 0, time.UTC) }

// testArticles gives each watchlist enough news to clear MinSectionArticles,
// plus one story matching nothing, which is what the general bucket is for.
func testArticles() []model.Article {
	base := testTime().Add(-6 * time.Hour)
	semis := []string{"semis-ai"}
	macro := []string{"macro-rates"}

	return []model.Article{
		{ID: "1", Title: "NVDA beats on data center revenue", SourceID: "cnbc", SourceName: "CNBC",
			Summary: "Revenue rose 22%.", Published: base, GroupIDs: semis, Tickers: []string{"NVDA"}},
		{ID: "2", Title: "NVDA guidance lifts the sector", SourceID: "cnbc", SourceName: "CNBC",
			Summary: "Peers followed.", Published: base.Add(time.Minute), GroupIDs: semis, Tickers: []string{"NVDA"}},
		{ID: "3", Title: "Chip equipment orders climb", SourceID: "cnbc", SourceName: "CNBC",
			Summary: "Bookings improved.", Published: base.Add(2 * time.Minute), GroupIDs: semis},
		{ID: "4", Title: "Fed signals a rate cut", SourceID: "fed-press", SourceName: "Federal Reserve",
			Summary: "Policymakers hinted at easing.", Published: base.Add(time.Hour), GroupIDs: macro},
		{ID: "5", Title: "CPI comes in below forecast", SourceID: "fed-press", SourceName: "Federal Reserve",
			Summary: "Prices cooled.", Published: base.Add(2 * time.Hour), GroupIDs: macro},
		{ID: "6", Title: "Treasury yields ease", SourceID: "fed-press", SourceName: "Federal Reserve",
			Summary: "The curve steepened.", Published: base.Add(3 * time.Hour), GroupIDs: macro},
		{ID: "7", Title: "Retail sales tick higher", SourceID: "cnbc", SourceName: "CNBC",
			Summary: "Spending held up.", Published: base.Add(4 * time.Hour)},
	}
}

func reportGroups() []model.Group {
	return []model.Group{
		{ID: "semis-ai", Name: "Semiconductors & AI", Companies: []model.Company{{Symbol: "NVDA", Name: "Nvidia"}}},
		{ID: "macro-rates", Name: "Macro & Rates"},
	}
}

func TestParseResponseSplitsOverviewAndSections(t *testing.T) {
	raw := `## OVERVIEW
Chips led the tape today.

A second paragraph.

## SECTION: semis-ai
NVDA carried the group.

## SECTION: macro-rates
The Fed did the talking.`

	got := parseResponse(raw)

	if !strings.HasPrefix(got.Overview, "Chips led the tape") {
		t.Errorf("Overview = %q", got.Overview)
	}
	if !strings.Contains(got.Overview, "A second paragraph.") {
		t.Errorf("Overview dropped a paragraph: %q", got.Overview)
	}
	if got.Sections["semis-ai"] != "NVDA carried the group." {
		t.Errorf("semis-ai = %q", got.Sections["semis-ai"])
	}
	if got.Sections["macro-rates"] != "The Fed did the talking." {
		t.Errorf("macro-rates = %q", got.Sections["macro-rates"])
	}
}

// A model that starts writing without the opening marker has still produced a
// usable brief; losing the report over the missing line would be worse.
func TestParseResponseTreatsLeadingTextAsOverview(t *testing.T) {
	raw := `Markets drifted lower.

## SECTION: semis-ai
Chips were the exception.`

	got := parseResponse(raw)
	if got.Overview != "Markets drifted lower." {
		t.Errorf("Overview = %q, want the unmarked leading text", got.Overview)
	}
	if got.Sections["semis-ai"] == "" {
		t.Error("section was lost")
	}
}

func TestParseResponseIsCaseInsensitiveAndTrimsIDs(t *testing.T) {
	raw := "## overview\nBody.\n## Section:  SEMIS-AI  \nChips."

	got := parseResponse(raw)
	if got.Overview != "Body." {
		t.Errorf("Overview = %q", got.Overview)
	}
	if got.Sections["semis-ai"] != "Chips." {
		t.Errorf("sections = %v, want the id lowercased and trimmed", got.Sections)
	}
}

func TestGenerateOrdersSectionsByWatchlistNotByModelOutput(t *testing.T) {
	// The model emits macro-rates first; the report must follow the configured
	// watchlist order so the brief reads the same way every day.
	fake := &fakeCompleter{reply: `## OVERVIEW
A quiet session.

## SECTION: macro-rates
Rates did the work.

## SECTION: semis-ai
Chips followed.`}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(rep.Sections) != 2 {
		t.Fatalf("got %d sections, want 2", len(rep.Sections))
	}
	if rep.Sections[0].GroupID != "semis-ai" || rep.Sections[1].GroupID != "macro-rates" {
		t.Errorf("section order = %s,%s; want semis-ai,macro-rates", rep.Sections[0].GroupID, rep.Sections[1].GroupID)
	}
	if rep.Sections[0].GroupName != "Semiconductors & AI" {
		t.Errorf("GroupName = %q", rep.Sections[0].GroupName)
	}
}

func TestGenerateAttachesTheArticlesBehindEachSection(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(rep.Sections[0].Articles) != 3 || rep.Sections[0].Articles[0].ID != "1" {
		t.Errorf("semis-ai articles = %v, want the three semis stories", rep.Sections[0].Articles)
	}
	// The unmatched article counts toward the totals but belongs to no section.
	if rep.ArticleCount != 7 {
		t.Errorf("ArticleCount = %d, want 7", rep.ArticleCount)
	}
	if rep.SourceCount != 2 {
		t.Errorf("SourceCount = %d, want 2 distinct sources", rep.SourceCount)
	}
	if !rep.GeneratedAt.Equal(testTime()) {
		t.Errorf("GeneratedAt = %s, want %s", rep.GeneratedAt, testTime())
	}
}

func TestGenerateDropsAWatchlistWithNoNewsAndNoProse(t *testing.T) {
	// "energy" was configured but matched nothing and the model said nothing
	// about it; rendering an empty heading would be worse than omitting it.
	groups := append(reportGroups(), model.Group{ID: "energy", Name: "Energy"})
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), testArticles(), groups)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, s := range rep.Sections {
		if s.GroupID == "energy" {
			t.Error("an empty watchlist was carried into the report")
		}
	}
}

func TestGenerateRejectsAnEmptyDayBeforeCallingTheModel(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nSomething."}

	g := &Generator{Completer: fake, Now: testTime}
	_, err := g.Generate(context.Background(), nil, reportGroups())
	if !errors.Is(err, ErrNoArticles) {
		t.Errorf("err = %v, want ErrNoArticles", err)
	}
	if fake.calls != 0 {
		t.Error("the model was called with no articles; an empty day must not be summarized")
	}
}

func TestGenerateFailsWhenTheModelReturnsNoProse(t *testing.T) {
	fake := &fakeCompleter{reply: "   \n\n  "}

	g := &Generator{Completer: fake, Now: testTime}
	if _, err := g.Generate(context.Background(), testArticles(), reportGroups()); err == nil {
		t.Error("Generate accepted an empty response; an empty brief must not be sent")
	}
}

func TestGeneratePropagatesCompleterFailure(t *testing.T) {
	sentinel := errors.New("api down")
	g := &Generator{Completer: &fakeCompleter{err: sentinel}, Now: testTime}

	_, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want it to wrap %v", err, sentinel)
	}
}

func TestGenerateRecordsUsage(t *testing.T) {
	fake := &fakeCompleter{
		reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates.",
		model: "claude-opus-5",
		usage: model.Usage{InputTokens: 20_000, OutputTokens: 4_000},
	}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rep.Usage.InputTokens != 20_000 || rep.Usage.OutputTokens != 4_000 {
		t.Errorf("Usage = %+v, want the completer's counts", rep.Usage)
	}
}

func TestPromptCarriesTheWatchlistsAndTheirArticles(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}
	sgt, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}

	g := &Generator{Completer: fake, Now: testTime, DisplayLocation: sgt}
	if _, err := g.Generate(context.Background(), testArticles(), reportGroups()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, want := range []string{
		"semis-ai", "macro-rates", // ids the model must echo back as markers
		"NVDA beats on data center revenue",
		"Fed signals a rate cut",
		"General market news", // the unmatched article still informs the overview
	} {
		if !strings.Contains(fake.prompt, want) {
			t.Errorf("prompt is missing %q\n---\n%s", want, fake.prompt)
		}
	}

	// The reader's date, not UTC: 20:30 UTC on the 9th is already the 10th in
	// Singapore, which is the morning they actually read this.
	if !strings.Contains(fake.prompt, "Thursday, 10 September 2026") {
		t.Errorf("prompt does not carry the reader's local date:\n%s", fake.prompt)
	}
}

// A watchlist with too little news is never asked about. Asking and discarding
// the answer would pay output tokens for prose nobody reads.
func TestQuietWatchlistsAreLeftOutOfThePromptAndNamedInTheReport(t *testing.T) {
	groups := append(reportGroups(), model.Group{ID: "energy", Name: "Energy"})
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), testArticles(), groups)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if strings.Contains(fake.prompt, "energy") {
		t.Errorf("the quiet watchlist was still asked about:\n%s", fake.prompt)
	}
	if !equalStringSlices(rep.QuietGroups, []string{"Energy"}) {
		t.Errorf("QuietGroups = %v, want [Energy]", rep.QuietGroups)
	}
	for _, s := range rep.Sections {
		if s.GroupID == "energy" {
			t.Error("a quiet watchlist produced a section")
		}
	}
}

// A watchlist below the threshold must not take its articles out of the brief
// with it: they still belong to the overview as general news.
func TestArticlesFromQuietWatchlistsStillReachTheModel(t *testing.T) {
	groups := append(reportGroups(), model.Group{ID: "energy", Name: "Energy"})
	articles := append(testArticles(), model.Article{
		ID: "oil", Title: "Brent crude tops $100 a barrel", SourceID: "cnbc", SourceName: "CNBC",
		Summary: "Supply fears drove it.", Published: testTime(), GroupIDs: []string{"energy"},
	})
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}

	g := &Generator{Completer: fake, Now: testTime}
	if _, err := g.Generate(context.Background(), articles, groups); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(fake.prompt, "Brent crude tops") {
		t.Errorf("an article whose only watchlist was quiet vanished from the prompt:\n%s", fake.prompt)
	}
}

func TestSectionsAppearOnceAWatchlistHasEnoughNews(t *testing.T) {
	// Two articles is usually one story and its follow-up; three is the point
	// where a section has something to say.
	groups := []model.Group{{ID: "energy", Name: "Energy"}}
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: energy\nOil rose."}

	var articles []model.Article
	for i := 0; i < MinSectionArticles; i++ {
		articles = append(articles, model.Article{
			ID: fmt.Sprintf("oil-%d", i), Title: "Oil story", SourceID: "cnbc", SourceName: "CNBC",
			Published: testTime(), GroupIDs: []string{"energy"},
		})
	}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), articles, groups)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(rep.Sections) != 1 || rep.Sections[0].GroupID != "energy" {
		t.Errorf("Sections = %+v, want an energy section", rep.Sections)
	}
	if len(rep.QuietGroups) != 0 {
		t.Errorf("QuietGroups = %v, want none", rep.QuietGroups)
	}
}

func equalStringSlices(got, want []string) bool {
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

// Levels are measured values, not claims by an outlet. The prompt has to say so,
// or the model will attribute a yield to whichever article sat nearest it.
func TestPromptSeparatesMarketLevelsFromArticles(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}
	g := &Generator{
		Completer: fake,
		Now:       testTime,
		Levels: []prices.Reading{
			{Indicator: prices.Indicator{ID: "DGS10", Label: "US 10-year Treasury yield", Unit: "%"},
				Latest: 4.32, AsOf: testTime(), Previous: 4.28, HasPrevious: true,
				WeekAgo: 4.11, HasWeekAgo: true},
			// A monthly figure is dated by its month and compared with the last.
			{Indicator: prices.Indicator{ID: "CPIAUCSL", Label: "US consumer price inflation", Unit: "%", Units: "pc1", Monthly: true},
				Latest: 3.35302, AsOf: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Previous: 3.30386, HasPrevious: true},
		},
	}

	if _, err := g.Generate(context.Background(), testArticles(), reportGroups()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, want := range []string{
		"Market levels",
		"US 10-year Treasury yield: 4.32%",
		"up 0.04 since the previous session",
		"up 0.21 over the past week",
		"not claims made by any article",
		"US consumer price inflation: 3.35% for August 2026, up 0.05 from the month before",
	} {
		if !strings.Contains(fake.prompt, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, fake.prompt)
		}
	}
}

func TestPromptOmitsTheLevelsBlockWithoutData(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}
	g := &Generator{Completer: fake, Now: testTime}

	if _, err := g.Generate(context.Background(), testArticles(), reportGroups()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(fake.prompt, "Market levels") {
		t.Errorf("rendered an empty levels block:\n%s", fake.prompt)
	}
}

// The full source list shows the articles no section claimed, so the report has
// to carry them: they are what the overview was written from.
func TestGenerateRecordsTheGeneralNews(t *testing.T) {
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}
	g := &Generator{Completer: fake, Now: testTime}

	rep, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(rep.General) != 1 || rep.General[0].ID != "7" {
		t.Errorf("General = %+v, want the one article no watchlist claimed", rep.General)
	}
}

// An article whose only watchlist was too quiet for a section still informed
// the overview, so it belongs in the general list rather than vanishing.
func TestGeneralNewsIncludesArticlesFromQuietWatchlists(t *testing.T) {
	groups := append(reportGroups(), model.Group{ID: "energy", Name: "Energy"})
	articles := append(testArticles(), model.Article{
		ID: "oil", Title: "Brent crude tops $100 a barrel", SourceID: "cnbc", SourceName: "CNBC",
		Published: testTime(), GroupIDs: []string{"energy"},
	})
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips.\n## SECTION: macro-rates\nRates."}
	g := &Generator{Completer: fake, Now: testTime}

	rep, err := g.Generate(context.Background(), articles, groups)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	found := false
	for _, a := range rep.General {
		if a.ID == "oil" {
			found = true
		}
	}
	if !found {
		t.Errorf("the quiet watchlist's article is missing from General: %+v", rep.General)
	}
}

// A section is written in a handful of blocks whatever it is handed, so past a
// couple of dozen articles the rest are sent, paid for and discarded.
func TestPromptCapsHowManyArticlesASectionIsWrittenFrom(t *testing.T) {
	base := testTime().Add(-6 * time.Hour)
	articles := make([]model.Article, 0, MaxSectionArticles+4)
	for i := range cap(articles) {
		articles = append(articles, model.Article{
			ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Chip story %d", i),
			SourceID: "cnbc", SourceName: "CNBC", Published: base,
			GroupIDs: []string{"semis-ai"}, Rating: 5,
		})
	}

	prompt := buildPrompt(articles, reportGroups(), market{}, testTime(), time.UTC)

	if want := fmt.Sprintf("(semis-ai) -- %d articles", MaxSectionArticles); !strings.Contains(prompt, want) {
		t.Errorf("prompt does not say %q:\n%s", want, prompt)
	}
	if !strings.Contains(prompt, "Chip story 0") {
		t.Error("the strongest article was cut")
	}
	if last := fmt.Sprintf("Chip story %d", MaxSectionArticles+3); strings.Contains(prompt, last) {
		t.Errorf("%q is past the cap and should not be in the prompt", last)
	}
	// An article cut from a full section must not come back as general news:
	// its sector is being written about.
	if strings.Contains(prompt, "General market news") {
		t.Error("an article the cap cut reappeared as general news")
	}
	if want := fmt.Sprintf("Articles: %d from", MaxSectionArticles); !strings.Contains(prompt, want) {
		t.Errorf("the header counts articles the model was not shown:\n%s", prompt)
	}
}

// The general block is the biggest thing in the prompt and the least cited. A
// story outside every watchlist can still matter, but it is never a 2.
func TestGeneralNewsKeepsOnlyWhatCouldReachTheOverview(t *testing.T) {
	articles := append(testArticles(),
		model.Article{ID: "8", Title: "Bakery opens on the high street", SourceID: "cnbc",
			SourceName: "CNBC", Published: testTime(), Rating: 2},
		model.Article{ID: "9", Title: "Dollar rallies on rate bets", SourceID: "cnbc",
			SourceName: "CNBC", Published: testTime(), Rating: 4},
	)

	prompt := buildPrompt(articles, reportGroups(), market{}, testTime(), time.UTC)

	if strings.Contains(prompt, "Bakery opens") {
		t.Error("a rated-2 article was offered as general news")
	}
	if !strings.Contains(prompt, "Dollar rallies") {
		t.Error("a rated-4 article was cut from general news")
	}
	// Article 7 was never rated -- sorting off, or a failed batch -- and that
	// means unjudged, not unimportant.
	if !strings.Contains(prompt, "Retail sales tick higher") {
		t.Error("an unrated article was cut from general news")
	}
}

// A keyword match proves the name appears, not that the article is about it.
// "Morgan Stanley sees Shell hitting new highs" names a bank and is about an oil
// company, and the sorting had already rated it a 2.
func TestSectionsLeaveOutKeywordMatchesRatedAsNoise(t *testing.T) {
	base := testTime().Add(-6 * time.Hour)
	fin := []string{"financials"}
	articles := []model.Article{
		{ID: "a", Title: "Morgan Stanley sees Shell hitting new highs", SourceID: "yahoo",
			SourceName: "Yahoo Finance", Published: base, GroupIDs: fin, Rating: 2},
		{ID: "b", Title: "Citigroup appoints a new head of China sales", SourceID: "yahoo",
			SourceName: "Yahoo Finance", Published: base, GroupIDs: fin, Rating: 1},
		{ID: "c", Title: "Visa moves to close a card rewards loophole", SourceID: "cnbc",
			SourceName: "CNBC", Published: base, GroupIDs: fin, Rating: 3},
		{ID: "d", Title: "JPMorgan lifts its loan-loss reserves", SourceID: "cnbc",
			SourceName: "CNBC", Published: base, GroupIDs: fin, Rating: 4},
		// Never rated: sorting off, or a failed batch. Not judged is not noise.
		{ID: "e", Title: "Goldman Sachs trading revenue jumps", SourceID: "cnbc",
			SourceName: "CNBC", Published: base, GroupIDs: fin},
	}
	groups := []model.Group{{ID: "financials", Name: "Financials & Banks"}}

	prompt := buildPrompt(articles, groups, market{}, testTime(), time.UTC)

	for _, gone := range []string{"Shell hitting new highs", "head of China sales"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("%q was rated as noise and is still in the section", gone)
		}
	}
	for _, kept := range []string{"card rewards loophole", "loan-loss reserves", "trading revenue jumps"} {
		if !strings.Contains(prompt, kept) {
			t.Errorf("%q was cut, but only 1s and 2s should be", kept)
		}
	}
	if !strings.Contains(prompt, "(financials) -- 3 articles") {
		t.Errorf("the section header counts articles it no longer carries:\n%s", prompt)
	}
}

// A sector whose only news was noise is quiet. Saying so is better than a
// section padded out of board appointments.
func TestAWatchlistWithOnlyNoiseIsQuiet(t *testing.T) {
	base := testTime().Add(-6 * time.Hour)
	articles := append(testArticles(),
		model.Article{ID: "n1", Title: "Retailer appoints a director", SourceID: "sec", GroupIDs: []string{"consumer-retail"}, Published: base, Rating: 2},
		model.Article{ID: "n2", Title: "Coffee chain launches a seasonal cup", SourceID: "cnbc", GroupIDs: []string{"consumer-retail"}, Published: base, Rating: 2},
		model.Article{ID: "n3", Title: "Five retail stocks to buy now", SourceID: "yahoo", GroupIDs: []string{"consumer-retail"}, Published: base, Rating: 2},
	)
	groups := append(reportGroups(), model.Group{ID: "consumer-retail", Name: "Consumer & Retail"})

	active, quiet := splitByCoverage(articles, groups, MinSectionArticles)
	for _, g := range active {
		if g.ID == "consumer-retail" {
			t.Fatal("a watchlist of three noise articles was given a section")
		}
	}
	if len(quiet) != 1 || quiet[0] != "Consumer & Retail" {
		t.Errorf("quiet = %v, want Consumer & Retail named as quiet", quiet)
	}
}

// The cap counts what is left after the noise is taken out, so a 2 can never
// hold a place a real story needed.
func TestTheSectionCapIsFilledAfterTheNoiseIsRemoved(t *testing.T) {
	base := testTime().Add(-6 * time.Hour)
	var articles []model.Article
	for i := range 5 {
		articles = append(articles, model.Article{
			ID: fmt.Sprintf("noise%d", i), Title: fmt.Sprintf("Noise %d", i),
			Published: base, GroupIDs: []string{"semis-ai"}, Rating: 2,
		})
	}
	for i := range MaxSectionArticles {
		articles = append(articles, model.Article{
			ID: fmt.Sprintf("real%d", i), Title: fmt.Sprintf("Real %d", i),
			Published: base, GroupIDs: []string{"semis-ai"}, Rating: 4,
		})
	}

	got := sectionArticles(articles, "semis-ai")
	if len(got) != MaxSectionArticles {
		t.Fatalf("section has %d articles, want %d", len(got), MaxSectionArticles)
	}
	for _, a := range got {
		if a.Rating < MinSectionRating {
			t.Errorf("%q is rated %d and took a place in the section", a.Title, a.Rating)
		}
	}
}

// The sources under a section are meant to be what it was written from, so a
// capped section may not list articles the model never saw.
func TestSectionSourcesAreOnlyWhatTheModelWasShown(t *testing.T) {
	base := testTime().Add(-6 * time.Hour)
	articles := make([]model.Article, 0, MaxSectionArticles+4)
	for i := range cap(articles) {
		articles = append(articles, model.Article{
			ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Chip story %d", i),
			SourceID: "cnbc", SourceName: "CNBC", Published: base,
			GroupIDs: []string{"semis-ai"}, Rating: 5,
		})
	}
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips."}
	g := &Generator{Completer: fake, Now: testTime}

	rep, err := g.Generate(context.Background(), articles, reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(rep.Sections) != 1 || len(rep.Sections[0].Articles) != MaxSectionArticles {
		t.Fatalf("section articles = %d, want %d", len(rep.Sections[0].Articles), MaxSectionArticles)
	}
}

// The reader asked not to be handed trade shorthand: the brief has to explain
// its terms rather than assume them. And later for it sharp: sub-headings and
// one-sentence bullets, keeping what carries each point.
func TestSystemPromptDemandsPlainLanguage(t *testing.T) {
	for _, want := range []string{
		"not a market professional",
		"25bp",
		"the numbers, attributions and caveats that carry each point",
		`a line starting "### "`,
		"at most about twenty-five words",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt no longer carries %q", want)
		}
	}
}

// The reader asked to be able to check a claim against its source, which means
// every article has to arrive with a number the model can point back at.
func TestPromptNumbersArticlesAndFlagsRepeats(t *testing.T) {
	now := time.Date(2026, 9, 16, 20, 30, 0, 0, time.UTC)
	articles := []model.Article{
		{ID: "a", Title: "Oil surges on Gulf attack", SourceName: "Reuters", Published: now},
		{ID: "b", Title: "Fed holds rates", SourceName: "CNBC", Published: now,
			Covered: now.AddDate(0, 0, -2)},
	}

	prompt := buildPrompt(articles, nil, market{}, now, time.UTC)

	if !strings.Contains(prompt, "[1] Oil surges on Gulf attack") {
		t.Errorf("articles are not numbered for citation:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[2] Fed holds rates") {
		t.Errorf("numbering is not continuous:\n%s", prompt)
	}
	if !strings.Contains(prompt, "already reported in the brief of 14 Sep") {
		t.Errorf("a story an earlier brief carried was not flagged:\n%s", prompt)
	}
	if strings.Count(prompt, "already reported") != 1 {
		t.Errorf("an unseen story was flagged as a repeat:\n%s", prompt)
	}
}

func TestSystemPromptAsksForCitationsAndSkipsRepeats(t *testing.T) {
	for _, want := range []string{
		"Cite your source",
		"never invent one",
		"Leave it out unless something has moved since",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt no longer carries %q", want)
		}
	}
}
