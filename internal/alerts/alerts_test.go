package alerts

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

var now = time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)

func stories() []model.Article {
	return []model.Article{
		{ID: "a", Title: "Fed cuts rates by half a point in emergency move", SourceName: "Reuters", URL: "https://r.example/fed", Published: now.Add(-5 * time.Minute)},
		{ID: "b", Title: "Nvidia agrees to buy Arm for $60 billion", SourceName: "CNBC", URL: "https://c.example/nvda", Published: now.Add(-10 * time.Minute), Tickers: []string{"NVDA"}},
		{ID: "c", Title: "Ten cheap stocks to buy now", SourceName: "Blog", URL: "https://b.example/ten", Published: now.Add(-15 * time.Minute)},
	}
}

type fakeModel struct {
	reply  string
	prompt string
}

func (f *fakeModel) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	f.prompt = prompt
	return f.reply, model.Usage{}, nil
}

// The reply's blocks become alerts with the stories they point to. A block
// pointing at no story, or with no headline, is dropped, and so is a number
// out of range.
func TestPickReadsTheAlertBlocks(t *testing.T) {
	m := &fakeModel{reply: `ALERT: 1
HEADLINE: The Fed cut rates by **half a point** in an emergency move
WHAT: Borrowing gets cheaper at once, and markets read it as a sign of trouble.

ALERT: 2, 9
HEADLINE: Nvidia agreed to buy Arm for $60 billion
WHAT: Its biggest deal yet.

ALERT: 12
HEADLINE: A story that is not there
WHAT: Dropped.

ALERT: 3
WHAT: No headline, dropped.`}
	groups := []model.Group{{ID: "semis", Name: "Semiconductors", Companies: []model.Company{{Symbol: "NVDA", Name: "Nvidia"}}}}
	sent := []Sent{{At: now.Add(-time.Hour), Headline: "Oil jumped 8% after an attack in the Gulf"}}

	got, _, err := (&Picker{Completer: m}).Pick(context.Background(), stories(), groups, sent, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d alerts, want 2: %+v", len(got), got)
	}
	if got[0].Articles[0].ID != "a" || got[1].Articles[0].ID != "b" || len(got[1].Articles) != 1 {
		t.Errorf("alerts point at the wrong stories: %+v", got)
	}
	for _, want := range []string{"Nvidia (NVDA)", "Oil jumped 8%", "[1] 12:55 Reuters: Fed cuts rates", "[mentions NVDA]"} {
		if !strings.Contains(m.prompt, want) {
			t.Errorf("the request is missing %q:\n%s", want, m.prompt)
		}
	}
}

func TestNoneMeansNoAlerts(t *testing.T) {
	got, _, err := (&Picker{Completer: &fakeModel{reply: "NONE"}}).Pick(context.Background(), stories(), nil, nil, now, time.UTC)
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want nothing", got, err)
	}
}

// The log remembers what it has seen and sent across a restart, and forgets
// what is old enough not to matter.
func TestTheLogRemembersAndForgets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.json")
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Started() {
		t.Error("an empty log says the watch has run")
	}
	all := stories()
	if err := l.MarkSeen(all[:2], now); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Sent{At: now, Headline: "The Fed cut rates"}); err != nil {
		t.Fatal(err)
	}

	l, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Started() {
		t.Error("the watch forgot it had run")
	}
	if unseen := l.Unseen(all); len(unseen) != 1 || unseen[0].ID != "c" {
		t.Errorf("unseen = %+v, want only c", unseen)
	}
	if sent := l.SentSince(now.Add(-time.Minute)); len(sent) != 1 {
		t.Errorf("sent = %+v, want the one", sent)
	}

	// Three days on, both are forgotten.
	later := now.Add(8 * 24 * time.Hour)
	if err := l.MarkSeen(nil, later); err != nil {
		t.Fatal(err)
	}
	if unseen := l.Unseen(all); len(unseen) != 3 {
		t.Errorf("after a week, %d stories unseen, want all 3", len(unseen))
	}
	if sent := l.SentSince(time.Time{}); len(sent) != 0 {
		t.Errorf("after a week, still remembers %+v", sent)
	}
}
