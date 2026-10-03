package triage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// counting answers each article it is asked about by its title, and counts
// the articles asked about.
type counting struct {
	asked []string
	fail  bool
}

func (c *counting) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	if c.fail {
		return "", model.Usage{}, errors.New("timed out")
	}
	var lines []string
	n := 0
	for _, line := range strings.Split(prompt, "\n") {
		if !strings.Contains(line, ". [") {
			continue
		}
		n++
		c.asked = append(c.asked, line)
		switch {
		case strings.Contains(line, "pipeline"):
			lines = append(lines, itoa(n)+"|5|energy")
		default:
			lines = append(lines, itoa(n)+"|2|-")
		}
	}
	return strings.Join(lines, "\n"), model.Usage{}, nil
}

func itoa(n int) string { return string(rune('0' + n)) }

func memoryAt(t *testing.T) (*Memory, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sorted.json")
	m, err := LoadMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}

// An article sorted by an earlier brief keeps its verdict, read back from
// the file, and only the new one goes to the model.
func TestAnArticleSortedBeforeIsNotSortedAgain(t *testing.T) {
	m, path := memoryAt(t)
	day := time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)
	first := &counting{}
	tr := &Triager{Completer: first, Memory: m, Now: func() time.Time { return day }}
	yesterday := []model.Article{{ID: "a", Title: "Drone strikes hit Saudi pipeline"}, {ID: "b", Title: "Board names a director"}}
	if _, _, err := tr.Triage(context.Background(), yesterday, testGroups()); err != nil {
		t.Fatal(err)
	}

	again, err := LoadMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	second := &counting{}
	tr = &Triager{Completer: second, Memory: again, Now: func() time.Time { return day.Add(24 * time.Hour) }}
	today := append(yesterday, model.Article{ID: "c", Title: "Fed holds rates"})
	got, _, err := tr.Triage(context.Background(), today, testGroups())
	if err != nil {
		t.Fatal(err)
	}

	if len(second.asked) != 1 || !strings.Contains(second.asked[0], "Fed holds rates") {
		t.Errorf("asked about %q, want only the new article", second.asked)
	}
	if got[0].Rating != 5 || !got[0].InGroup("energy") || got[1].Rating != 2 || got[2].Rating != 2 {
		t.Errorf("got %+v, want yesterday's verdicts given again", got)
	}
	if again.Recalled() != 2 {
		t.Errorf("recalled %d, want 2", again.Recalled())
	}
}

// A change to a sector's description is a new prompt, so everything is
// sorted again.
func TestChangedSectorsSortEverythingAgain(t *testing.T) {
	m, _ := memoryAt(t)
	articles := []model.Article{{ID: "a", Title: "Drone strikes hit Saudi pipeline"}}
	tr := &Triager{Completer: &counting{}, Memory: m}
	if _, _, err := tr.Triage(context.Background(), articles, testGroups()); err != nil {
		t.Fatal(err)
	}

	groups := testGroups()
	groups[0].About = "Oil and gas only."
	second := &counting{}
	tr.Completer = second
	if _, _, err := tr.Triage(context.Background(), articles, groups); err != nil {
		t.Fatal(err)
	}
	if len(second.asked) != 1 {
		t.Errorf("asked about %d articles, want the one sorted under the old sectors", len(second.asked))
	}
}

// An article whose batch failed has no verdict to keep, so the next brief
// asks about it.
func TestAFailedBatchIsAskedAgain(t *testing.T) {
	m, _ := memoryAt(t)
	articles := []model.Article{{ID: "a", Title: "Drone strikes hit Saudi pipeline"}}
	tr := &Triager{Completer: &counting{fail: true}, Memory: m}
	if _, _, err := tr.Triage(context.Background(), articles, testGroups()); err == nil {
		t.Fatal("want the failed batch reported")
	}

	second := &counting{}
	tr.Completer = second
	if _, _, err := tr.Triage(context.Background(), articles, testGroups()); err != nil {
		t.Fatal(err)
	}
	if len(second.asked) != 1 {
		t.Errorf("asked about %d articles, want the one that failed", len(second.asked))
	}
}

// A verdict older than the week an article may stay is forgotten.
func TestAVerdictAgesOut(t *testing.T) {
	m, _ := memoryAt(t)
	day := time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)
	articles := []model.Article{{ID: "a", Title: "Drone strikes hit Saudi pipeline"}}
	tr := &Triager{Completer: &counting{}, Memory: m, Now: func() time.Time { return day }}
	if _, _, err := tr.Triage(context.Background(), articles, testGroups()); err != nil {
		t.Fatal(err)
	}

	second := &counting{}
	tr = &Triager{Completer: second, Memory: m, Now: func() time.Time { return day.Add(MemoryRetention + time.Hour) }}
	if _, _, err := tr.Triage(context.Background(), []model.Article{{ID: "z", Title: "Fed holds rates"}}, testGroups()); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.recallFor("a", promptKeyFor(t, testGroups())); ok {
		t.Error("a verdict past its retention was kept")
	}
}

func promptKeyFor(t *testing.T, groups []model.Group) string {
	t.Helper()
	system, err := systemPrompt(groups)
	if err != nil {
		t.Fatal(err)
	}
	return promptKey(system)
}
