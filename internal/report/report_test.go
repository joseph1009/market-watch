package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/marketdata"
	"github.com/joseph1009/market-watch/internal/model"
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
		{ID: "semis-ai", Name: "Semiconductors & AI", Tickers: []string{"NVDA"}},
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

func TestGenerateRecordsUsageAndPricesIt(t *testing.T) {
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
	// 20k input at $5/M plus 4k output at $25/M.
	if want := 0.20; rep.Usage.EstimatedUSD != want {
		t.Errorf("EstimatedUSD = %v, want %v", rep.Usage.EstimatedUSD, want)
	}
}

// A cost of zero reads as "not known"; a guessed one would be taken as fact.
func TestGenerateLeavesCostUnpricedForAnUnknownModel(t *testing.T) {
	fake := &fakeCompleter{
		reply: "## OVERVIEW\nBody.",
		model: "some-model-shipped-after-this-table",
		usage: model.Usage{InputTokens: 1000, OutputTokens: 500},
	}

	g := &Generator{Completer: fake, Now: testTime}
	rep, err := g.Generate(context.Background(), testArticles(), reportGroups())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if rep.Usage.EstimatedUSD != 0 {
		t.Errorf("EstimatedUSD = %v, want 0 for an unpriced model", rep.Usage.EstimatedUSD)
	}
	if rep.Usage.InputTokens != 1000 {
		t.Errorf("token counts were lost along with the price: %+v", rep.Usage)
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
		Levels: []marketdata.Reading{
			{Series: marketdata.Series{ID: "DGS10", Label: "US 10-year Treasury yield", Unit: "%"},
				Latest: 4.32, AsOf: testTime(), Previous: 4.28, HasPrevious: true,
				WeekAgo: 4.11, HasWeekAgo: true},
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
	} {
		if !strings.Contains(fake.prompt, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, fake.prompt)
		}
	}
}

// Directions are spelled out because the reader of this block is a language
// model, and a bare "-0.04" invites it to describe a fall as a rise.
func TestMarketLevelsStateDirectionInWords(t *testing.T) {
	if got := describeMove(-0.04); got != "down 0.04" {
		t.Errorf("describeMove(-0.04) = %q", got)
	}
	if got := describeMove(0.04); got != "up 0.04" {
		t.Errorf("describeMove(0.04) = %q", got)
	}
	if got := describeMove(0.001); got != "unchanged" {
		t.Errorf("describeMove(0.001) = %q, want unchanged", got)
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

// The reader asked not to be handed trade shorthand: the brief has to explain
// its terms rather than assume them.
func TestSystemPromptDemandsPlainLanguage(t *testing.T) {
	for _, want := range []string{
		"not a market professional",
		"25bp",
		"Keep every number, attribution and caveat",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt no longer carries %q", want)
		}
	}
}
