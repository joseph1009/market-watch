// Package triage judges articles by what they say rather than by which words
// they contain.
//
// Matching is precise but literal: a drone strike on a Saudi pipeline names no
// company the reader follows, and ranking the unmatched remainder by source
// weight kept routine sanctions notices over a surge in oil and borrowing
// costs. So a model reads every article once, rates its market relevance and
// places it in the sectors its substance bears on, judged against each
// sector's description (config/sectors.yaml). The sorting only adds to the
// matches by company name and ticker; the review (review.go) then takes a
// second look at where everything that will reach the brief ended up, and can
// move any of it. Both are Opus 5.5, as every stage is: with the keywords
// gone, more of the placing rests on reading.
package triage

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// Completer is the one model call triage needs, so batching, parsing and
// failure handling are testable without the network.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

const (
	// DefaultBatchSize keeps each reply short enough that the model does not
	// lose its place in the numbering.
	DefaultBatchSize = 60

	// DefaultConcurrency bounds parallel requests. A day's articles fit in
	// about nine batches, so three at a time finishes in well under a minute.
	DefaultConcurrency = 3

	// DefaultTimeout caps the whole pass. Triage improves the brief but is not
	// required for one, so it must never be what makes the brief late.
	DefaultTimeout = 3 * time.Minute

	// summaryRunes is enough of the summary to tell a feature from a shock
	// without paying to send the whole thing.
	summaryRunes = 300

	maxHints = 6

	// MinPlacementRating is the lowest rating whose watchlist placement is
	// accepted. The model places generously: on its first real run it put 244
	// articles in watchlists, and those rated 2 or 3 were mostly stretches --
	// a Kosovo coalition in geopolitics, FDA index pages in healthcare, AI
	// opinion columns in big tech. The ones rated 4 and 5 were the stories
	// keywords genuinely missed. Below this, an article still informs the
	// overview; it just does not pad a section.
	MinPlacementRating = 4

	// ReserveRating is the rating whose placements are kept in reserve rather
	// than made: one short of MinPlacementRating. On 24 September the sorting
	// rated every crypto story 2 or 3 -- bitcoin under $84,000, the NYSE
	// planning tokenised shares -- and the section went missing that the
	// morning's brief had filled with eleven. A 3 is "relevant, with limited
	// near-term effect": not enough to add to a full section, enough to fill
	// an empty one, once the review agrees (TopUp).
	ReserveRating = MinPlacementRating - 1

	// MaxPlacements is how many watchlists one article may belong to.
	//
	// A watchlist described as a sector rather than a list of tickers is a
	// wider net, and the same story starts to fit several: export controls on
	// chips bear on semiconductors, geopolitics, big tech and industrials at
	// once. Placed in all four it is written up four times, in four sections,
	// saying the same thing. Two keeps the cross-sector link that makes a
	// story worth reading twice, and stops the third and fourth retelling.
	//
	// Keyword matches count towards it and are never removed: the reader named
	// those subjects, so an article that already matched two of them keeps
	// both and takes nothing further from judgment.
	MaxPlacements = 2
)

// Triager rates and places articles.
type Triager struct {
	Completer   Completer
	BatchSize   int
	Concurrency int
	Timeout     time.Duration

	// Memory gives an article the verdict it had before, rather than asking
	// again. Nil asks about every article.
	Memory *Memory

	// Now is injected for tests.
	Now func() time.Time
}

// Triage returns a copy of articles, in the same order, with Rating set and
// watchlist placements added. Failure is partial, never total: articles in a
// batch that failed come back unrated and unchanged, and the error says how
// many batches that was. The caller carries on either way.
func (t *Triager) Triage(ctx context.Context, articles []model.Article, groups []model.Group) ([]model.Article, model.Usage, error) {
	out := make([]model.Article, len(articles))
	copy(out, articles)
	if len(out) == 0 || t.Completer == nil {
		return out, model.Usage{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()

	known := make(map[string]bool, len(groups))
	for _, g := range groups {
		known[g.ID] = true
	}
	system, err := systemPrompt(groups)
	if err != nil {
		return out, model.Usage{}, err
	}

	// What an earlier pass judged under this same prompt is judged again
	// from memory, and only the rest go to the model, in a copy of their own.
	key := promptKey(system)
	var pending []int
	for i := range out {
		if v, ok := t.Memory.recallFor(out[i].ID, key); ok {
			apply(out[i:i+1], map[int]verdict{1: v}, known)
			continue
		}
		pending = append(pending, i)
	}
	ask := make([]model.Article, len(pending))
	for j, i := range pending {
		ask[j] = out[i]
	}

	size := t.batchSize()
	var bounds [][2]int
	for start := 0; start < len(ask); start += size {
		bounds = append(bounds, [2]int{start, min(start+size, len(ask))})
	}

	type outcome struct {
		usage model.Usage
		err   error
	}
	outcomes := make([]outcome, len(bounds))
	sem := make(chan struct{}, t.concurrency())
	var wg sync.WaitGroup

	for i, b := range bounds {
		wg.Add(1)
		// Each batch is a disjoint window onto ask, so the goroutines write to
		// separate elements and need no lock.
		go func(i int, batch []model.Article) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			text, usage, err := t.Completer.Complete(ctx, system, userPrompt(batch))
			// A reply cut off partway still carries valid verdicts for the
			// items it reached, so they are applied even alongside an error.
			verdicts := parse(text, len(batch))
			apply(batch, verdicts, known)
			for n, v := range verdicts {
				t.Memory.rememberAt(batch[n-1].ID, key, v, t.now())
			}
			// Fewer verdicts than items is a failure even without an error: a
			// model that drifts from the format would otherwise leave
			// articles unrated with nothing in the log to say so.
			if err == nil && len(verdicts) < len(batch) {
				err = fmt.Errorf("reply rated %d of %d articles", len(verdicts), len(batch))
			}
			outcomes[i] = outcome{usage: usage, err: err}
		}(i, ask[b[0]:b[1]])
	}
	wg.Wait()
	for j, i := range pending {
		out[i] = ask[j]
	}
	// A memory that cannot be written costs the next brief its saving, never
	// this one its ratings.
	_ = t.Memory.saveAt(t.now(), len(out)-len(pending))

	var usage model.Usage
	failed := 0
	var first error
	for _, o := range outcomes {
		usage.InputTokens += o.usage.InputTokens
		usage.OutputTokens += o.usage.OutputTokens
		if o.err != nil {
			failed++
			if first == nil {
				first = o.err
			}
		}
	}
	if failed > 0 {
		return out, usage, fmt.Errorf("%d of %d triage batches failed: %w", failed, len(bounds), first)
	}
	return out, usage, nil
}

// verdict is the model's judgment of one article.
type verdict struct {
	rating int
	groups []string
}

var verdictLine = regexp.MustCompile(`^\s*(\d+)\s*\|\s*([1-5])\s*\|\s*(.*?)\s*$`)

// parse reads "number|rating|ids" lines, keyed by the 1-based item number.
// Anything that does not fit -- prose, an out-of-range number, a rating outside
// 1..5 -- is skipped, leaving that article unrated rather than misjudged.
func parse(text string, items int) map[int]verdict {
	out := make(map[int]verdict)
	for _, line := range strings.Split(text, "\n") {
		m := verdictLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > items {
			continue
		}
		rating, _ := strconv.Atoi(m[2])

		var groups []string
		if field := strings.TrimSpace(m[3]); field != "" && field != "-" {
			for _, id := range strings.Split(field, ",") {
				if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
					groups = append(groups, id)
				}
			}
		}
		out[n] = verdict{rating: rating, groups: groups}
	}
	return out
}

// apply writes verdicts onto a batch. Unknown watchlist ids are ignored, since a
// section for a watchlist that does not exist has nowhere to go, and existing
// placements are only ever added to.
func apply(batch []model.Article, verdicts map[int]verdict, known map[string]bool) {
	for n, v := range verdicts {
		a := &batch[n-1]
		a.Rating = v.rating
		if v.rating == ReserveRating {
			for _, id := range v.groups {
				if len(a.Reserve) < MaxPlacements && known[id] && !a.InGroup(id) && !slices.Contains(a.Reserve, id) {
					a.Reserve = append(slices.Clip(a.Reserve), id)
				}
			}
		}
		if v.rating < MinPlacementRating {
			continue
		}
		for _, id := range v.groups {
			if len(a.GroupIDs) >= MaxPlacements {
				// The reply lists its best fit first, so what is dropped here
				// is the weakest link the model saw.
				break
			}
			if known[id] && !a.InGroup(id) {
				// Clip first: GroupIDs is shared with the caller's copy of the
				// article, and appending into spare capacity would change it.
				a.GroupIDs = append(slices.Clip(a.GroupIDs), id)
			}
		}
	}
}

// systemPrompt is the triage brief with the reader's sectors written into it.
// Its text lives in config/prompts.md.
func systemPrompt(groups []model.Group) (string, error) {
	return config.RenderPrompt("triage.system", struct{ Watchlists string }{
		Watchlists: describeSectors(groups),
	})
}

// describeSectors lists the sectors for the sorting and the review: each by
// its description, then a few of its companies.
func describeSectors(groups []model.Group) string {
	var list strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&list, "- %s: %s", g.ID, g.Name)
		// The description first, the companies after it. That order is the
		// instruction: judge the article against what the sector covers, and
		// read the companies as some of what lives there rather than as the
		// whole of it.
		if g.About != "" {
			fmt.Fprintf(&list, " -- %s", g.About)
		}
		if hints := groupHints(g); len(hints) > 0 {
			fmt.Fprintf(&list, " (for example %s)", strings.Join(hints, ", "))
		}
		list.WriteString("\n")
	}
	return strings.TrimRight(list.String(), "\n")
}

// groupHints names some of the companies a sector follows, as examples beside
// its description.
func groupHints(g model.Group) []string {
	hints := make([]string, 0, maxHints)
	for _, c := range g.Companies {
		if len(hints) == maxHints {
			break
		}
		hints = append(hints, c.Name)
	}
	return hints
}

func userPrompt(batch []model.Article) string {
	var b strings.Builder
	for i, a := range batch {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, a.SourceName, oneLine(a.Title))
		if s := clip(oneLine(a.Summary), summaryRunes); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	return b.String()
}

// oneLine collapses whitespace, so a newline inside a headline cannot start
// what looks like a new numbered item.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (t *Triager) batchSize() int {
	if t.BatchSize > 0 {
		return t.BatchSize
	}
	return DefaultBatchSize
}

func (t *Triager) concurrency() int {
	if t.Concurrency > 0 {
		return t.Concurrency
	}
	return DefaultConcurrency
}

func (t *Triager) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Triager) timeout() time.Duration {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return DefaultTimeout
}
