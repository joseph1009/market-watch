package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/runcache"
)

// cached reads one file from a run cache folder.
func cached(t *testing.T, root, kind, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, kind, name))
	if err != nil {
		t.Fatalf("%s/%s: %v", kind, name, err)
	}
	return string(data)
}

// A brief leaves what it was written from and what was sent in cache/brief,
// so a change to it can be worked out from the last run instead of a new one.
func TestABriefKeepsItsDataInTheRunCache(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242
	root := t.TempDir()
	a.Cache = &runcache.Cache{Root: root}

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Fed holds rates steady</title><link>https://feed.example/fed</link>
<description>The Fed held.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()
	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nThe Fed held [1].\n"}}

	if err := a.SendReport(context.Background()); err != nil {
		t.Fatalf("SendReport: %v", err)
	}

	if got := cached(t, root, runcache.Brief, "articles.json"); !strings.Contains(got, "Fed holds rates steady") {
		t.Errorf("articles.json does not hold what the brief was written from:\n%s", got)
	}
	if got := cached(t, root, runcache.Brief, "collected.json"); !strings.Contains(got, `"arrived_articles"`) {
		t.Errorf("collected.json does not say what arrived:\n%s", got)
	}
	if got := cached(t, root, runcache.Brief, "messages.html"); !strings.Contains(got, "The Fed held") {
		t.Errorf("messages.html is not what was sent:\n%s", got)
	}
	var run struct {
		Finished string
		Error    string
		Files    []string
	}
	if err := json.Unmarshal([]byte(cached(t, root, runcache.Brief, "run.json")), &run); err != nil {
		t.Fatal(err)
	}
	if run.Finished == "" || run.Error != "" || len(run.Files) < 5 {
		t.Errorf("run.json = %+v, want a finished run with its files listed", run)
	}
}

// The closer look keeps the moves it read, the facts each verdict was given,
// the verdicts and what was shown, in cache/recommendations.
func TestACloserLookKeepsItsDataInTheRunCache(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242
	root := t.TempDir()
	a.Cache = &runcache.Cache{Root: root}
	rambusMoved(t, a, stubCompleter{reply: rambusBuy})

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false, false))

	for name, want := range map[string]string{
		"look.json":      "Rambus soars on HBM win",
		"moves.json":     "RMBS",
		"reactions.json": "RMBS",
		"facts.json":     `"ticker": "RMBS"`,
		"verdicts.json":  "HBM demand runs through it",
		"shown.json":     "RMBS",
		"messages.html":  "Reacting to the news",
	} {
		if got := cached(t, root, runcache.Recommendations, name); !strings.Contains(got, want) {
			t.Errorf("%s is missing %q:\n%s", name, want, got)
		}
	}
}
