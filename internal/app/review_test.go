package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/history"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/triage"
)

// reviewStub moves every item it is shown into one section.
type reviewStub struct{ to string }

func (s reviewStub) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	var lines []string
	for _, line := range strings.Split(prompt, "\n") {
		if n, _, ok := strings.Cut(line, ". ["); ok && !strings.HasPrefix(line, " ") {
			lines = append(lines, n+"|"+s.to)
		}
	}
	return strings.Join(lines, "\n"), model.Usage{InputTokens: 50, OutputTokens: 5}, nil
}

// The review runs between the sorting and the brief, and the run record keeps
// what it moved, which is what /stats shows to judge whether it earns its time.
func TestABriefRecordsWhatTheReviewMoved(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Fed holds rates steady</title><link>https://feed.example/fed</link>
<description>The Fed held.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()

	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Review = &triage.Reviewer{Completer: reviewStub{to: "macro-rates"}}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nThe Fed held [1].\n"}}
	runs, err := history.LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.Runs = runs

	done, err := a.sendReport(context.Background(), 0)
	if err != nil {
		t.Fatalf("sendReport: %v", err)
	}

	run := a.Runs.All()[0]
	if run.Moved != 1 || len(run.MovedExamples) != 1 ||
		run.MovedExamples[0] != "Fed holds rates steady: none → Macro & Rates" {
		t.Errorf("Moved=%d examples=%q", run.Moved, run.MovedExamples)
	}
	if done.rep.Triage.InputTokens != 50 {
		t.Errorf("footer tokens = %+v, want the review's counted beside the sorting's", done.rep.Triage)
	}
}

// triageStub answers every sorting batch with the same reply.
type triageStub struct{ reply string }

func (s triageStub) Complete(context.Context, string, string) (string, model.Usage, error) {
	return s.reply, model.Usage{}, nil
}

// A story the sorting rated 3 fills a section with no other news once the
// review confirms it, and the run record keeps it apart from the review's
// moves, so /stats can say how often the top-up is used and what it adds.
func TestABriefRecordsWhatFilledAThinSection(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Fed holds rates steady</title><link>https://feed.example/fed</link>
<description>The Fed held.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()

	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Triage = &triage.Triager{Completer: triageStub{reply: "1|3|macro-rates"}}
	a.Review = &triage.Reviewer{Completer: reviewStub{to: "macro-rates"}}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nThe Fed held [1].\n"}}
	runs, err := history.LoadRuns(filepath.Join(t.TempDir(), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.Runs = runs

	if _, err := a.sendReport(context.Background(), 0); err != nil {
		t.Fatalf("sendReport: %v", err)
	}

	run := a.Runs.All()[0]
	if run.ToppedUp != 1 || run.TopUpsKept != 1 || run.Moved != 0 {
		t.Errorf("ToppedUp=%d TopUpsKept=%d Moved=%d, want one added, kept, and no move", run.ToppedUp, run.TopUpsKept, run.Moved)
	}
	if len(run.TopUpExamples) != 1 || run.TopUpExamples[0] != "Fed holds rates steady → Macro & Rates" {
		t.Errorf("TopUpExamples = %q", run.TopUpExamples)
	}
}
