package ideas

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// Backcheck looks again at the calls going the wrong way, and asks why: the
// whole market, the share's industry, news that came after the call, or a
// case that was wrong on what was known when it was made. What it finds is
// written on the record, shown under /scorecard, and given back to the
// verdicts with the lessons drawn from the misjudged ones, so the next calls
// are made knowing how the last ones went (2026-10-14).
type Backcheck struct {
	Completer Completer

	// Batch is how many calls one request checks, and Concurrency how many
	// requests run at once.
	Batch       int
	Concurrency int
}

// Miss is a call going the wrong way, with what the back-check needs to see
// it: how far the share and the index have moved since, and the share's
// price then and now in its own currency.
type Miss struct {
	Record       Record
	Gain, Index  float64
	Called       float64
	Entry, Price float64
}

// backcheckPrompt governs the back-check. Its text lives in
// config/prompts.md.
var backcheckPrompt = config.Prompt("backcheck.system")

// Check asks why each miss went the wrong way, and returns what was found by
// record key. A batch that fails costs its own calls; the error says how many
// batches that was, and the rest are returned.
func (b *Backcheck) Check(ctx context.Context, misses []Miss, today time.Time) (map[string]Review, model.Usage, error) {
	out := map[string]Review{}
	if b == nil || b.Completer == nil || len(misses) == 0 {
		return out, model.Usage{}, nil
	}
	size, conc := b.Batch, b.Concurrency
	if size <= 0 {
		size = DefaultBatch
	}
	if conc <= 0 {
		conc = DefaultJudgeConcurrency
	}

	var (
		mu     sync.Mutex
		usage  model.Usage
		failed int
		first  error
		wg     sync.WaitGroup
	)
	sem := make(chan struct{}, conc)
	batches := 0
	for start := 0; start < len(misses); start += size {
		batch := misses[start:min(start+size, len(misses))]
		batches++
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				failed++
				if first == nil {
					first = ctx.Err()
				}
				mu.Unlock()
				return
			}
			text, u, err := b.Completer.Complete(ctx, backcheckPrompt, backcheckRequest(batch, today))
			mu.Lock()
			defer mu.Unlock()
			usage.InputTokens += u.InputTokens
			usage.OutputTokens += u.OutputTokens
			if err != nil {
				failed++
				if first == nil {
					first = err
				}
				return
			}
			for k, rv := range parseReviews(text) {
				rv.At = today
				out[k] = rv
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		return out, usage, fmt.Errorf("%d of %d back-check batches failed: %w", failed, batches, first)
	}
	return out, usage, nil
}

// backcheckRequest sets out each miss: the call as it was made, in its own
// words where they were kept, and what the share and the index have done
// since.
func backcheckRequest(misses []Miss, today time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\nThe calls going the wrong way:\n", today.Format("Monday 2 January 2006"))
	for _, m := range misses {
		r := m.Record
		fmt.Fprintf(&b, "\n=== %s\n", r.Key())
		describeCall(&b, r)
		unit := r.Currency
		if unit == "" {
			unit = "USD"
		}
		dollars := ""
		if !r.dollar() {
			dollars = " in US dollars"
		}
		fmt.Fprintf(&b, "Since: the share from %.2f to %.2f %s, %+.1f%%%s; the S&P 500 %+.1f%%. That is %.1f points the wrong way.\n",
			m.Entry, m.Price, unit, 100*m.Gain, dollars, 100*m.Index, -100*m.Called)
		if rv := r.Review; rv != nil {
			fmt.Fprintf(&b, "It was checked on %s, when it stood %.1f points the wrong way: %s. %s\n",
				rv.At.Format("2 January"), -100*rv.Called, causeWords[rv.Cause], rv.Why)
		}
	}
	return b.String()
}

// parseReviews reads the reply's blocks, keyed by the record key each opens
// with. A block without a cause it knows is left out.
func parseReviews(text string) map[string]Review {
	out := map[string]Review{}
	key := ""
	var cur Review
	var knowable string
	var last *string
	flush := func() {
		if key != "" {
			if c := normaliseCause(cur.Cause); c != "" {
				cur.Cause = c
				cur.Knowable = strings.HasPrefix(strings.ToLower(strings.TrimSpace(knowable)), "yes")
				if cur.Cause != CauseMisjudged {
					cur.Lesson = ""
				}
				if strings.EqualFold(strings.Trim(cur.Lesson, ". "), "none") {
					cur.Lesson = ""
				}
				out[key] = cur
			}
		}
		key, cur, knowable, last = "", Review{}, "", nil
	}
	fields := []struct {
		label string
		into  func() *string
	}{
		{"CAUSE:", func() *string { return &cur.Cause }},
		{"KNOWABLE:", func() *string { return &knowable }},
		{"WHY:", func() *string { return &cur.Why }},
		{"LESSON:", func() *string { return &cur.Lesson }},
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "===") {
			flush()
			key = strings.TrimSpace(strings.Trim(line, "= "))
			continue
		}
		if key == "" || line == "" {
			continue
		}
		matched := false
		for _, f := range fields {
			if len(line) >= len(f.label) && strings.EqualFold(line[:len(f.label)], f.label) {
				p := f.into()
				*p = strings.TrimSpace(line[len(f.label):])
				last, matched = p, true
				break
			}
		}
		if !matched && last != nil {
			*last += " " + line
		}
	}
	flush()
	return out
}

// normaliseCause reads a cause as the reply wrote it.
func normaliseCause(s string) string {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), ".*"))
	for _, c := range []string{CauseMarket, CauseSector, CauseNews, CauseMisjudged, CauseEarly} {
		if strings.HasPrefix(s, c) {
			return c
		}
	}
	switch {
	case strings.HasPrefix(s, "news"):
		return CauseNews
	case strings.HasPrefix(s, "early"):
		return CauseEarly
	}
	return ""
}

// NewMiss is a record with what the back-check is shown of it: how far the
// share and the index have moved since its entry, and the share's price then
// and now. False where it cannot be priced.
func NewMiss(r Record, now time.Time, path, bench Path, rates Rates) (Miss, bool) {
	c := assess(r, now, path, bench, rates)
	if !c.priced {
		return Miss{}, false
	}
	return Miss{Record: r, Gain: c.gain, Index: c.index, Called: c.called(), Entry: c.Entry, Price: c.last}, true
}
