// Package runcache keeps what the latest run of each kind was made from, and
// what it produced, where it can be read afterwards.
//
// A change to how a brief reads, what an analysis is given or how the closer
// look is judged used to need a whole new run to see its inputs: every feed,
// every search, every price, and a model call at each stage, to look at data
// the last run had already gathered. The cache keeps that data instead. Each
// kind of run has a folder, replaced when a run of that kind finishes, so it
// only ever holds the latest one:
//
//	cache/brief/            the daily brief
//	cache/analysis/         the latest /analyse
//	cache/recommendations/  the latest closer look
//	cache/industry/         the latest /industry
//
// A run writes into a folder of its own under .running/ and takes the kind's
// folder only when it finishes. Two runs of one kind can overlap, since
// /analyse and /industry run side by side (2026-10-03), and they may finish
// in either order. The folder keeps the one that finished last, by the time
// each run.json gives, as the owner asked: a run whose end is earlier than
// the one kept is thrown away. A run cut short, by a restart say, stays under
// .running/ until the next run of its kind starts.
//
// Each holds the data at every step as JSON, the messages as they were sent,
// the prompt and reply of every model call under model/, and run.json saying
// when the run started and finished and whether it failed. Every secret the
// configuration holds is scrubbed out of whatever is written.
//
// It is for reading, not for running on: nothing reads the cache back, and a
// failure to write it never touches the run.
package runcache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/logging"
)

// The kinds of run kept, one folder each.
const (
	Brief           = "brief"
	Analysis        = "analysis"
	Recommendations = "recommendations"
	Industry        = "industry"
)

// Cache is the directory the folders live in.
type Cache struct {
	Root string

	// Secrets are scrubbed out of every file written. A request URL with a
	// key in it can reach an article or an error, and the cache is copied off
	// the server.
	Secrets []string

	Log func(format string, args ...any)
	Now func() time.Time

	mu      sync.Mutex
	writing map[string]bool // the folders under .running/ that runs are writing to now
}

// running is the folder under Root that runs write into until they finish.
const running = ".running"

// Entry is one run being written.
type Entry struct {
	cache   *Cache
	dir     string // under .running/ until the run finishes
	kind    string
	subject string
	started time.Time

	secrets []string
	log     func(format string, args ...any)
	now     func() time.Time

	mu     sync.Mutex
	files  []string
	failed error
}

type entryKey struct{}

// Start returns a context carrying an entry for a new run of kind, and the
// entry. The run is written under .running/ and kept by Finish. Subject says
// what the run was about, such as the ticker an analysis read; it may be
// empty. A nil Cache, or one that cannot make its folder, returns a nil
// entry, whose methods do nothing.
func (c *Cache) Start(ctx context.Context, kind, subject string) (context.Context, *Entry) {
	if c == nil || c.Root == "" {
		return ctx, nil
	}
	dir, err := c.claim(kind)
	if err != nil {
		c.logf("cache: could not start a %s run: %v", kind, err)
		return ctx, nil
	}
	e := &Entry{
		cache: c, dir: dir, kind: kind, subject: subject,
		secrets: c.Secrets, log: c.Log, now: c.now,
	}
	e.started = e.now()
	e.writeRun(time.Time{})
	return context.WithValue(ctx, entryKey{}, e), e
}

// claim makes a new run's folder under .running/. Folders there of the same
// kind that no run is writing to were cut short, and go.
func (c *Cache) claim(kind string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writing == nil {
		c.writing = map[string]bool{}
	}
	parent := filepath.Join(c.Root, running)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if n := e.Name(); e.IsDir() && !c.writing[n] && strings.HasPrefix(n, kind+"-") {
			if err := os.RemoveAll(filepath.Join(parent, n)); err != nil {
				c.logf("cache: could not remove %s: %v", n, err)
			}
		}
	}
	dir, err := os.MkdirTemp(parent, kind+"-")
	if err != nil {
		return "", err
	}
	c.writing[filepath.Base(dir)] = true
	return dir, nil
}

// keep moves a run that finished at end into its kind's folder, unless the
// run kept there finished later, in which case this one is thrown away.
// Either way its folder under .running/ is gone afterwards.
func (c *Cache) keep(e *Entry, end time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.writing, filepath.Base(e.dir))

	dest := filepath.Join(c.Root, e.kind)
	if kept, ok := finishedAt(dest); ok && end.Before(kept) {
		c.logf("cache: dropped a %s run that finished before the one kept", e.kind)
		if err := os.RemoveAll(e.dir); err != nil {
			c.logf("cache: could not remove %s: %v", e.dir, err)
		}
		return
	}
	if err := os.RemoveAll(dest); err != nil {
		c.logf("cache: could not empty %s: %v", dest, err)
		return
	}
	if err := os.Rename(e.dir, dest); err != nil {
		c.logf("cache: could not keep the %s run: %v", e.kind, err)
	}
}

// finishedAt is when the run kept in dir finished, from its run.json. A run
// kept before it finished, which the cache no longer does, has no end.
func finishedAt(dir string) (time.Time, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return time.Time{}, false
	}
	var r run
	if json.Unmarshal(data, &r) != nil {
		return time.Time{}, false
	}
	return r.Finished, !r.Finished.IsZero()
}

// From returns the entry a context carries, or nil.
func From(ctx context.Context) *Entry {
	e, _ := ctx.Value(entryKey{}).(*Entry)
	return e
}

// Dir is the entry's folder.
func (e *Entry) Dir() string {
	if e == nil {
		return ""
	}
	return e.dir
}

// Save writes v as indented JSON to name.json. A name already written this run
// gets a number instead, name-2.json and so on, so a step that runs twice --
// the closer look's second round of verdicts -- keeps both.
func (e *Entry) Save(name string, v any) {
	if e == nil {
		return
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		e.logf("cache: could not encode %s: %v", name, err)
		return
	}
	e.write(name, ".json", string(data)+"\n")
}

// Text writes text to name, which carries its own extension: messages.html,
// model/01-brief-request.txt.
func (e *Entry) Text(name, text string) {
	if e == nil {
		return
	}
	ext := filepath.Ext(name)
	e.write(strings.TrimSuffix(name, ext), ext, text)
}

// Fail records why a run fell short when it still returned normally: the
// analysis of a ticker that could not be read, a closer look that was not
// delivered. Finish writes it unless given an error of its own.
func (e *Entry) Fail(err error) {
	if e == nil || err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failed == nil {
		e.failed = err
	}
}

// Finish writes run.json again with the time the run ended and, if it failed,
// why, and keeps the run in its kind's folder unless the run there finished
// later.
// Nothing should be written to the entry afterwards.
func (e *Entry) Finish(err error) {
	if e == nil {
		return
	}
	e.Fail(err)
	end := e.now()
	e.writeRun(end)
	e.cache.keep(e, end)
}

// run is what run.json holds.
type run struct {
	Kind     string    `json:"kind"`
	Subject  string    `json:"subject,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Error    string    `json:"error,omitempty"`
	Files    []string  `json:"files"`
}

func (e *Entry) writeRun(finished time.Time) {
	e.mu.Lock()
	r := run{
		Kind: e.kind, Subject: e.subject, Started: e.started, Finished: finished,
		Files: append([]string{}, e.files...),
	}
	if e.failed != nil {
		r.Error = e.failed.Error()
	}
	e.mu.Unlock()

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		e.logf("cache: could not encode run.json: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(e.dir, "run.json"), []byte(e.scrub(string(data))+"\n"), 0o644); err != nil {
		e.logf("cache: could not write run.json: %v", err)
	}
}

// write puts text in base+ext under the folder, numbering the name if this
// run has already used it.
func (e *Entry) write(base, ext, text string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	name := base + ext
	for i := 2; e.used(name); i++ {
		name = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	path := filepath.Join(e.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.logf("cache: could not write %s: %v", name, err)
		return
	}
	if err := os.WriteFile(path, []byte(e.scrub(text)), 0o644); err != nil {
		e.logf("cache: could not write %s: %v", name, err)
		return
	}
	e.files = append(e.files, name)
}

func (e *Entry) used(name string) bool {
	for _, f := range e.files {
		if f == name {
			return true
		}
	}
	return false
}

func (e *Entry) scrub(s string) string { return logging.Scrub(s, e.secrets...) }

func (e *Entry) logf(format string, args ...any) {
	if e.log != nil {
		e.log(format, args...)
	}
}

func (c *Cache) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ErrText is an error kept as its message, for saving alongside data: an error
// value encodes as an empty object.
func ErrText(errs ...error) []string {
	var out []string
	for _, err := range errs {
		if err != nil {
			out = append(out, err.Error())
		}
	}
	return out
}
