package fundamentals

import (
	"fmt"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func article(title, summary string, at time.Time) model.Article {
	return model.Article{Title: title, Summary: summary, Published: at, SourceName: "Test"}
}

// The vendor tags loosely. Asking it for NVIDIA's news really does return a
// piece about holding Microsoft for ten years, and a reader who saw that under
// an NVIDIA heading would rightly stop trusting the section.
func TestRelevantDropsArticlesAboutOtherCompanies(t *testing.T) {
	now := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	articles := []model.Article{
		article("I've Held Microsoft for 10 Years. Here's Why I'm Not Selling.", "Microsoft has changed a lot.", now),
		article("5 Reasons Intel's CEO Just Made This Stock Worth Buying", "Intel's turnaround is gaining credibility.", now),
		article("Nvidia's data centre revenue climbs again", "The chipmaker reported another quarter of growth.", now),
	}

	got := Relevant(articles, "NVDA", "NVIDIA Corporation", 10)
	if len(got) != 1 {
		t.Fatalf("kept %d articles, want 1: %+v", len(got), titles(got))
	}
	if got[0].Title != articles[2].Title {
		t.Errorf("kept %q", got[0].Title)
	}
}

// A ticker is a word, not a substring: MU is Micron and "mu" is not.
func TestRelevantMatchesTickersAsWords(t *testing.T) {
	now := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	articles := []model.Article{
		article("MU upgraded on memory pricing", "", now),
		article("(MU) sets a date for results", "", now),
		article("The stimulus is cumulative across the quarter", "", now),
		article("MUX names a new chief executive", "", now),
	}

	got := Relevant(articles, "MU", "Micron Technology, Inc.", 10)
	if len(got) != 2 {
		t.Fatalf("kept %d, want 2: %v", len(got), titles(got))
	}
}

// Aggregators carry one story under a dozen bylines. Ten headlines that are one
// headline tell the reader nothing while looking like they tell them a lot.
func TestRelevantCollapsesRepeats(t *testing.T) {
	now := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	articles := []model.Article{
		article("Micron beats estimates on HBM demand", "", now),
		article("Micron beats estimates on HBM demand!", "", now.Add(-time.Hour)),
		article("micron beats estimates on hbm demand", "", now.Add(-2*time.Hour)),
		article("Micron announces a dividend", "", now.Add(-3*time.Hour)),
	}

	got := Relevant(articles, "MU", "Micron Technology, Inc.", 10)
	if len(got) != 2 {
		t.Fatalf("kept %d, want 2: %v", len(got), titles(got))
	}
}

func TestRelevantRespectsTheLimit(t *testing.T) {
	now := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	var articles []model.Article
	for i := 0; i < 20; i++ {
		articles = append(articles, article(
			"Micron story number "+string(rune('a'+i)), "", now.Add(-time.Duration(i)*time.Hour)))
	}
	if got := Relevant(articles, "MU", "Micron Technology, Inc.", 4); len(got) != 4 {
		t.Fatalf("kept %d, want 4", len(got))
	}
}

// A company name is mostly furniture. Matching on "Holdings" or "Technology"
// would return the whole market under any ticker.
func TestNameWordsDropCorporateFurniture(t *testing.T) {
	got := nameWords("Taiwan Semiconductor Manufacturing Company Limited")
	want := map[string]bool{"taiwan": true, "semiconductor": true, "manufacturing": true}
	if len(got) != len(want) {
		t.Fatalf("name words = %v", got)
	}
	for _, w := range got {
		if !want[w] {
			t.Errorf("kept %q, which does not distinguish one company from another", w)
		}
	}
}

func titles(articles []model.Article) []string {
	out := make([]string, 0, len(articles))
	for _, a := range articles {
		out = append(out, a.Title)
	}
	return out
}

// The most recent morning always has enough copy to fill the section on its
// own. A reader asking what has happened lately needs the fortnight, not ten
// wordings of today.
func TestRelevantSpreadsAcrossDays(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	var articles []model.Article
	for day := 0; day < 5; day++ {
		for i := 0; i < 8; i++ {
			articles = append(articles, article(
				fmt.Sprintf("Micron story %d on day %d", i, day), "",
				now.AddDate(0, 0, -day).Add(-time.Duration(i)*time.Minute)))
		}
	}

	got := Relevant(articles, "MU", "Micron Technology, Inc.", 9)
	if len(got) != 9 {
		t.Fatalf("kept %d, want 9", len(got))
	}

	days := map[string]int{}
	for _, a := range got {
		days[a.Published.Format(time.DateOnly)]++
	}
	if len(days) < 3 {
		t.Errorf("the list covers %d days, want at least 3: %v", len(days), titles(got))
	}
	for day, n := range days {
		if n > maxPerDay {
			t.Errorf("%s takes %d of the list, more than the %d allowed", day, n, maxPerDay)
		}
	}
}

// A quiet fortnight should still fill the list rather than stop at three a day.
func TestRelevantFallsBackToABusyDay(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	var articles []model.Article
	for i := 0; i < 6; i++ {
		articles = append(articles, article(
			fmt.Sprintf("Micron story %d", i), "", now.Add(-time.Duration(i)*time.Minute)))
	}

	if got := Relevant(articles, "MU", "Micron Technology, Inc.", 5); len(got) != 5 {
		t.Fatalf("kept %d from a single busy day, want 5", len(got))
	}
}
