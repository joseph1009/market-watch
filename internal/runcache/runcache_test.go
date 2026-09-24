package runcache

import (
	"context"
	"encoding/json"
	"errors"
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
	_, other := c.Start(context.Background(), Analysis, "MU")
	other.Save("snapshot", map[string]string{"ticker": "MU"})

	_, second := c.Start(context.Background(), Brief, "")
	second.Save("prices", []int{1})

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

// A step that runs twice keeps both, and files go into subfolders by name.
func TestSaveNumbersARepeatedName(t *testing.T) {
	root := t.TempDir()
	_, e := (&Cache{Root: root}).Start(context.Background(), Recommendations, "")
	e.Save("verdicts", []string{"first round"})
	e.Save("verdicts", []string{"stand-ins"})
	e.Text("model/01-ideas-request.txt", "the prompt")

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
