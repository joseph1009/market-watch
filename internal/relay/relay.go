// Package relay carries every model call the service makes.
//
// A call is written to disk as a request, answered, and the answer written
// beside it, with a ledger saying what was asked and what came back. That was
// first built as a way to test the brief without API credit, answered by hand
// from a Claude Code session. It turned out to be the better design outright:
// every prompt the service sends and every reply it gets is a file that can be
// read, diffed and re-run, and who does the answering is a detail.
//
// Two answerers exist. Claude runs Claude Code headless, once per call, with
// the model the stage asks for -- a fresh process each time, so nothing
// accumulates between calls and there is no session to clear. Session waits for
// a reply file written by someone else, which is how a run is watched and
// answered by hand.
package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
)

// The stages, named as they appear in the files.
const (
	Triage   = "triage"   // rating and placing the day's articles
	Brief    = "brief"    // writing the brief
	Names    = "names"    // spotting companies no watchlist tracks
	Analysis = "analysis" // writing up one company's accounts
)

// DefaultKeep is how many run directories are kept. A month of daily briefs
// plus whatever analyses were asked for in between, at a few hundred kilobytes
// each.
const DefaultKeep = 40

// Question is one model call on its way to an answerer.
type Question struct {
	Stage  string
	Dir    string // the run's directory
	Base   string // the path both files share, without the -request/-reply suffix
	System string
	Prompt string
}

// RequestPath and ReplyPath are where the call and its answer live.
func (q Question) RequestPath() string { return q.Base + "-request.txt" }
func (q Question) ReplyPath() string   { return q.Base + "-reply.txt" }

// Reply is an answer, with what it took to produce.
type Reply struct {
	Text  string
	Model string
	Usage model.Usage
}

// Answerer produces the reply to one question.
type Answerer interface {
	Answer(ctx context.Context, q Question) (Reply, error)
}

// Relay opens runs and hands out the completers the rest of the service calls.
type Relay struct {
	// Root holds one directory per run.
	Root   string
	Answer Answerer

	// Keep bounds how many run directories stay on disk. Zero means DefaultKeep.
	Keep int

	Log func(format string, args ...any)
	Now func() time.Time
}

type runKey struct{}

// Begin opens a run -- a brief, or one company's analysis -- and returns a
// context carrying it. Every call made with that context lands in the run's
// directory, numbered in the order it was asked.
func (r *Relay) Begin(ctx context.Context, kind string) (context.Context, *Run, error) {
	run, err := r.open(kind)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, runKey{}, run), run, nil
}

func (r *Relay) open(kind string) (*Run, error) {
	if err := os.MkdirAll(r.Root, 0o755); err != nil {
		return nil, fmt.Errorf("relay directory: %w", err)
	}
	r.prune()

	name := r.now().Format("20060102-150405") + "-" + safe(kind)
	dir := filepath.Join(r.Root, name)
	for i := 2; ; i++ {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("relay run: %w", err)
		}
		dir = filepath.Join(r.Root, fmt.Sprintf("%s-%d", name, i))
	}

	run := &Run{Dir: dir, answer: r.Answer, log: r.logf}
	run.Note("## %s, %s", kind, r.now().Format("Mon 2 Jan 2006 15:04 MST"))
	return run, nil
}

// prune removes the oldest run directories beyond Keep. Names begin with a
// timestamp, so sorting them sorts them by age.
func (r *Relay) prune() {
	keep := r.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	entries, err := os.ReadDir(r.Root)
	if err != nil {
		return
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() {
			runs = append(runs, e.Name())
		}
	}
	sort.Strings(runs)
	// One place is left for the run about to open.
	for len(runs) >= keep {
		if err := os.RemoveAll(filepath.Join(r.Root, runs[0])); err != nil {
			r.logf("could not prune %s: %v", runs[0], err)
		}
		runs = runs[1:]
	}
}

// ask routes a call to the run in the context. A call made outside any run
// still gets one of its own, so a code path that forgot to open a run loses
// nothing but its grouping.
func (r *Relay) ask(ctx context.Context, stage, system, prompt string) (Reply, error) {
	run, ok := ctx.Value(runKey{}).(*Run)
	if !ok {
		var err error
		if run, err = r.open(stage); err != nil {
			return Reply{}, err
		}
	}
	return run.Ask(ctx, stage, system, prompt)
}

// Stage is the relay as the brief and the analysis call it.
func (r *Relay) Stage(stage string) report.Completer { return completion{r, stage} }

// TextCompleter is the call shape sorting and name-spotting use.
type TextCompleter interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

// Plain is the relay as sorting and name-spotting call it: text and usage.
func (r *Relay) Plain(stage string) TextCompleter { return plain{r, stage} }

type completion struct {
	r     *Relay
	stage string
}

func (c completion) Complete(ctx context.Context, system, prompt string) (report.Completion, error) {
	reply, err := c.r.ask(ctx, c.stage, system, prompt)
	return report.Completion{Text: reply.Text, Model: reply.Model, Usage: reply.Usage}, err
}

type plain struct {
	r     *Relay
	stage string
}

func (p plain) Complete(ctx context.Context, system, prompt string) (string, model.Usage, error) {
	reply, err := p.r.ask(ctx, p.stage, system, prompt)
	return reply.Text, reply.Usage, err
}

func (r *Relay) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

func (r *Relay) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run is one brief or one analysis: a directory of numbered requests and
// replies, and a ledger.
type Run struct {
	Dir string

	answer Answerer
	log    func(format string, args ...any)
	n      atomic.Int32
	mu     sync.Mutex // guards the ledger, which concurrent calls append to
}

// Ask writes a request, has it answered, and writes the answer beside it.
func (run *Run) Ask(ctx context.Context, stage, system, prompt string) (Reply, error) {
	q := Question{
		Stage:  stage,
		Dir:    run.Dir,
		Base:   filepath.Join(run.Dir, fmt.Sprintf("%02d-%s", run.n.Add(1), stage)),
		System: system,
		Prompt: prompt,
	}
	body := "===== SYSTEM =====\n" + system + "\n\n===== PROMPT =====\n" + prompt + "\n"
	if err := os.WriteFile(q.RequestPath(), []byte(body), 0o644); err != nil {
		return Reply{}, err
	}
	run.Note("- [ ] %s asked, %s to read in %s", stage, Size(len(body)), filepath.Base(q.RequestPath()))

	reply, err := run.answer.Answer(ctx, q)
	if err != nil {
		run.Note("- [!] %s failed: %s", stage, oneLine(err.Error()))
		return Reply{}, err
	}
	if strings.TrimSpace(reply.Text) == "" {
		run.Note("- [!] %s came back empty", stage)
		return Reply{}, fmt.Errorf("%s: the reply was empty", stage)
	}

	// An answerer that took its reply from a file has already written it; one
	// that produced it has not. Either way the reply is on disk afterwards.
	if _, err := os.Stat(q.ReplyPath()); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(q.ReplyPath(), []byte(reply.Text), 0o644); err != nil {
			run.log("could not keep the %s reply: %v", stage, err)
		}
	}
	via := ""
	if reply.Model != "" {
		via = " by " + reply.Model
	}
	run.Note("- [x] %s answered%s, %s in %s", stage, via, Size(len(reply.Text)), filepath.Base(q.ReplyPath()))
	return reply, nil
}

// Note appends a line to the run's ledger.
//
// The ledger is what lets a run be understood afterwards, and picked up again
// when it is answered by hand: it says which stages were asked, which came
// back, which failed and how large each was, from the files on disk rather than
// from anybody's memory.
func (run *Run) Note(format string, args ...any) {
	line := fmt.Sprintf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	run.mu.Lock()
	defer run.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(run.Dir, "ledger.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		run.log("ledger: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		run.log("ledger: %v", err)
	}
}

// Size reads a byte count the way a person judges whether a request is small
// enough to read directly or wants a subagent of its own.
func Size(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d bytes", bytes)
	}
	return fmt.Sprintf("%.0fKB", float64(bytes)/1024)
}

// safe keeps a run's name to characters every filesystem accepts.
func safe(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}
