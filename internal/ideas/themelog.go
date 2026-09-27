package ideas

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The theme log is what the weekly themes remember from one week to the
// next: that this week's have been done, and what last week's research said,
// so a theme that is still running is taken further rather than written up
// again from the start.

// keptRuns is how many weeks the log keeps: half a year, which is more than
// anything reads.
const keptRuns = 26

// ThemeRun is one week's themes.
type ThemeRun struct {
	At     time.Time    `json:"at"`
	Themes []ThemeEntry `json:"themes"`
}

// ThemeEntry is one theme as it was researched.
type ThemeEntry struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Driver   string   `json:"driver"`
	Research string   `json:"research"`
	Picks    []string `json:"picks,omitempty"`
}

// ThemeLog is the record on the data volume.
type ThemeLog struct {
	Path string
	runs []ThemeRun
}

// LoadThemeLog reads the log; a missing file is an empty one.
func LoadThemeLog(path string) (*ThemeLog, error) {
	l := &ThemeLog{Path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &l.runs); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return l, nil
}

// Add records a week's themes and saves.
func (l *ThemeLog) Add(run ThemeRun) error {
	l.runs = append(l.runs, run)
	if len(l.runs) > keptRuns {
		l.runs = l.runs[len(l.runs)-keptRuns:]
	}
	data, err := json.MarshalIndent(l.runs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	tmp := l.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.Path)
}

// Last is the latest week's themes, or nil.
func (l *ThemeLog) Last() *ThemeRun {
	if len(l.runs) == 0 {
		return nil
	}
	return &l.runs[len(l.runs)-1]
}

// DoneThisWeek reports whether the themes have been run in the week of now,
// weeks counted from Monday where the market is.
func (l *ThemeLog) DoneThisWeek(now time.Time, where *time.Location) bool {
	last := l.Last()
	if last == nil {
		return false
	}
	y1, w1 := last.At.In(where).ISOWeek()
	y2, w2 := now.In(where).ISOWeek()
	return y1 == y2 && w1 == w2
}

// Names are the latest week's themes of a kind, by name.
func (l *ThemeLog) Names(kind string) []string {
	last := l.Last()
	if last == nil {
		return nil
	}
	var out []string
	for _, t := range last.Themes {
		if t.Kind == kind {
			out = append(out, t.Name)
		}
	}
	return out
}

// Previous is the latest research on a theme of this name, or "": the
// sorting is asked to reuse last week's name for a theme that continues.
func (l *ThemeLog) Previous(name string) string {
	for i := len(l.runs) - 1; i >= 0; i-- {
		for _, t := range l.runs[i].Themes {
			if strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(name)) {
				return t.Research
			}
		}
	}
	return ""
}
