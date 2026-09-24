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

// Reviewer takes a second look at where the sorting put the articles that will
// reach the brief, and moves the ones that belong in another section.
//
// The sorting judges sixty articles at a time and places each on its own; it
// never sees what else went into a section. A company name decides a
// placement before any judgment does: "Morgan Stanley sees Shell hitting new
// highs" names a bank the reader follows and is about an oil company. Without
// keywords, more of the placing rests on reading, and the review is the check
// on that reading. It changes placements only; the ratings stand.
//
// It may search the web to learn what an unfamiliar company does -- a drone
// maker nobody lists, a supplier two steps back -- where the text does not say
// and the placement depends on it.
type Reviewer struct {
	Completer   Completer
	BatchSize   int
	Concurrency int
	Timeout     time.Duration
}

// Move is one article the review placed differently, for the run record.
type Move struct {
	Title    string
	From, To []string
}

const (
	// DefaultReviewBatch is larger than the sorting's: the review writes a
	// line only for an article it moves, so the reply stays short.
	DefaultReviewBatch = 80

	// DefaultReviewTimeout caps the whole pass. A web search takes seconds, a
	// batch that uses several takes minutes, and the brief must not wait on it.
	DefaultReviewTimeout = 8 * time.Minute

	// MinReviewRating is the lowest rating reviewed. Below it an article
	// reaches no section (report.MinSectionRating), so where it sits does not
	// matter. Unrated articles are reviewed: a sorting batch that failed left
	// them placed by name alone, and this is their second chance.
	MinReviewRating = 3
)

// Review returns a copy of articles, in the same order, with the placements the
// review changed, and what it changed. Failure is partial, as the sorting's
// is: an article in a batch that failed keeps where the sorting put it.
func (r *Reviewer) Review(ctx context.Context, articles []model.Article, groups []model.Group) ([]model.Article, []Move, model.Usage, error) {
	out := make([]model.Article, len(articles))
	copy(out, articles)
	if len(out) == 0 || r.Completer == nil || len(groups) == 0 {
		return out, nil, model.Usage{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()

	known := make(map[string]bool, len(groups))
	for _, g := range groups {
		known[g.ID] = true
	}
	system, err := config.RenderPrompt("review.system", struct{ Watchlists string }{describeSectors(groups)})
	if err != nil {
		return out, nil, model.Usage{}, err
	}

	var picked []int
	for i, a := range out {
		if a.Rating == 0 || a.Rating >= MinReviewRating {
			picked = append(picked, i)
		}
	}
	var batches [][]int
	for start := 0; start < len(picked); start += r.batchSize() {
		batches = append(batches, picked[start:min(start+r.batchSize(), len(picked))])
	}

	type outcome struct {
		moves []Move
		usage model.Usage
		err   error
	}
	outcomes := make([]outcome, len(batches))
	sem := make(chan struct{}, r.concurrency())
	var wg sync.WaitGroup
	for b, batch := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			text, usage, err := r.Completer.Complete(ctx, system, reviewPrompt(out, batch))
			// Each batch owns its articles, so the writes need no lock.
			moves := applyReview(out, batch, parseReview(text, len(batch)), known)
			outcomes[b] = outcome{moves: moves, usage: usage, err: err}
		}()
	}
	wg.Wait()

	var (
		moves  []Move
		usage  model.Usage
		failed int
		first  error
	)
	for _, o := range outcomes {
		moves = append(moves, o.moves...)
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
		return out, moves, usage, fmt.Errorf("%d of %d review batches failed: %w", failed, len(batches), first)
	}
	return out, moves, usage, nil
}

// reviewPrompt lists a batch with where each article sits now.
func reviewPrompt(articles []model.Article, batch []int) string {
	var b strings.Builder
	for n, i := range batch {
		a := articles[i]
		fmt.Fprintf(&b, "%d. [%s] %s\n", n+1, a.SourceName, oneLine(a.Title))
		if s := clip(oneLine(a.Summary), summaryRunes); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
		now := "none"
		if len(a.GroupIDs) > 0 {
			now = strings.Join(a.GroupIDs, ", ")
		}
		if a.ToppedUp {
			now += " " + toppedUpMark
		}
		fmt.Fprintf(&b, "   now: %s\n", now)
	}
	return b.String()
}

var reviewLine = regexp.MustCompile(`^\s*(\d+)\s*\|\s*([a-z0-9,\s-]*?)\s*$`)

// parseReview reads "number|ids" lines, keyed by the 1-based item number, with
// "-" for none. Anything else -- prose, the word NONE, a number out of range --
// is skipped, which leaves that article where it was.
func parseReview(text string, items int) map[int][]string {
	out := make(map[int][]string)
	for _, line := range strings.Split(text, "\n") {
		m := reviewLine.FindStringSubmatch(strings.ToLower(line))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > items {
			continue
		}
		var ids []string
		if field := strings.TrimSpace(m[2]); field != "" && field != "-" {
			for _, id := range strings.Split(field, ",") {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
		}
		out[n] = ids
	}
	return out
}

// toppedUpMark follows the sections of an article TopUp added, in the review's
// list. config/prompts.md tells the review what it means, in the same words.
const toppedUpMark = "(added to fill a thin section)"

// applyReview writes the review's placements onto a batch and returns what
// moved. Unknown ids are dropped and the list is cut at MaxPlacements, as the
// sorting's are. One guard carries over from the sorting: an article the
// sorting rated 3 and placed nowhere is not given a section now, since that is
// where its placements were mostly stretches (MinPlacementRating).
//
// A top-up is the exception to "no reply means no change": it stays only
// where the review names its section, and silence withdraws it, as does a
// batch that failed. It can be kept or removed, never moved, since a section
// it moved to is not one that was short of news. None of this is a move: it
// is the review finishing the top-up, not correcting the sorting.
func applyReview(articles []model.Article, batch []int, verdicts map[int][]string, known map[string]bool) []Move {
	for n, i := range batch {
		a := &articles[i]
		if !a.ToppedUp {
			continue
		}
		var keep []string
		for _, id := range verdicts[n+1] {
			if a.InGroup(id) && !slices.Contains(keep, id) {
				keep = append(keep, id)
			}
		}
		a.GroupIDs = keep
		a.ToppedUp = len(keep) > 0
	}

	var moves []Move
	for n, ids := range verdicts {
		a := &articles[batch[n-1]]
		if a.ToppedUp {
			continue // settled above; one withdrawn there meets the guard below
		}
		var to []string
		for _, id := range ids {
			if known[id] && !slices.Contains(to, id) && len(to) < MaxPlacements {
				to = append(to, id)
			}
		}
		if len(ids) > 0 && len(to) == 0 {
			continue // named only sections that do not exist: not a verdict
		}
		if len(a.GroupIDs) == 0 && a.Rating > 0 && a.Rating < MinPlacementRating && len(to) > 0 {
			continue
		}
		if slices.Equal(a.GroupIDs, to) {
			continue
		}
		moves = append(moves, Move{Title: a.Title, From: a.GroupIDs, To: to})
		// A fresh slice: GroupIDs is shared with the caller's copy.
		a.GroupIDs = to
	}
	return moves
}

func (r *Reviewer) batchSize() int {
	if r.BatchSize > 0 {
		return r.BatchSize
	}
	return DefaultReviewBatch
}

func (r *Reviewer) concurrency() int {
	if r.Concurrency > 0 {
		return r.Concurrency
	}
	return DefaultConcurrency
}

func (r *Reviewer) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultReviewTimeout
}
