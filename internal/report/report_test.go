package report

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// fakeCompleter records what it was asked and replies with a canned response.
type fakeCompleter struct {
	reply  string
	err    error
	system string
	prompt string
	calls  int
}

func (f *fakeCompleter) Complete(ctx context.Context, system, prompt string) (string, error) {
	f.calls++
	f.system, f.prompt = system, prompt
	return f.reply, f.err
}

func testTime() time.Time { return time.Date(2026, 9, 9, 20, 30, 0, 0, time.UTC) }

func testArticles() []model.Article {
	base := testTime().Add(-6 * time.Hour)
	return []model.Article{
		{ID: "1", Title: "NVDA beats on data center revenue", SourceID: "cnbc", SourceName: "CNBC",
			Summary: "Revenue rose 22%.", Published: base, GroupIDs: []string{"semis-ai"}, Tickers: []string{"NVDA"}},
		{ID: "2", Title: "Fed signals a rate cut", SourceID: "fed-press", SourceName: "Federal Reserve",
			Summary: "Policymakers hinted at easing.", Published: base.Add(time.Hour), GroupIDs: []string{"macro-rates"}},
		{ID: "3", Title: "Retail sales tick higher", SourceID: "cnbc", SourceName: "CNBC",
			Summary: "Spending held up.", Published: base.Add(2 * time.Hour)},
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

	if len(rep.Sections[0].Articles) != 1 || rep.Sections[0].Articles[0].ID != "1" {
		t.Errorf("semis-ai articles = %v, want the NVDA story", rep.Sections[0].Articles)
	}
	// The unmatched article counts toward the totals but belongs to no section.
	if rep.ArticleCount != 3 {
		t.Errorf("ArticleCount = %d, want 3", rep.ArticleCount)
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

func TestPromptTellsTheModelWhenAWatchlistIsQuiet(t *testing.T) {
	groups := append(reportGroups(), model.Group{ID: "energy", Name: "Energy"})
	fake := &fakeCompleter{reply: "## OVERVIEW\nBody.\n## SECTION: semis-ai\nChips."}

	g := &Generator{Completer: fake, Now: testTime}
	if _, err := g.Generate(context.Background(), testArticles(), groups); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(fake.prompt, "no articles matched this watchlist today") {
		t.Errorf("prompt does not mark the quiet watchlist:\n%s", fake.prompt)
	}
}
