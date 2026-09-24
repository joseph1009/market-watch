package fundamentals

import (
	"fmt"
	"strings"
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

// The searches return by relevance, not by date, and a list that filled up
// before the input ran out was handed back in that order.
func TestRelevantIsNewestFirstWhenTheListFills(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	articles := []model.Article{
		article("Micron story from last week", "", now.AddDate(0, 0, -7)),
		article("Micron story from today", "", now),
		article("Micron story from yesterday", "", now.AddDate(0, 0, -1)),
	}

	got := titles(Relevant(articles, "MU", "Micron Technology, Inc.", 2))
	if len(got) != 2 || got[0] != "Micron story from today" || got[1] != "Micron story from last week" {
		t.Errorf("got %q, want the first two found, newest first", got)
	}
}

// Sorted by date alone, the feed's day of share-price items took every place
// and the searches' month of reporting none. The searches have first call on
// searchPlaces, the feed has the rest, and either fills what the other cannot.
func TestSetNewsKeepsPlacesForTheSearches(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	var feed, searched []model.Article
	for i := 0; i < 20; i++ {
		feed = append(feed, article(fmt.Sprintf("Alibaba shares move, take %d", i), "", now.Add(-time.Duration(i)*time.Hour)))
	}
	for i := 0; i < 10; i++ {
		searched = append(searched, article(fmt.Sprintf("Alibaba cloud story %d", i), "", now.AddDate(0, 0, -3-2*i)))
	}
	fromSearch := func(news []model.Article) int {
		n := 0
		for _, a := range news {
			if strings.Contains(a.Title, "cloud story") {
				n++
			}
		}
		return n
	}
	snap := Snapshot{Ticker: "BABA", Company: "Alibaba Group Holding Ltd"}

	SetNews(&snap, searched, feed)
	if len(snap.News) != maxHeadlines || fromSearch(snap.News) != searchPlaces {
		t.Errorf("kept %d, %d of them searched; want %d and %d:\n%s",
			len(snap.News), fromSearch(snap.News), maxHeadlines, searchPlaces, strings.Join(titles(snap.News), "\n"))
	}

	// A thin feed leaves its places to the searches, and no search leaves
	// them all to the feed.
	SetNews(&snap, searched, feed[:2])
	if len(snap.News) != 12 || fromSearch(snap.News) != 10 {
		t.Errorf("with two from the feed, kept %d, %d searched; want 12 and 10", len(snap.News), fromSearch(snap.News))
	}
	SetNews(&snap, nil, feed)
	if len(snap.News) != maxHeadlines {
		t.Errorf("with no search, kept %d from the feed, want %d", len(snap.News), maxHeadlines)
	}

	// A story both carry is shown once.
	both := article("Alibaba prices HK$80bn share placing", "", now)
	SetNews(&snap, []model.Article{both}, []model.Article{both})
	if len(snap.News) != 1 {
		t.Errorf("a story in both was kept %d times", len(snap.News))
	}
}
