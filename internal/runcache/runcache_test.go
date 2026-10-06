package runcache

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A new run of a kind replaces the last one of that kind, and leaves the other
// kinds alone: the folder only ever holds the latest.
func TestStartKeepsOnlyTheLatestRunOfEachKind(t *testing.T) {
	root := t.TempDir()
	c := &Cache{Root: root}

	_, first := c.Start(context.Background(), Brief, "")
	first.Save("articles", []string{"yesterday"})
	first.Finish(nil)
	_, other := c.Start(context.Background(), Analysis, "MU")
	other.Save("snapshot", map[string]string{"ticker": "MU"})
	other.Finish(nil)

	_, second := c.Start(context.Background(), Brief, "")
	second.Save("prices", []int{1})
	second.Finish(nil)

	if _, err := os.Stat(filepath.Join(root, Brief, "articles.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("yesterday's articles survived a new brief: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(root, Brief, "prices.json")), "1") {
		t.Error("the new brief's prices are missing")
	}
	if !strings.Contains(read(t, filepath.Join(root, Analysis, "snapshot.json")), "MU") {
		t.Error("a new brief emptied the analysis folder")
	}
}

// Two analyses at once write apart and may finish in either order. The folder
// keeps the one that finished last, whichever was asked for first.
func TestTheRunThatFinishedLastIsKept(t *testing.T) {
	root := t.TempDir()
	clock := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	c := &Cache{Root: root, Now: func() time.Time { return clock }}

	_, mu := c.Start(context.Background(), Analysis, "MU")
	clock = clock.Add(time.Minute)
	_, nvda := c.Start(context.Background(), Analysis, "NVDA")
	mu.Save("snapshot", "MU")
	nvda.Save("snapshot", "NVDA")
	if _, err := os.Stat(filepath.Join(root, Analysis)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a run reached the analysis folder before it finished: %v", err)
	}

	clock = clock.Add(3 * time.Minute)
	nvda.Finish(nil)
	clock = clock.Add(time.Minute)
	mu.Finish(nil)

	if got := read(t, filepath.Join(root, Analysis, "snapshot.json")); !strings.Contains(got, "MU") {
		t.Errorf("kept %s, want MU, the last to finish", got)
	}
	if left, _ := os.ReadDir(filepath.Join(root, running)); len(left) != 0 {
		t.Errorf("%d runs were left under .running", len(left))
	}
}

// A run whose end time is earlier than the one kept is thrown away, even if
// it reaches the folder second: the timestamps decide, not the order the two
// happen to be moved in.
func TestARunThatEndedBeforeTheOneKeptIsThrownAway(t *testing.T) {
	root := t.TempDir()
	clock := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	c := &Cache{Root: root, Now: func() time.Time { return clock }}

	_, mu := c.Start(context.Background(), Analysis, "MU")
	_, nvda := c.Start(context.Background(), Analysis, "NVDA")
	mu.Save("snapshot", "MU")
	nvda.Save("snapshot", "NVDA")

	clock = clock.Add(5 * time.Minute)
	nvda.Finish(nil)
	clock = clock.Add(-time.Minute) // MU ended a minute earlier, but is moved second
	mu.Finish(nil)

	if got := read(t, filepath.Join(root, Analysis, "snapshot.json")); !strings.Contains(got, "NVDA") {
		t.Errorf("kept %s, want NVDA, which ended later", got)
	}
	if left, _ := os.ReadDir(filepath.Join(root, running)); len(left) != 0 {
		t.Errorf("the run thrown away was left under .running: %d", len(left))
	}
}

// A run cut short, by a restart say, stays to be read until the next run of
// its kind starts, which clears it away.
func TestARunCutShortIsClearedByTheNextOfItsKind(t *testing.T) {
	root := t.TempDir()
	_, cut := (&Cache{Root: root}).Start(context.Background(), Brief, "")
	cut.Save("articles", "half done")

	c := &Cache{Root: root} // the process started again
	if left, _ := os.ReadDir(filepath.Join(root, running)); len(left) != 1 {
		t.Fatalf("the cut-short run was not left to read: %d folders", len(left))
	}
	_, next := c.Start(context.Background(), Brief, "")
	if left, _ := os.ReadDir(filepath.Join(root, running)); len(left) != 1 || filepath.Join(root, running, left[0].Name()) != next.Dir() {
		t.Errorf("the cut-short run was not cleared: %v", left)
	}
}

// A step that runs twice keeps both, and files go into subfolders by name.
func TestSaveNumbersARepeatedName(t *testing.T) {
	root := t.TempDir()
	_, e := (&Cache{Root: root}).Start(context.Background(), Recommendations, "")
	e.Save("verdicts", []string{"first round"})
	e.Save("verdicts", []string{"stand-ins"})
	e.Text("model/01-ideas-request.txt", "the prompt")
	e.Finish(nil)

	dir := filepath.Join(root, Recommendations)
	if !strings.Contains(read(t, filepath.Join(dir, "verdicts.json")), "first round") ||
		!strings.Contains(read(t, filepath.Join(dir, "verdicts-2.json")), "stand-ins") {
		t.Error("the second round overwrote the first, or was lost")
	}
	if read(t, filepath.Join(dir, "model", "01-ideas-request.txt")) != "the prompt" {
		t.Error("the model call was not kept under model/")
	}
}

// The cache is copied off the server, so no credential may reach it, whatever
// carried it in.
func TestEverythingWrittenIsScrubbed(t *testing.T) {
	root := t.TempDir()
	c := &Cache{Root: root, Secrets: []string{"finnhub-secret-key", ""}}
	_, e := c.Start(context.Background(), Brief, "")
	e.Save("articles", []string{"https://finnhub.io/api?token=finnhub-secret-key"})
	e.Text("messages.html", "sent with finnhub-secret-key")
	e.Finish(errors.New("GET ?token=finnhub-secret-key: timeout"))

	for _, name := range []string{"articles.json", "messages.html", "run.json"} {
		if got := read(t, filepath.Join(root, Brief, name)); strings.Contains(got, "finnhub-secret-key") {
			t.Errorf("%s holds the key: %s", name, got)
		}
	}
}

// run.json says when the run began and ended, what it was about, which files
// it wrote, and why it fell short where it did.
func TestFinishRecordsTheRun(t *testing.T) {
	root := t.TempDir()
	clock := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	c := &Cache{Root: root, Now: func() time.Time { return clock }}

	ctx, e := c.Start(context.Background(), Analysis, "BABA")
	if From(ctx) != e {
		t.Fatal("the context does not carry the entry")
	}
	From(ctx).Save("snapshot", struct{}{})
	e.Fail(errors.New("could not read BABA"))
	clock = clock.Add(4 * time.Minute)
	e.Finish(nil)

	var got struct {
		Kind, Subject, Error string
		Started, Finished    time.Time
		Files                []string
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, Analysis, "run.json"))), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != Analysis || got.Subject != "BABA" || got.Error != "could not read BABA" {
		t.Errorf("run = %+v", got)
	}
	if got.Finished.Sub(got.Started) != 4*time.Minute || len(got.Files) != 1 || got.Files[0] != "snapshot.json" {
		t.Errorf("run = %+v", got)
	}
}

// With no cache configured, and in the tests that set none, every call is a
// no-op rather than a nil dereference.
func TestANilCacheDoesNothing(t *testing.T) {
	var c *Cache
	ctx, e := c.Start(context.Background(), Brief, "")
	if e != nil || From(ctx) != nil {
		t.Fatal("a nil cache made an entry")
	}
	e.Save("x", 1)
	e.Text("x.txt", "x")
	e.Fail(errors.New("x"))
	e.Finish(nil)
	From(context.Background()).Save("x", 1)
}

// A figure the history is too short for is NaN, which JSON cannot hold: it is
// written as null and the rest of the value is kept, as encoding/json would
// name it.
func TestANaNIsSavedAsNull(t *testing.T) {
	type listing struct {
		Symbol string
	}
	type stock struct {
		listing
		R24, R12 float64
		Note     string    `json:"note,omitempty"`
		Hidden   string    `json:"-"`
		AsOf     time.Time `json:"as_of"`
	}
	c := &Cache{Root: t.TempDir()}
	_, e := c.Start(context.Background(), Recommendations, "")
	e.Save("leaders", []stock{{listing: listing{Symbol: "ARW"}, R24: math.NaN(), R12: 0.42, Hidden: "x",
		AsOf: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}})

	var got []map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(e.Dir(), "leaders.json"))), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("leaders = %v", got)
	}
	s := got[0]
	if v, ok := s["R24"]; !ok || v != nil {
		t.Errorf("R24 = %v, want null", v)
	}
	if s["R12"] != 0.42 || s["Symbol"] != "ARW" || s["as_of"] != "2026-10-02T00:00:00Z" {
		t.Errorf("leader = %v", s)
	}
	if _, ok := s["note"]; ok {
		t.Errorf("empty omitempty field written: %v", s)
	}
	if _, ok := s["Hidden"]; ok {
		t.Errorf("hidden field written: %v", s)
	}
}
