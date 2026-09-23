package search

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
)

// TestLiveSearchBesideTheFeeds runs today's searches and today's feeds and
// says what each found that the other did not. It spends real credits, about
// fifteen, and sends nothing anywhere.
//
//	SEARCH_LIVE=1 go test ./internal/search -run TestLive -v -timeout 5m
//
// It is the first look at a question the run record then answers properly over
// a fortnight: whether search can take over from the media feeds.
func TestLiveSearchBesideTheFeeds(t *testing.T) {
	if os.Getenv("SEARCH_LIVE") == "" {
		t.Skip("set SEARCH_LIVE=1 to run real searches; it spends about fifteen credits")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		t.Fatal("TAVILY_API_KEY is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	prefs := config.DefaultPrefs()
	c := &Client{APIKey: key, HTTP: &http.Client{Timeout: 30 * time.Second}}

	before, err := c.Usage(ctx)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	t.Logf("plan %s: %d of %d credits used before this run", before.Plan, before.Used, before.Limit)

	queries := Queries(prefs.Groups)
	found := c.Collect(ctx, queries, time.Now().Add(-24*time.Hour))
	for _, err := range found.Errors {
		t.Errorf("search failed: %v", err)
	}
	t.Logf("%d searches, %d articles, %d credits", len(queries), len(found.Articles), found.Credits)

	byOutlet := map[string]int{}
	for _, a := range found.Articles {
		byOutlet[a.SourceName]++
	}
	t.Logf("by outlet: %s", ranked(byOutlet))

	f := &feed.Fetcher{Client: &http.Client{Timeout: 30 * time.Second}, UserAgent: os.Getenv("USER_AGENT")}
	sources := prefs.EnabledSources()
	fetched, errs := f.Fetch(ctx, sources)
	for _, e := range errs {
		t.Logf("feed %s failed: %v", e.SourceID, e.Err)
	}
	fetched = feed.DropStale(fetched, time.Now(), feed.MaxArticleAge)

	// Only the past day of feed articles, to compare like with like: the
	// feeds carry a week, the searches a day.
	var recent []model.Article
	for _, a := range fetched {
		if time.Since(a.Published) <= 24*time.Hour {
			recent = append(recent, a)
		}
	}

	all := append(append([]model.Article{}, recent...), found.Articles...)
	stories := feed.Dedupe(all, append(sources, Sources()...))

	var searchOnly, feedOnly, both int
	// Per feed: how many of its stories there were, and how many of those a
	// search also found. A media feed whose stories search nearly always
	// finds is one search could replace.
	carried, alsoSearched := map[string]int{}, map[string]int{}
	var newHeadlines []string
	for _, s := range stories {
		var fromSearch, fromFeed bool
		for _, id := range s.Carriers() {
			if IsSearch(id) {
				fromSearch = true
			} else {
				fromFeed = true
			}
		}
		for _, id := range s.Carriers() {
			if IsSearch(id) {
				continue
			}
			carried[id]++
			if fromSearch {
				alsoSearched[id]++
			}
		}
		switch {
		case fromSearch && fromFeed:
			both++
		case fromSearch:
			searchOnly++
			if len(newHeadlines) < 15 {
				newHeadlines = append(newHeadlines, s.SourceName+": "+s.Title)
			}
		default:
			feedOnly++
		}
	}
	t.Logf("past day: %d feed articles, %d search articles, %d stories after dedupe", len(recent), len(found.Articles), len(stories))
	t.Logf("stories found by search alone: %d; by the feeds alone: %d; by both: %d", searchOnly, feedOnly, both)
	var lines []string
	for _, src := range sources {
		if n := carried[src.ID]; n > 0 {
			lines = append(lines, fmt.Sprintf("%-22s %3d of %3d", src.ID, alsoSearched[src.ID], n))
		}
	}
	t.Logf("each feed's past-day stories that a search also found:\n  %s", strings.Join(lines, "\n  "))
	t.Logf("a sample of what search found that no feed carried:\n  %s", strings.Join(newHeadlines, "\n  "))
}

// ranked formats counts largest first.
func ranked(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + " " + strconv.Itoa(counts[k])
	}
	return strings.Join(parts, ", ")
}
