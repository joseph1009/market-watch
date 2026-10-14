package ideas

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// Recheck looks again, once a month, at every live call a month old or
// more, and asks whether it is still the right call from here: keep it,
// change it, or close it. A live call otherwise changes only when its company
// turns up again in the week's picks or a reaction, which most never do, or
// when it is dropped after eight weeks off course, which reads the price and
// not the case. A case broken by results, or a BUY that has run to what it
// was worth, would stay on the list until then (2026-10-15).
//
// A changed call replaces the old one as any newer verdict does, and a
// closed one shares the Replaced section of /scorecard; both keep their
// result until that day, frozen, and still count. A call can be ended, but
// not erased.
type Recheck struct {
	Completer Completer

	// Batch is how many calls one request looks at, and Concurrency how many
	// requests run at once.
	Batch       int
	Concurrency int
}

// RecheckAge is how old a live call must be before it is looked at again:
// younger, nothing much has happened to it since it was made.
const RecheckAge = 28 * 24 * time.Hour

// recheckGap is how soon a call looked at once may be looked at again, so
// a month whose re-check ran late does not look at the same calls twice.
const recheckGap = 21 * 24 * time.Hour

// What the re-check decided for a call.
const (
	RecheckKeep   = "keep"
	RecheckChange = "change"
	RecheckClose  = "close"
)

// Rechecked is what the re-check decided for a call, kept on the record: the
// day, what it did, and why.
type Rechecked struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	Why    string    `json:"why"`
}

// Live is a live call as the re-check is shown it: how far the share and
// the index have moved since its entry, its price then and now, and how
// long it has been off course. Priced is false for a BUY BELOW still
// waiting for its price, and for a share that could not be priced.
type Live struct {
	Record       Record
	Priced       bool
	Gain, Index  float64
	Called       float64
	OnCourse     bool
	Entry, Price float64
	Off          time.Duration
}

// NewLive is a live call with what the re-check is shown of it.
func NewLive(r Record, now time.Time, path, bench Path, rates Rates) Live {
	c := assess(r, now, path, bench, rates)
	l := Live{Record: r, Price: c.last, Off: c.off}
	if c.priced {
		l.Priced, l.Gain, l.Index, l.Called, l.OnCourse, l.Entry = true, c.gain, c.index, c.called(), c.onCourse(), c.Entry
	}
	return l
}

// Rethink is the re-check's answer on one call. Verdict, Confidence, Entry,
// Case, Catalyst and Risk are the new call's, for a change only.
type Rethink struct {
	Action     string
	Why        string
	Verdict    string
	Confidence string
	Entry      float64
	Case       string
	Catalyst   string
	Risk       string
}

// recheckPrompt governs the re-check. Its text lives in config/prompts.md.
var recheckPrompt = config.Prompt("recheck.system")

// Check asks of each call whether it still stands, and returns the answers
// by record key. record is the scorecard's RecordNote, so the re-check knows
// how calls like these have done. A batch that fails costs its own calls;
// the error says how many batches that was, and the rest are returned.
func (rc *Recheck) Check(ctx context.Context, calls []Live, record string, today time.Time) (map[string]Rethink, model.Usage, error) {
	out := map[string]Rethink{}
	if rc == nil || rc.Completer == nil || len(calls) == 0 {
		return out, model.Usage{}, nil
	}
	size, conc := rc.Batch, rc.Concurrency
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
	for start := 0; start < len(calls); start += size {
		batch := calls[start:min(start+size, len(calls))]
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
			text, u, err := rc.Completer.Complete(ctx, recheckPrompt, recheckRequest(batch, record, today))
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
			for k, rt := range parseRethinks(text) {
				out[k] = rt
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		return out, usage, fmt.Errorf("%d of %d re-check batches failed: %w", failed, batches, first)
	}
	return out, usage, nil
}

// recheckRequest sets out each live call: the call as it was made, what the
// share and the index have done since, and what the back-check found where
// it was going the wrong way.
func recheckRequest(calls []Live, record string, today time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", today.Format("Monday 2 January 2006"))
	if record != "" {
		b.WriteString(record + "\n")
	}
	b.WriteString("The live calls to look at again:\n")
	for _, l := range calls {
		r := l.Record
		fmt.Fprintf(&b, "\n=== %s\n", r.Key())
		describeCall(&b, r)
		unit := r.Currency
		if unit == "" {
			unit = "USD"
		}
		switch {
		case r.Verdict == model.BuyBelow && r.Entry == 0:
			fmt.Fprintf(&b, "It is still waiting for its price: the share last closed at %.2f %s, %.1f%% above it.\n",
				l.Price, unit, 100*(l.Price/r.Trigger-1))
		case l.Priced:
			dollars := ""
			if !r.dollar() {
				dollars = " in US dollars"
			}
			fmt.Fprintf(&b, "Since: the share from %.2f to %.2f %s, %+.1f%%%s; the S&P 500 %+.1f%%. ", l.Entry, l.Price, unit, 100*l.Gain, dollars, 100*l.Index)
			switch {
			case r.Verdict == model.Hold:
				fmt.Fprintf(&b, "That is %s points against the index", tenths(100*l.Called))
			default:
				fmt.Fprintf(&b, "That is %s points the way it called", tenths(100*l.Called))
			}
			switch weeks := int(l.Off / (7 * 24 * time.Hour)); {
			case l.OnCourse:
				b.WriteString(", on course.\n")
			case weeks >= 1:
				fmt.Fprintf(&b, ", off course for %d %s.\n", weeks, plural(weeks, "week", "weeks"))
			default:
				b.WriteString(", off course.\n")
			}
		default:
			if l.Price > 0 {
				fmt.Fprintf(&b, "It could not be scored today; the share last closed at %.2f %s.\n", l.Price, unit)
			} else {
				b.WriteString("It could not be priced today.\n")
			}
		}
		if rv := r.Review; rv != nil {
			fmt.Fprintf(&b, "The back-check on %s found: %s. %s\n", rv.At.Format("2 January"), causeWords[rv.Cause], rv.Why)
		}
		if rc := r.Rechecked; rc != nil {
			fmt.Fprintf(&b, "Looked at again on %s and kept: %s\n", rc.At.Format("2 January"), rc.Why)
		}
	}
	return b.String()
}

// describeCall writes a call as it was made, in its own words where they
// were kept, for the back-check and the re-check.
func describeCall(b *strings.Builder, r Record) {
	fmt.Fprintf(b, "%s, %s, called %s on %s", r.Name, r.Symbol, r.Verdict, r.At.Format("2 January 2006"))
	if r.Confidence != "" {
		fmt.Fprintf(b, " at %s confidence", r.Confidence)
	}
	switch r.source() {
	case SourceTheme:
		fmt.Fprintf(b, ", a weekly theme pick under %q", r.Theme)
	case SourceReaction:
		b.WriteString(", a reaction to a large move on news")
	case SourceAnalysis:
		b.WriteString(", from an analysis of its accounts")
	case SourceRecheck:
		b.WriteString(", changed to this on a monthly re-check")
	}
	b.WriteString(".\n")
	if r.Verdict == model.BuyBelow {
		if r.Entry > 0 {
			fmt.Fprintf(b, "It was a BUY BELOW %.2f, reached on %s.\n", r.Trigger, r.EntryAt.Format("2 January"))
		} else {
			fmt.Fprintf(b, "It is a BUY BELOW %.2f.\n", r.Trigger)
		}
	}
	if r.Case != "" {
		fmt.Fprintf(b, "Its case: %s\n", r.Case)
	}
	if r.Catalyst != "" {
		fmt.Fprintf(b, "The event it named: %s\n", r.Catalyst)
	}
	if r.Risk != "" {
		fmt.Fprintf(b, "The risk it named: %s\n", r.Risk)
	}
	if r.Case == "" {
		b.WriteString("Its case was not kept: find from the news of the time what the call most likely rested on.\n")
	}
}

// parseRethinks reads the reply's blocks, keyed by the record key each opens
// with. A block without an action it knows is left out, and so is a change
// without a verdict it knows: both are kept as they were.
func parseRethinks(text string) map[string]Rethink {
	out := map[string]Rethink{}
	for key, f := range parseBlocks(text, []string{"ACTION:", "WHY:", "VERDICT:", "CONFIDENCE:", "ENTRY:", "CASE:", "CATALYST:", "RISK:"}) {
		rt := Rethink{Why: f["WHY:"], Case: f["CASE:"], Catalyst: f["CATALYST:"], Risk: f["RISK:"]}
		switch a := strings.ToLower(strings.Trim(f["ACTION:"], " .*")); {
		case strings.HasPrefix(a, RecheckKeep):
			rt.Action = RecheckKeep
		case strings.HasPrefix(a, RecheckChange):
			rt.Action = RecheckChange
		case strings.HasPrefix(a, RecheckClose):
			rt.Action = RecheckClose
		default:
			continue
		}
		if rt.Action == RecheckChange {
			entry := f["ENTRY:"]
			if v := f["VERDICT:"]; entry == "" && strings.HasPrefix(strings.ToUpper(v), model.BuyBelow) {
				entry = strings.TrimSpace(v[len(model.BuyBelow):])
			}
			rt.Verdict = normaliseVerdict(f["VERDICT:"])
			if rt.Verdict == "" {
				continue
			}
			rt.Confidence = normaliseConfidence(f["CONFIDENCE:"])
			rt.Entry, _ = parseEntry(entry)
		}
		out[key] = rt
	}
	return out
}

// parseBlocks reads a reply of "=== key" blocks of labelled fields into the
// fields by key. A field runs on over the lines after its label until the
// next label.
func parseBlocks(text string, labels []string) map[string]map[string]string {
	out := map[string]map[string]string{}
	var cur map[string]string
	last := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "===") {
			key := strings.TrimSpace(strings.Trim(line, "= "))
			cur, last = map[string]string{}, ""
			if key != "" {
				out[key] = cur
			}
			continue
		}
		if cur == nil || line == "" {
			continue
		}
		matched := false
		for _, l := range labels {
			if len(line) >= len(l) && strings.EqualFold(line[:len(l)], l) {
				cur[l] = strings.TrimSpace(line[len(l):])
				last, matched = l, true
				break
			}
		}
		if !matched && last != "" {
			cur[last] += " " + line
		}
	}
	return out
}

// Changed is the new call a change makes, from the live call it replaces:
// the same company, priced at its last close. A BUY BELOW keeps the rules
// the verdicts keep: without a price under today's it is a BUY, and with one
// more than MaxBelow under it, a HOLD. False where the share has no price
// today to start the new call from.
func (l Live) Changed(rt Rethink, at time.Time, rates Rates) (Record, bool) {
	if l.Price <= 0 || rt.Verdict == "" {
		return Record{}, false
	}
	old := l.Record
	r := Record{
		At:         at,
		Name:       old.Name,
		Symbol:     old.Symbol,
		Chart:      old.Chart,
		Verdict:    rt.Verdict,
		Confidence: rt.Confidence,
		Source:     SourceRecheck,
		Theme:      old.Theme,
		Price:      l.Price,
		Currency:   old.Currency,
		FX:         rates.Latest(old.Currency),
		Case:       rt.Case,
		Risk:       rt.Risk,
		Catalyst:   rt.Catalyst,
	}
	if r.Verdict == model.BuyBelow {
		r.Verdict, r.Trigger = buyBelowRule(rt.Entry, l.Price)
	}
	return r, true
}

// buyBelowRule is the verdict a BUY BELOW at entry comes to with the share
// at last, and its price: a HOLD without a price or with one more than
// MaxBelow under today's, a BUY with one at or above it.
func buyBelowRule(entry, last float64) (string, float64) {
	switch below := 1 - entry/last; {
	case entry <= 0:
		return model.Hold, 0
	case below <= 0:
		return model.Buy, 0
	case below > MaxBelow:
		return model.Hold, 0
	}
	return model.BuyBelow, entry
}

// Live returns the live calls the re-check should look at, the longest
// since last looked at first, at most max of them: a month old or more, not
// looked at in the last three weeks, and not a BUY BELOW that lapsed without
// reaching its price.
func (s *Scorecard) Live(now time.Time, max int) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range s.records {
		if r.Closed != nil || now.Sub(r.At) < RecheckAge {
			continue
		}
		if r.Rechecked != nil && now.Sub(r.Rechecked.At) < recheckGap {
			continue
		}
		if r.Verdict == model.BuyBelow && r.Entry == 0 && now.Sub(r.At) > TriggerWindow {
			continue
		}
		out = append(out, r)
	}
	looked := func(r Record) time.Time {
		if r.Rechecked != nil {
			return r.Rechecked.At
		}
		return r.At
	}
	sort.SliceStable(out, func(i, j int) bool { return looked(out[i]).Before(looked(out[j])) })
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// ApplyRecheck writes down what the re-check decided for the live call with
// key, and saves. A kept call notes it was looked at. A closed call ends
// that day, with its result frozen as a replaced one's is. A change records
// next, the new call, which replaces the old one; a change to the same
// verdict is kept as it was. It reports whether there was such a live call.
func (s *Scorecard) ApplyRecheck(key string, at time.Time, rt Rethink, next *Record) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.records {
		r := &s.records[i]
		if r.Key() != key || r.Closed != nil {
			continue
		}
		action := rt.Action
		if action == RecheckChange && (next == nil || next.Verdict == r.Verdict) {
			action = RecheckKeep
		}
		r.Rechecked = &Rechecked{At: at, Action: action, Why: rt.Why}
		switch action {
		case RecheckClose:
			r.Closed = &Closed{At: at, Reason: ClosedRechecked}
		case RecheckChange:
			s.place(*next)
		}
		return true, s.save()
	}
	return false, nil
}

// RecheckDone is one call as the month's message lists it: the call, what
// the re-check did with it and why, how far it had gone the way it called,
// and the new verdict where it was changed.
type RecheckDone struct {
	Live
	Action string
	Why    string
	Now    string  // the new verdict, for a change
	Below  float64 // its price, for a new BUY BELOW
}

// RecheckMessages are the month's re-check, as the messages to send in turn:
// how many calls were looked at and what came of them, then each change and
// close with its reason, then the calls kept, by name.
func RecheckMessages(done []RecheckDone, missing int, where *time.Location) []string {
	n := map[string]int{}
	for _, d := range done {
		n[d.Action]++
	}
	head := fmt.Sprintf("<b>🔁 Monthly re-check</b>\n<i>Once a month, each live call a month old or more is looked at again, with the news and results since: is it still the right call from here? A changed or closed call keeps its result to today, and still counts in /scorecard.</i>\n\nLooked again at %d %s: %d kept, %d changed, %d closed.",
		len(done), plural(len(done), "call", "calls"), n[RecheckKeep], n[RecheckChange], n[RecheckClose])
	if missing > 0 {
		head += fmt.Sprintf(" %d could not be looked at this time, and stay as they were.", missing)
	}
	lines := []string{head}
	section := func(title, action string, line func(RecheckDone) string) {
		var in []RecheckDone
		for _, d := range done {
			if d.Action == action {
				in = append(in, d)
			}
		}
		if len(in) == 0 {
			return
		}
		lines = append(lines, "\n"+title)
		for _, d := range in {
			lines = append(lines, line(d))
		}
	}
	section("<b>Changed</b>", RecheckChange, func(d RecheckDone) string {
		now := d.Now
		if d.Now == model.BuyBelow {
			now += " " + call{scored: scored{Record: d.Record}}.money(d.Below)
		}
		return fmt.Sprintf("🔄 %s · %s → <b>%s</b>%s\n%s", d.label(where), d.Record.Verdict, now, d.standing(), telegram.Escape(d.Why))
	})
	section("<b>Closed</b>, no view from here", RecheckClose, func(d RecheckDone) string {
		return fmt.Sprintf("✋ %s · %s%s\n%s", d.label(where), d.Record.Verdict, d.standing(), telegram.Escape(d.Why))
	})
	var kept []string
	for _, d := range done {
		if d.Action == RecheckKeep {
			kept = append(kept, "<b>"+telegram.Escape(d.Record.Symbol)+"</b> "+d.Record.Verdict)
		}
	}
	if len(kept) > 0 {
		lines = append(lines, "\n<b>Kept</b>: "+strings.Join(kept, ", "))
	}
	return packLines(lines)
}

// label is the call's share and the day it was made.
func (d RecheckDone) label(where *time.Location) string {
	return shareLabel(d.Record.Symbol, d.Record.Name) + " · " + d.Record.At.In(where).Format("2 Jan")
}

// standing is the points the call had gone the way it called, where it was
// scored: " · -6.2".
func (d RecheckDone) standing() string {
	if !d.Priced {
		return ""
	}
	return " · <b>" + tenths(100*d.Called) + "</b>"
}
