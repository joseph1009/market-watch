// Package alerts watches the news between the briefs, around the clock, and
// picks out what is big enough to tell the reader about at once: news that
// could move the whole market, and major news about a company they follow
// (since 2026-11-15, at the owner's request).
package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

const (
	// Window is how recent a story must be to be alerted on. One published
	// longer ago has had its moment, and the next brief has it.
	Window = 3 * time.Hour

	// PerDay is the most alerts sent in one day where the reader is: the
	// owner's choice, so a day of upheaval does not become a stream.
	PerDay = 8

	// MaxStories bounds one check's request. A check every quarter of an
	// hour usually finds a few dozen; more means the watch was down, and the
	// newest are the ones that matter.
	MaxStories = 80

	// summaryRunes is how much of a story's summary is shown with it.
	summaryRunes = 300

	// seenFor and sentFor are how long the log remembers a story it has
	// looked at, and an alert it has sent. Both outlast Window and a day.
	seenFor = 2 * 24 * time.Hour
	sentFor = 7 * 24 * time.Hour
)

// systemPrompt governs the choice. Its text lives in config/prompts.md.
var systemPrompt = config.Prompt("alert.system")

// Sent is one alert as the log keeps it.
type Sent struct {
	At       time.Time `json:"at"`
	Headline string    `json:"headline"`
	URL      string    `json:"url,omitempty"`
}

// Log is what the watch has looked at and sent, kept on the data volume so a
// restart neither alerts on a story twice nor resets the day's count.
type Log struct {
	mu   sync.Mutex
	path string

	// Since is when the watch first ran. Until then everything in the feeds
	// is taken as seen: it is not news to alert on.
	Since time.Time            `json:"since,omitempty"`
	Seen  map[string]time.Time `json:"seen"`
	Sent  []Sent               `json:"sent"`
}

// Load reads the log, or starts an empty one where there is none.
func Load(path string) (*Log, error) {
	l := &Log{path: path, Seen: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("alerts log %s: %w", path, err)
	}
	if l.Seen == nil {
		l.Seen = map[string]time.Time{}
	}
	return l, nil
}

// Started reports whether the watch has run before.
func (l *Log) Started() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.Since.IsZero()
}

// Unseen returns the stories not looked at before.
func (l *Log) Unseen(articles []model.Article) []model.Article {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []model.Article
	for _, a := range articles {
		if _, ok := l.Seen[a.ID]; !ok {
			out = append(out, a)
		}
	}
	return out
}

// MarkSeen records the stories as looked at. It is called once the model has
// answered, so a check that failed looks at them again the next time.
func (l *Log) MarkSeen(articles []model.Article, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Since.IsZero() {
		l.Since = now
	}
	for _, a := range articles {
		l.Seen[a.ID] = now
	}
	return l.save(now)
}

// Record adds a sent alert.
func (l *Log) Record(s Sent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Sent = append(l.Sent, s)
	return l.save(s.At)
}

// SentSince returns the alerts sent at or after t, oldest first.
func (l *Log) SentSince(t time.Time) []Sent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Sent
	for _, s := range l.Sent {
		if !s.At.Before(t) {
			out = append(out, s)
		}
	}
	return out
}

// save forgets what is old enough not to matter, and writes the log out. The
// caller holds the lock.
func (l *Log) save(now time.Time) error {
	for id, at := range l.Seen {
		if now.Sub(at) > seenFor {
			delete(l.Seen, id)
		}
	}
	kept := l.Sent[:0]
	for _, s := range l.Sent {
		if now.Sub(s.At) <= sentFor {
			kept = append(kept, s)
		}
	}
	l.Sent = kept

	if l.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// Alert is one story to tell the reader about now, in a line and a sentence
// or two, with the articles it came from.
type Alert struct {
	Headline string
	What     string
	Articles []model.Article
}

// Completer is the model.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

// Picker asks the model which of the new stories are big enough to alert on.
type Picker struct {
	Completer Completer
}

// Pick returns the stories worth an alert, most often none. sent is what was
// alerted on earlier today, so a story already told is not told again.
func (p *Picker) Pick(ctx context.Context, stories []model.Article, groups []model.Group, sent []Sent, now time.Time, display *time.Location) ([]Alert, model.Usage, error) {
	if len(stories) == 0 {
		return nil, model.Usage{}, nil
	}
	if display == nil {
		display = time.UTC
	}
	stories = newest(stories, MaxStories)
	reply, usage, err := p.Completer.Complete(ctx, systemPrompt, request(stories, groups, sent, now, display))
	if err != nil {
		return nil, usage, err
	}
	return parse(reply, stories), usage, nil
}

// newest keeps the n most recently published stories.
func newest(stories []model.Article, n int) []model.Article {
	out := append([]model.Article(nil), stories...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// request is the user turn: the time, the reader's companies, what was
// alerted on today, and the new stories, numbered.
func request(stories []model.Article, groups []model.Group, sent []Sent, now time.Time, display *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Now: %s, %s time.\n\n", now.In(display).Format("Monday 2 January 2006, 15:04"), display.String())

	names := map[string]string{}
	b.WriteString("The reader's companies, by watchlist:\n")
	for _, g := range groups {
		names[g.ID] = g.Name
		var cs []string
		for _, c := range g.Companies {
			if c.Symbol != "" {
				cs = append(cs, c.Name+" ("+c.Symbol+")")
			} else {
				cs = append(cs, c.Name)
			}
		}
		fmt.Fprintf(&b, "- %s: %s\n", g.Name, strings.Join(cs, ", "))
	}

	b.WriteString("\nAlready alerted on today:\n")
	if len(sent) == 0 {
		b.WriteString("(none)\n")
	}
	for _, s := range sent {
		fmt.Fprintf(&b, "- %s %s\n", s.At.In(display).Format("15:04"), s.Headline)
	}

	fmt.Fprintf(&b, "\nNew stories, %d, numbered:\n", len(stories))
	for i, a := range stories {
		fmt.Fprintf(&b, "[%d] %s %s: %s", i+1, a.Published.In(display).Format("15:04"), a.SourceName, a.Title)
		if s := clip(strings.TrimSpace(a.Summary), summaryRunes); s != "" {
			b.WriteString(" -- " + s)
		}
		var about []string
		for _, id := range a.GroupIDs {
			about = append(about, names[id])
		}
		if len(a.Tickers) > 0 || len(about) > 0 {
			fmt.Fprintf(&b, " [mentions %s]", strings.Join(append(a.Tickers, about...), ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parse reads the reply's ALERT blocks. A block with no story it can point to,
// or no headline, is dropped: an alert has to say what happened and where.
func parse(reply string, stories []model.Article) []Alert {
	var (
		out []Alert
		cur *Alert
	)
	flush := func() {
		if cur != nil && cur.Headline != "" && len(cur.Articles) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "ALERT":
			flush()
			cur = &Alert{}
			seen := map[int]bool{}
			for _, f := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' }) {
				n, err := strconv.Atoi(strings.Trim(f, "[]"))
				if err == nil && n >= 1 && n <= len(stories) && !seen[n] {
					seen[n] = true
					cur.Articles = append(cur.Articles, stories[n-1])
				}
			}
		case "HEADLINE":
			if cur != nil {
				cur.Headline = value
			}
		case "WHAT":
			if cur != nil {
				cur.What = value
			}
		}
	}
	flush()
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
