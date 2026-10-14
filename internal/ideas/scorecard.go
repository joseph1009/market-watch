package ideas

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// The scorecard is what makes the verdicts more than opinions. Each one is
// written down when it is given, and /scorecard later asks how the share and
// the S&P 500 have moved since. BUY means "at least Clearly better than the
// index over twelve months, in US dollars", so that is what it is measured
// against.
//
// Both are measured from the first price after the verdict: the open of the
// next session, for the share and for the index alike. The closer look is
// written before the US open from the night's news, and a verdict measured
// from the last close would be credited with the move that news makes at the
// open -- a move nobody reading it could have had.
//
// It is kept on the data volume and never trimmed: a year of verdicts is a
// few thousand small records, and the old ones are the ones that say most.

// Benchmark is the fund a verdict is measured against: the S&P 500, as the
// SPDR fund that tracks it, spelled as the chart source spells it.
const Benchmark = "SPY"

// MinAge is how old a verdict must be before it is scored. Younger than a
// week, the result is the day's noise and says nothing about the call.
const MinAge = 7 * 24 * time.Hour

// Clearly is how far a BUY promises to beat the index over twelve months, and
// a SELL to trail it: five percentage points. Without a number, a share a
// tenth of a point ahead counted as a BUY proved right, and "clearly better"
// was a word the model could read as it liked. The verdicts' prompt and the
// note above every closer look say the same; change them together.
const Clearly = 0.05

// year is the stretch a verdict is a call on.
const year = 365 * 24 * time.Hour

// TriggerWindow is how long a BUY BELOW waits for its price. One whose share
// has not closed at or under it in that time has lapsed: it is counted as
// such, and never scored, since nobody following it bought.
const TriggerWindow = 90 * 24 * time.Hour

// Where a verdict came from: the closer look that started from the day's
// news (every verdict before the weekly themes), the weekly themes found from
// the market's numbers, the daily reactions to news, /analyse, and the
// monthly re-check, which changes a live call it no longer agrees with.
const (
	SourceNews     = "news"
	SourceTheme    = "theme"
	SourceReaction = "reaction"
	SourceAnalysis = "analysis"
	SourceRecheck  = "recheck"
)

// Record is one verdict as it was given.
type Record struct {
	At         time.Time `json:"at"`
	Name       string    `json:"name"`
	Symbol     string    `json:"symbol"`
	Chart      string    `json:"chart"`
	Verdict    string    `json:"verdict"`
	Confidence string    `json:"confidence,omitempty"`

	// Connected marks a company the old closer look found connected to the
	// day's news rather than in it. Kept for the records that carry it.
	Connected bool `json:"connected,omitempty"`

	// Source is how the company came to be judged, one of the Source
	// constants; empty is SourceNews, which is what every record before the
	// field was. Theme is the weekly theme it was picked under.
	Source string `json:"source,omitempty"`
	Theme  string `json:"theme,omitempty"`

	// Price is where the share last closed when the verdict was given, in the
	// currency it trades in, and Benchmark where the S&P 500 fund did. Kept
	// for reading; the scoring starts from Entry.
	Price     float64 `json:"price"`
	Currency  string  `json:"currency,omitempty"`
	Benchmark float64 `json:"benchmark,omitempty"`

	// FX is what one unit of Currency was worth in US dollars when the
	// verdict was given. Zero for a dollar share, and where it could not be
	// read.
	FX float64 `json:"fx,omitempty"`

	// Entry is the first price after the verdict -- the open of the share's
	// next session, EntryAt -- and EntryBenchmark the index fund's first open
	// after it. EntryFX is the dollar rate on the day of Entry. All zero until
	// the session has happened and /scorecard has read it; see Settle.
	Entry          float64   `json:"entry,omitempty"`
	EntryAt        time.Time `json:"entry_at,omitempty"`
	EntryBenchmark float64   `json:"entry_benchmark,omitempty"`
	EntryFX        float64   `json:"entry_fx,omitempty"`

	// Trigger is the price a BUY BELOW waits for, in the share's currency.
	// Its entry is the first close at or under it, and the index's close
	// that day, rather than the next open: see settle.
	Trigger float64 `json:"trigger,omitempty"`

	// Case, Risk and Catalyst are the verdict's own words, kept so a call
	// that goes wrong can be checked against what it said. Empty on the
	// records from before 14 October 2026.
	Case     string `json:"case,omitempty"`
	Risk     string `json:"risk,omitempty"`
	Catalyst string `json:"catalyst,omitempty"`

	// Standing is how the call stood when it was last priced, Review what
	// the back-check found where it was going the wrong way, and Rechecked
	// what the monthly re-check last decided for it.
	Standing  *Standing  `json:"standing,omitempty"`
	Review    *Review    `json:"review,omitempty"`
	Rechecked *Rechecked `json:"rechecked,omitempty"`

	// OffSince is when the call last went off course, zero while it is on
	// course. A live call off course for DropAfter straight is dropped.
	OffSince time.Time `json:"off_since,omitempty"`

	// Closed says when and why the call stopped being the live one on its
	// company. Nil while it is live.
	Closed *Closed `json:"closed,omitempty"`
}

// Closed is when a call stopped being the live one on its company, and why:
// one of the Closed constants.
type Closed struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// Why a call stopped being the live one. A company has one live call at a
// time (2026-10-14): two verdicts on one share, a HOLD from /analyse and a
// BUY from the week's picks, only confused the reader. A call is not closed
// for having run twelve months; twelve months is the yardstick it is judged
// by, and a call still on course stays on the list.
const (
	// ClosedReplaced is a call a different verdict on the same company
	// replaced. Its result is frozen on that day, and counts.
	ClosedReplaced = "replaced"
	// ClosedDropped is a call off course for DropAfter straight. Its result
	// is frozen on that day, and counts.
	ClosedDropped = "dropped"
	// ClosedRepeat is the same verdict given again while the first was live.
	// The first carries on from its own day, and the repeat is not counted.
	ClosedRepeat = "repeat"
	// ClosedRechecked is a call the monthly re-check closed, with no view
	// to take its place: its case played out or broke. Its result is frozen
	// on that day, and counts. /scorecard lists it with the replaced calls,
	// as the owner chose (2026-10-15).
	ClosedRechecked = "rechecked"
)

// DropAfter is how long a live call may stay off course before it is
// dropped: eight weeks of checks in a row, so a bad week or two does not end
// a call, and one back on course starts the count again.
const DropAfter = 8 * 7 * 24 * time.Hour

// recentlyClosed is how long a dropped, replaced or closed call is still
// listed under /scorecard's calls, after which only the totals carry it.
const recentlyClosed = 8 * 7 * 24 * time.Hour

// Standing is how a scored call stood on a day: the points it had gone the
// way it called, as a fraction, and whether that was on course. Directed is
// false for a HOLD, which calls no way.
type Standing struct {
	At       time.Time `json:"at"`
	Called   float64   `json:"called"`
	OnCourse bool      `json:"on_course"`
	Directed bool      `json:"directed"`

	// Gain and Index are the share's move and the index's, and Final marks
	// a closed call's standing on the day it closed, which no later price
	// changes.
	Gain  float64 `json:"gain,omitempty"`
	Index float64 `json:"index,omitempty"`
	Final bool    `json:"final,omitempty"`
}

// Review is what the back-check found when a call was going the wrong way:
// what moved the share (Cause, one of the Cause constants), whether that
// was there to be seen when the call was made, why in a few sentences, and
// what to weigh next time, where the call was misjudged. Called is where the
// call stood when it was checked, so a later check can tell it has worsened.
type Review struct {
	At       time.Time `json:"at"`
	Called   float64   `json:"called"`
	Cause    string    `json:"cause"`
	Knowable bool      `json:"knowable"`
	Why      string    `json:"why"`
	Lesson   string    `json:"lesson,omitempty"`
}

// What moved a share against its call, as the back-check names it.
const (
	CauseMarket    = "market"     // the whole market moved
	CauseSector    = "sector"     // its industry moved, and it with it
	CauseNews      = "news after" // news nobody had when the call was made
	CauseMisjudged = "misjudged"  // the case was wrong on what was known
	CauseEarly     = "too early"  // nothing yet says the case is wrong
)

// causeWords say a cause in the words /scorecard shows.
var causeWords = map[string]string{
	CauseMarket:    "market-wide",
	CauseSector:    "its sector",
	CauseNews:      "news after the call",
	CauseMisjudged: "misjudged",
	CauseEarly:     "too early to say",
}

// Key names a record for the back-check: its symbol and the minute it was
// given, since a share can be called more than once.
func (r Record) Key() string {
	return r.Symbol + " " + r.At.UTC().Format("2006-01-02T15:04")
}

// BackcheckAge is how long a call has before a miss is checked: younger, a
// share going the wrong way is mostly the weeks' noise. A call checked once
// is checked again RecheckAfter later, where it has gone Clearly further the
// wrong way since.
const (
	BackcheckAge = 14 * 24 * time.Hour
	RecheckAfter = 28 * 24 * time.Hour
)

// source is Source with the empty value spelled out.
func (r Record) source() string {
	if r.Source == "" {
		return SourceNews
	}
	return r.Source
}

// dollar reports whether the share is priced in US dollars. The first
// records carry no currency, and all of them were.
func (r Record) dollar() bool { return r.Currency == "" || r.Currency == "USD" }

// gain is how far the share has moved since its entry, in US dollars, given
// its price now in its own currency. The index is a dollar fund, and a Tokyo
// share that rose 10% while the yen fell 10% made a dollar investor nothing.
// False where the exchange rate then or now cannot be read.
func (r Record) gain(entry Record, now float64, rates Rates) (float64, bool) {
	moved := now / entry.Entry
	if r.dollar() {
		return moved - 1, true
	}
	then := entry.EntryFX
	if then <= 0 {
		then = rates.on(r.Currency, entry.EntryAt)
	}
	if then <= 0 {
		then = r.FX
	}
	current := rates.Latest(r.Currency)
	if then <= 0 || current <= 0 {
		return 0, false
	}
	return moved*current/then - 1, true
}

// Session is one day's trading as the scorecard reads it: when it opened,
// and at what prices it opened and closed.
type Session struct {
	Opened      time.Time
	Open, Close float64
}

// Path is a share's sessions, oldest first.
type Path []Session

// after is the first session that opened after t: the first price anybody
// reading a verdict given at t could have dealt at.
func (p Path) after(t time.Time) (Session, bool) {
	for _, s := range p {
		if s.Opened.After(t) && (s.Open > 0 || s.Close > 0) {
			if s.Open <= 0 {
				s.Open = s.Close
			}
			return s, true
		}
	}
	return Session{}, false
}

// reached is the first session after t, and within TriggerWindow of it,
// that closed at or under price: the first close a reader waiting for that
// price could have bought at. The chart sources give no day's low, so a
// share that only dipped under it during a session has not reached it.
func (p Path) reached(t time.Time, price float64) (Session, bool) {
	for _, s := range p {
		if !s.Opened.After(t) || s.Close <= 0 {
			continue
		}
		if s.Opened.Sub(t) > TriggerWindow {
			break
		}
		if s.Close <= price {
			return s, true
		}
	}
	return Session{}, false
}

// upTo is the last session with a close that opened by t: the index's close
// on the day a share reached its price, or, for a share whose session opens
// before New York's, the day before.
func (p Path) upTo(t time.Time) (Session, bool) {
	for i := len(p) - 1; i >= 0; i-- {
		if !p[i].Opened.After(t) && p[i].Close > 0 {
			return p[i], true
		}
	}
	return Session{}, false
}

// last is the latest session with a close.
func (p Path) last() (Session, bool) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].Close > 0 {
			return p[i], true
		}
	}
	return Session{}, false
}

// Rates are exchange-rate histories by currency: what one unit was worth in
// US dollars at each day's close, oldest first.
type Rates map[string][]Rate

// Rate is one day's close.
type Rate struct {
	Date time.Time
	USD  float64
}

// Latest is the currency's most recent rate, or zero.
func (r Rates) Latest(currency string) float64 {
	if h := r[currency]; len(h) > 0 {
		return h[len(h)-1].USD
	}
	return 0
}

// On is the currency's rate on the day of t, or the last day before it the
// history has, or zero where the history starts later.
func (r Rates) On(currency string, t time.Time) float64 { return r.on(currency, t) }

func (r Rates) on(currency string, t time.Time) float64 {
	day := t.UTC().Truncate(24 * time.Hour)
	h := r[currency]
	for i := len(h) - 1; i >= 0; i-- {
		if !h[i].Date.After(day) {
			return h[i].USD
		}
	}
	return 0
}

// NewRecord writes an idea down, or says it cannot be: without a chart to
// read its price from later there is nothing to measure.
func NewRecord(idea model.Idea, chart string, at time.Time) (Record, bool) {
	r := Record{
		At:         at,
		Name:       idea.Name,
		Symbol:     idea.Symbol(),
		Chart:      chart,
		Verdict:    idea.Verdict,
		Confidence: idea.Confidence,
		Theme:      idea.Theme,
	}
	switch idea.Kind {
	case model.IdeaTheme:
		r.Source = SourceTheme
	case model.IdeaReaction:
		r.Source = SourceReaction
	}
	if idea.Trading != nil && idea.Trading.Last > 0 {
		r.Price, r.Currency = idea.Trading.Last, idea.Trading.Currency
	}
	if idea.Verdict == model.BuyBelow {
		r.Trigger = idea.Entry
	}
	r.Case, r.Risk, r.Catalyst = idea.Case, idea.Risk, idea.Catalyst
	if chart == "" || r.Verdict == "" {
		return Record{}, false
	}
	return r, true
}

// Scorecard is the record of every verdict given.
//
// Its methods may be called at once: two analyses running side by side can
// each record a verdict as they finish (2026-10-03).
type Scorecard struct {
	Path    string
	mu      sync.Mutex
	records []Record
}

// LoadScorecard reads the record; a missing file is an empty one.
func LoadScorecard(path string) (*Scorecard, error) {
	s := &Scorecard{Path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.records); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// The records from before one live call a company carry its earlier
	// calls side by side; they are put in order once.
	if oneLiveCall(s.records) {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// oneLiveCall closes, in the order they were given, every call a later one
// on the same company replaced or repeated, and reports whether it changed
// any. It is what Add does as each call comes, applied to a record kept
// before that rule.
func oneLiveCall(records []Record) bool {
	order := make([]int, len(records))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return records[order[a]].At.Before(records[order[b]].At) })
	live := map[string]int{}
	changed := false
	for _, i := range order {
		r := &records[i]
		key := strings.ToUpper(r.Symbol)
		if r.Closed != nil {
			continue
		}
		if j, ok := live[key]; ok && records[j].Closed == nil {
			if records[j].Verdict == r.Verdict {
				r.Closed = &Closed{At: r.At, Reason: ClosedRepeat}
				changed = true
				continue
			}
			records[j].Closed = &Closed{At: r.At, Reason: ClosedReplaced}
			changed = true
		}
		live[key] = i
	}
	return changed
}

// place adds r as its company's live call, unless it repeats the live call's
// verdict, and reports whether it was added. A different verdict replaces
// the live call, which closes on r's day.
func (s *Scorecard) place(r Record) bool {
	for i := len(s.records) - 1; i >= 0; i-- {
		old := &s.records[i]
		if old.Closed != nil || !strings.EqualFold(old.Symbol, r.Symbol) {
			continue
		}
		if old.Verdict == r.Verdict {
			return false
		}
		old.Closed = &Closed{At: r.At, Reason: ClosedReplaced}
		break
	}
	s.records = append(s.records, r)
	return true
}

// Add records verdicts, each as its company's live call, and saves. One that
// repeats the live call's verdict is not recorded: the live call carries on
// from its own day.
func (s *Scorecard) Add(records ...Record) error {
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, r := range records {
		changed = s.place(r) || changed
	}
	if !changed {
		return nil
	}
	return s.save()
}

// AddOnce records r as Add does, and reports whether it was recorded. It
// checks and adds in one step, so two analyses of one share finishing
// together are not both counted.
func (s *Scorecard) AddOnce(r Record) (added bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.place(r) {
		return false, nil
	}
	return true, s.save()
}

// All returns every record, oldest first.
func (s *Scorecard) All() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

// Due returns the charts that need a current price, newest verdicts first,
// at most limit of them: those old enough to score, and the newer ones, whose
// move so far the list of calls shows unscored. The benchmark is not
// included; the caller always needs it.
func (s *Scorecard) Due(now time.Time, limit int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for i := len(s.records) - 1; i >= 0; i-- {
		r := s.records[i]
		if seen[r.Chart] || (r.Closed != nil && (r.Closed.Reason == ClosedRepeat || (r.Standing != nil && r.Standing.Final))) {
			continue
		}
		seen[r.Chart] = true
		out = append(out, r.Chart)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// Currencies returns the currencies other than the dollar that the verdicts
// old enough to score are priced in, for their exchange rates to be read.
func (s *Scorecard) Currencies(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, r := range s.records {
		if now.Sub(r.At) < MinAge || r.dollar() || seen[r.Currency] {
			continue
		}
		seen[r.Currency] = true
		out = append(out, r.Currency)
	}
	return out
}

// Settle fills in the entry of every record whose next session has now
// happened, from paths by chart and the index fund's bench, and saves. A
// record already settled keeps its entry: the price history is only ever
// read for two years back, and a verdict older than that must still be
// scored.
func (s *Scorecard) Settle(paths map[string]Path, bench Path, rates Rates) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.records {
		if r, ok := settle(s.records[i], paths[s.records[i].Chart], bench, rates); ok && s.records[i].Entry == 0 {
			s.records[i] = r
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

// settle is a record with its entry, from its own path and the index's, or
// false while the session after it has not happened. A BUY BELOW's entry is
// the first close at or under its price, and false until there is one.
func settle(r Record, path, bench Path, rates Rates) (Record, bool) {
	if r.Entry > 0 && r.EntryBenchmark > 0 {
		return r, true
	}
	if r.Verdict == model.BuyBelow {
		share, ok := path.reached(r.At, r.Trigger)
		if !ok {
			return r, false
		}
		index, ok := bench.upTo(share.Opened)
		if !ok {
			return r, false
		}
		r.Entry, r.EntryAt, r.EntryBenchmark = share.Close, share.Opened, index.Close
		if !r.dollar() {
			r.EntryFX = rates.on(r.Currency, share.Opened)
		}
		return r, true
	}
	share, ok := path.after(r.At)
	if !ok {
		return r, false
	}
	index, ok := bench.after(r.At)
	if !ok {
		return r, false
	}
	r.Entry, r.EntryAt, r.EntryBenchmark = share.Open, share.Opened, index.Open
	if !r.dollar() {
		r.EntryFX = rates.on(r.Currency, share.Opened)
	}
	return r, true
}

func (s *Scorecard) save() error {
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// scored is one verdict set against what happened.
type scored struct {
	Record
	gain, index float64 // fractional moves since, of the share in dollars and of the index
	age         time.Duration
}

func (s scored) ahead() float64 { return s.gain - s.index }

// called is how far the share has gone the way its verdict said, against the
// index: ahead of it for a BUY, behind it for a SELL.
func (s scored) called() float64 {
	if s.Verdict == model.Sell {
		return -s.ahead()
	}
	return s.ahead()
}

// right says whether the call is on course: a BUY leading the index, or a
// SELL trailing it, by at least Clearly a year, pro rata. A verdict is scored
// long before its twelve months are up, and a month in, holding it to the
// full five points would call most good calls wrong. A HOLD has no direction
// to be right about. A BUY BELOW that reached its price is a BUY from there.
func (s scored) right() (bool, bool) {
	need := Clearly * math.Min(float64(s.age)/float64(year), 1)
	switch s.Verdict {
	case model.Buy, model.BuyBelow:
		return s.ahead() >= need, true
	case model.Sell:
		return s.ahead() <= -need, true
	}
	return false, false
}

// onCourse says whether a call is where it should be by now: a BUY, BUY
// BELOW or SELL that is right, and a HOLD within Clearly of the index either
// way. It decides the mark beside each call and whether a live one is
// counting towards being dropped.
func (s scored) onCourse() bool {
	if right, directed := s.right(); directed {
		return right
	}
	return math.Abs(s.ahead()) <= Clearly
}

// Called is how far a verdict has gone the way it said against the index
// since its entry, as a fraction -- ahead of it for a BUY, behind it for a
// SELL, and plain ahead for a HOLD -- and whether it could be priced yet. A
// closed call's is frozen on the day it closed.
func Called(r Record, path, bench Path, rates Rates) (float64, bool) {
	c := assess(r, time.Now(), path, bench, rates)
	if !c.priced {
		return 0, false
	}
	return c.called(), true
}

// Since returns the records from one source given since a time, oldest
// first.
func (s *Scorecard) Since(source string, since time.Time) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range s.records {
		if r.source() == source && !r.At.Before(since) && !r.repeat() {
			out = append(out, r)
		}
	}
	return out
}

// repeat reports whether the record repeats a live call, and so is not
// counted.
func (r Record) repeat() bool { return r.Closed != nil && r.Closed.Reason == ClosedRepeat }

// Summary reads the record back for /scorecard, as the messages to send in
// turn. The first gives the totals: how each kind of verdict has done, and
// the calls split by confidence and by how the company was found. The rest
// list every verdict by name, a line each, grouped by verdict and best first
// within each group, since totals alone do not say which calls are working.
// paths are each chart's sessions, bench the index fund's, and rates the
// exchange rates of the currencies Currencies names.
func (s *Scorecard) Summary(now time.Time, paths map[string]Path, bench Path, rates Rates, where *time.Location) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	b.WriteString("<b>📊 Scorecard</b>\n\n")

	if len(s.records) == 0 {
		b.WriteString("No verdicts yet. They start with the next brief's \"worth a closer look\".")
		return []string{b.String()}
	}

	var judged []scored
	var calls []call
	young, unpriced, waiting, lapsed := 0, 0, 0, 0
	counted, live, dropped, replaced, closed := 0, 0, 0, 0, 0
	for _, r := range s.records {
		c := assess(r, now, paths[r.Chart], bench, rates)
		if c.state == callRepeat {
			continue
		}
		counted++
		switch {
		case r.Closed == nil:
			live++
		case r.Closed.Reason == ClosedDropped:
			dropped++
		case r.Closed.Reason == ClosedRechecked:
			closed++
		default:
			replaced++
		}
		switch c.state {
		case callLapsed:
			lapsed++
		case callWaiting:
			waiting++
		case callYoung:
			young++
		case callUnpriced:
			unpriced++
		case callBrief:
		default:
			judged = append(judged, c.scored)
		}
		calls = append(calls, c)
	}

	fmt.Fprintf(&b, "%d calls since %s: %d live", counted, s.records[0].At.In(where).Format("2 Jan"), live)
	if dropped > 0 {
		fmt.Fprintf(&b, ", %d dropped", dropped)
	}
	if replaced > 0 {
		fmt.Fprintf(&b, ", %d replaced by a newer call", replaced)
	}
	if closed > 0 {
		fmt.Fprintf(&b, ", %d closed on a re-check", closed)
	}
	b.WriteString(". ")
	switch {
	case len(judged) == 0 && young > 0:
		b.WriteString("None is a week old yet, so there is nothing to score: a verdict is judged once it has had a week. How each has moved so far is below.\n")
	default:
		fmt.Fprintf(&b, "%d are a week old or more and scored here", len(judged))
		if young > 0 {
			fmt.Fprintf(&b, "; %d %s newer", young, plural(young, "is", "are"))
		}
		if unpriced > 0 {
			fmt.Fprintf(&b, "; %d could not be priced in dollars today", unpriced)
		}
		b.WriteString(".\n")
	}
	if waiting+lapsed > 0 {
		fmt.Fprintf(&b, "%d %s for %s price", waiting, plural(waiting, "BUY BELOW is waiting", "BUY BELOWs are waiting"), plural(waiting, "its", "their"))
		if lapsed > 0 {
			fmt.Fprintf(&b, ", and %d lapsed after three months without reaching %s", lapsed, plural(lapsed, "it", "theirs"))
		}
		b.WriteString(".\n")
	}

	for _, v := range []string{model.Buy, model.BuyBelow, model.Hold, model.Sell} {
		var group []scored
		for _, j := range judged {
			if j.Verdict == v {
				group = append(group, j)
			}
		}
		if len(group) == 0 {
			continue
		}
		var sum float64
		rights := 0
		for _, g := range group {
			sum += g.ahead()
			if ok, _ := g.right(); ok {
				rights++
			}
		}
		avg := sum / float64(len(group))
		fmt.Fprintf(&b, "\n<b>%s</b>, %d scored: ", v, len(group))
		switch v {
		case model.Buy:
			fmt.Fprintf(&b, "%d on course to beat the S&amp;P 500 by %s (%d%%), ", rights, clearly(), percentOf(rights, len(group)))
		case model.BuyBelow:
			fmt.Fprintf(&b, "%d on course to beat the S&amp;P 500 by %s since reaching %s price (%d%%), ", rights, clearly(), plural(len(group), "its", "their"), percentOf(rights, len(group)))
		case model.Sell:
			fmt.Fprintf(&b, "%d on course to trail it by %s (%d%%), ", rights, clearly(), percentOf(rights, len(group)))
		case model.Hold:
			within := 0
			for _, g := range group {
				if g.onCourse() {
					within++
				}
			}
			fmt.Fprintf(&b, "%d within %s of it (%d%%), ", within, clearly(), percentOf(within, len(group)))
		}
		fmt.Fprintf(&b, "on average %s", points(avg))
	}

	// A BUY and a SELL are both calls with a direction, so they are pooled
	// here and measured in it. Whether "high confidence" is worth more than
	// "low", and whether one way of finding companies beats another, is what
	// these are for.
	b.WriteString(breakdown("By confidence", judged, func(j scored) string { return j.Confidence },
		[]string{"high", "medium", "low"}, nil))
	b.WriteString(breakdown("By how the company was found", judged, func(j scored) string { return j.source() },
		[]string{SourceTheme, SourceReaction, SourceAnalysis, SourceRecheck, SourceNews}, sourceNames))
	// What moved the misses is /backcheck's to say; here, where to find it.
	if line := causeTally(calls); line != "" {
		b.WriteString("\n\n" + line + " Send /backcheck for each one.")
	}

	fmt.Fprintf(&b, "\n\n<i>A BUY promises to beat the S&amp;P 500 by %s over twelve months, and a SELL to trail it by as much. Each is measured from the first price after it was given, the next session's open, for the share and the index alike. A BUY BELOW is a BUY that waits for a lower price: it is measured from the first close at or under that price, against the index's close that day, and lapses if the price does not come within three months. Scored early, each is held to that pace: about %.1f points a month. Shares listed abroad are counted in US dollars. Each verdict is a twelve-month call, so a few weeks say little. Read this for a pattern once there are dozens of each, not for any one name. A company has one live call: a newer verdict replaces the old. A call stays on the list while it keeps on course, and is dropped after eight weeks off course in a row. Once a month each live call is looked at again, and may be changed or closed. All of these still count here with their final results.</i>",
		clearly(), Clearly*100/12)

	return append([]string{b.String()}, callMessages(calls, now, where)...)
}

// assess is one record set against its prices: its entry, its moves since
// and whether, and why not, it is scored. A BUY BELOW is a call from the day
// its price came, and is scored from then, once that is a week ago.
func assess(r Record, now time.Time, path, bench Path, rates Rates) call {
	if r.repeat() {
		return call{scored: scored{Record: r}, state: callRepeat}
	}
	// A closed call is measured to the day it closed, and once its standing
	// on that day is written down, from that alone.
	end := now
	if r.Closed != nil {
		end = r.Closed.At
		if st := r.Standing; st != nil && st.Final {
			since := r.At
			if r.Verdict == model.BuyBelow && !r.EntryAt.IsZero() {
				since = r.EntryAt
			}
			c := call{scored: scored{Record: r, gain: st.Gain, index: st.Index, age: end.Sub(since)}, priced: true, state: callScored}
			if c.age < MinAge {
				c.state = callBrief
			}
			return c
		}
	}
	// A live call is measured to the latest close, a closed one to the last
	// close by the day it closed.
	index, indexOK := bench.last()
	current, have := path.last()
	if r.Closed != nil {
		index, indexOK = bench.upTo(end)
		current, have = path.upTo(end)
	}
	entry, settled := settle(r, path, bench, rates)
	if settled && r.Closed != nil && entry.EntryAt.After(end) {
		settled = false // closed before its first price
	}
	c := call{scored: scored{Record: entry}, last: current.Close}
	if settled && have && indexOK && entry.Entry > 0 && entry.EntryBenchmark > 0 {
		if gain, ok := r.gain(entry, current.Close, rates); ok {
			c.gain, c.index, c.priced = gain, index.Close/entry.EntryBenchmark-1, true
		}
	}
	since := r.At
	if r.Verdict == model.BuyBelow && settled {
		since = entry.EntryAt
	}
	c.age = end.Sub(since)
	if r.Closed == nil && !r.OffSince.IsZero() {
		c.off = now.Sub(r.OffSince)
	}
	switch {
	case r.Closed != nil && (!settled || c.age < MinAge):
		c.state = callBrief
	case r.Verdict == model.BuyBelow && !settled && now.Sub(r.At) > TriggerWindow:
		c.state = callLapsed
	case r.Verdict == model.BuyBelow && !settled:
		c.state = callWaiting
	case c.age < MinAge:
		c.state = callYoung
	case !c.priced:
		c.state = callUnpriced
	default:
		c.state = callScored
	}
	return c
}

// Refresh writes down how each scored call stands, from the same prices
// Summary reads, and saves. The record the verdicts are shown of their own
// calls, and the back-check's choice of misses, read it without pricing every
// share again. A call whose prices were not given keeps what it had.
func (s *Scorecard) Refresh(now time.Time, paths map[string]Path, bench Path, rates Rates) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i, r := range s.records {
		if r.repeat() || (r.Closed != nil && r.Standing != nil && r.Standing.Final) {
			continue
		}
		path, ok := paths[r.Chart]
		if !ok {
			continue
		}
		c := assess(r, now, path, bench, rates)
		if c.state != callScored {
			continue
		}
		_, directed := c.right()
		on := c.onCourse()
		st := &Standing{At: now, Called: c.called(), Gain: c.gain, Index: c.index, OnCourse: on, Directed: directed}
		switch {
		case r.Closed != nil:
			st.At, st.Final = r.Closed.At, true
		case on:
			s.records[i].OffSince = time.Time{}
		case r.OffSince.IsZero():
			s.records[i].OffSince = now
		case now.Sub(r.OffSince) >= DropAfter:
			s.records[i].Closed = &Closed{At: now, Reason: ClosedDropped}
			st.Final = true
		}
		s.records[i].Standing = st
		if r.Verdict == model.BuyBelow && r.Entry == 0 {
			s.records[i].Entry, s.records[i].EntryAt, s.records[i].EntryBenchmark, s.records[i].EntryFX = c.Entry, c.EntryAt, c.EntryBenchmark, c.EntryFX
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

// Misses are the calls going the wrong way that the back-check should look
// at, the worst first, at most max of them: a BUY, a BUY BELOW that reached
// its price, or a SELL, scored, off course as last refreshed, and at least
// BackcheckAge into its call. One already checked comes back RecheckAfter
// later only where it has gone Clearly further the wrong way since.
func (s *Scorecard) Misses(now time.Time, max int) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range s.records {
		st := r.Standing
		if r.Closed != nil || st == nil || !st.Directed || st.OnCourse || now.Sub(st.At) > 3*24*time.Hour {
			continue
		}
		since := r.At
		if r.Verdict == model.BuyBelow {
			since = r.EntryAt
		}
		if now.Sub(since) < BackcheckAge {
			continue
		}
		if rv := r.Review; rv != nil && (now.Sub(rv.At) < RecheckAfter || st.Called > rv.Called-Clearly) {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Standing.Called < out[j].Standing.Called })
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// SetReview writes down what the back-check found on the record with key,
// and saves. It reports whether there was such a record.
func (s *Scorecard) SetReview(key string, rv Review) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.records {
		if s.records[i].Key() == key {
			if st := s.records[i].Standing; st != nil {
				rv.Called = st.Called
			}
			s.records[i].Review = &rv
			return true, s.save()
		}
	}
	return false, nil
}

// lessonsShown is how many of the back-check's lessons the verdicts are
// given: the newest, so the record they read moves on as it is learned.
const lessonsShown = 8

// RecordNote is the scorecard as the verdicts read it: how each kind of
// call has done, grouped by how the company was found and the way it was
// called, how each confidence has done, what the back-check found moved
// the misses, and the lessons it drew where a call was misjudged. Empty
// until a call has been scored.
func (s *Scorecard) RecordNote() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	type tally struct {
		n, right int
		sum      float64
	}
	groups, conf := map[string]*tally{}, map[string]*tally{}
	var order []string
	add := func(m map[string]*tally, k string, st *Standing) bool {
		t, seen := m[k]
		if !seen {
			t = &tally{}
			m[k] = t
		}
		t.n++
		t.sum += st.Called
		if st.OnCourse {
			t.right++
		}
		return !seen
	}
	for _, r := range s.records {
		if r.Standing == nil || !r.Standing.Directed || r.repeat() {
			continue
		}
		k := sourceNames[r.source()] + ", " + r.Verdict
		if add(groups, k, r.Standing) {
			order = append(order, k)
		}
		if r.Confidence != "" {
			add(conf, r.Confidence, r.Standing)
		}
	}
	if len(order) == 0 {
		return ""
	}
	sort.Strings(order)
	var b strings.Builder
	b.WriteString("Your record so far, from the scorecard: calls a week old or more, as last priced. A group of a few calls says little; weigh it by its size.\n")
	for _, k := range order {
		t := groups[k]
		fmt.Fprintf(&b, "- %s: %d of %d on course, on average %s points the way called\n", k, t.right, t.n, tenths(100*t.sum/float64(t.n)))
	}
	for _, c := range []string{"high", "medium", "low"} {
		if t := conf[c]; t != nil {
			fmt.Fprintf(&b, "- %s confidence: %d of %d on course\n", c, t.right, t.n)
		}
	}

	causes := map[string]int{}
	var lessons []string
	for i := len(s.records) - 1; i >= 0; i-- {
		r := s.records[i]
		if r.Review == nil {
			continue
		}
		causes[r.Review.Cause]++
		if r.Review.Cause == CauseMisjudged && r.Review.Lesson != "" && len(lessons) < lessonsShown {
			lessons = append(lessons, fmt.Sprintf("- %s, %s on %s: %s", r.Symbol, r.Verdict, r.At.Format("2 Jan"), r.Review.Lesson))
		}
	}
	if len(causes) > 0 {
		var parts []string
		for _, c := range []string{CauseMarket, CauseSector, CauseNews, CauseMisjudged, CauseEarly} {
			if causes[c] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", causes[c], causeWords[c]))
			}
		}
		fmt.Fprintf(&b, "The calls that went the wrong way were checked afterwards: %s.\n", strings.Join(parts, ", "))
	}
	if len(lessons) > 0 {
		b.WriteString("Lessons from the calls that were misjudged, newest first:\n" + strings.Join(lessons, "\n") + "\n")
	}
	return b.String()
}

// CompanyNote is what the scorecard holds on earlier calls on one share:
// each call, how it stands, and what the back-check found. Empty where there
// are none.
func (s *Scorecard) CompanyNote(symbol string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []string
	for _, r := range s.records {
		if !strings.EqualFold(r.Symbol, symbol) || r.repeat() {
			continue
		}
		line := fmt.Sprintf("- %s on %s (%s", r.Verdict, r.At.Format("2 Jan 2006"), sourceShort[r.source()])
		if r.Confidence != "" {
			line += ", " + r.Confidence + " confidence"
		}
		line += ")"
		if st := r.Standing; st != nil {
			line += fmt.Sprintf(": %s points the way it called as of %s", tenths(100*st.Called), st.At.Format("2 Jan"))
		}
		if rv := r.Review; rv != nil {
			line += fmt.Sprintf(". Checked afterwards: %s. %s", causeWords[rv.Cause], rv.Why)
		}
		if rc := r.Rechecked; rc != nil {
			line += fmt.Sprintf(". Looked at again on %s (%s): %s", rc.At.Format("2 Jan"), rechecks[rc.Action], rc.Why)
		}
		if r.Closed != nil {
			line += fmt.Sprintf(". %s on %s", closedWords[r.Closed.Reason], r.Closed.At.Format("2 Jan"))
		} else {
			line += ". The live call"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Earlier calls on it, from the scorecard:\n" + strings.Join(lines, "\n") + "\n"
}

// causeTally says what the back-check found moved the misses it looked at:
// "Of 9 misses checked: 3 market-wide, 2 its sector, 4 misjudged." Empty
// where none has been checked.
func causeTally(calls []call) string {
	n := map[string]int{}
	total := 0
	for _, c := range calls {
		if c.Review != nil && c.state == callScored {
			if right, directed := c.right(); directed && !right {
				n[c.Review.Cause]++
				total++
			}
		}
	}
	if total == 0 {
		return ""
	}
	var parts []string
	for _, k := range []string{CauseMarket, CauseSector, CauseNews, CauseMisjudged, CauseEarly} {
		if n[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[k], causeWords[k]))
		}
	}
	return fmt.Sprintf("<b>Why the misses missed</b>, of %d checked: %s.", total, strings.Join(parts, ", "))
}

// Backchecks reads the back-check's findings back for /backcheck, as the
// messages to send in turn: what moved each call going the wrong way, why,
// whether it was there to be seen, and the lesson where it was misjudged,
// the worst first. It is its own command, apart from /scorecard, which
// tags each miss with its cause and no more. paths, bench and rates are
// as Summary takes them.
func (s *Scorecard) Backchecks(now time.Time, paths map[string]Path, bench Path, rates Rates, where *time.Location) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var calls []call
	for _, r := range s.records {
		calls = append(calls, assess(r, now, paths[r.Chart], bench, rates))
	}
	return backcheckMessages(calls, where)
}

// backcheckIntro heads /backcheck, and says what it is when there is
// nothing to show yet.
const backcheckIntro = "<b>🔍 Back-checks</b>\n<i>Each Monday, before the week's picks, the calls two weeks or more into going the wrong way are looked at again: whether the whole market, the share's sector, news after the call or a misjudged case moved it, and whether that could have been seen when the call was made.</i>"

// backcheckMessages lists each call off course that the back-check has
// looked at, worst first, under the tally of what moved them, over as many
// messages as it takes, never splitting an entry.
func backcheckMessages(calls []call, where *time.Location) []string {
	var in []call
	for _, c := range calls {
		if c.Review == nil || c.state != callScored {
			continue
		}
		if right, directed := c.right(); directed && !right {
			in = append(in, c)
		}
	}
	if len(in) == 0 {
		return []string{backcheckIntro + "\n\nNone yet: no call going the wrong way has been checked."}
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].called() < in[j].called() })
	lines := []string{backcheckIntro + "\n\n" + causeTally(calls)}
	for _, c := range in {
		rv := c.Review
		line := fmt.Sprintf("\n%s · %s %s · <b>%s</b> · <i>%s</i>", shareLabel(c.Symbol, c.Name),
			c.Verdict, c.At.In(where).Format("2 Jan"), tenths(100*c.called()), causeWords[rv.Cause])
		if rv.Knowable {
			line += ", <i>there to be seen</i>"
		}
		line += "\n" + telegram.Escape(rv.Why)
		if rv.Cause == CauseMisjudged && rv.Lesson != "" {
			line += "\n<i>Lesson:</i> " + telegram.Escape(rv.Lesson)
		}
		lines = append(lines, line)
	}
	return packLines(lines)
}

// packLines joins lines into messages that each fit Telegram's limit,
// starting a new message rather than splitting a line, and dropping the
// blank line that opened a group at the top of one.
func packLines(lines []string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range lines {
		if cur.Len() > 0 && !telegram.Fits(cur.String()+"\n"+line) {
			out = append(out, cur.String())
			cur.Reset()
			line = strings.TrimPrefix(line, "\n")
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// call is one verdict as the list of calls shows it: scored, or why not.
type call struct {
	scored
	state  int
	priced bool
	last   float64       // the share's latest close, in its own currency
	off    time.Duration // how long a live call has been off course
}

// Why a verdict is or is not scored.
const (
	callScored = iota
	callYoung
	callWaiting
	callLapsed
	callUnpriced
	callRepeat // repeats the live call: not counted
	callBrief  // closed within a week of its first price: not scored
)

// closedWords say why a call closed, as /scorecard and the verdicts read it.
var closedWords = map[string]string{
	ClosedReplaced:  "Replaced by a newer call",
	ClosedDropped:   "Dropped after eight weeks off course",
	ClosedRechecked: "Closed on the monthly re-check",
}

// rechecks say what the monthly re-check did with a call, as the verdicts
// read it.
var rechecks = map[string]string{
	RecheckKeep:   "kept",
	RecheckChange: "changed",
	RecheckClose:  "closed",
}

// callMessages lists each company's live call on a line of its own: those
// scored by verdict, best first, then those too new to score, the BUY BELOWs
// waiting for their price or lapsed, and those that could not be priced;
// then the calls dropped or replaced in the last weeks, with their final
// results. A line is never split across two messages.
func callMessages(calls []call, now time.Time, where *time.Location) []string {
	lines := []string{"<b>📋 Each call</b>\n<i>The live call on each company: its share's move since the call, against the S&amp;P 500's, then the points it has gone the way it called. ✅ on course to the 5-point mark, or for a HOLD within 5 points of the index; ❌ not. A call off course eight weeks in a row is dropped.</i>"}
	group := func(title string, keep func(call) bool, order func(a, b call) bool, line func(call) string) {
		var in []call
		for _, c := range calls {
			if keep(c) {
				in = append(in, c)
			}
		}
		if len(in) == 0 {
			return
		}
		if order != nil {
			sort.SliceStable(in, func(i, j int) bool { return order(in[i], in[j]) })
		}
		lines = append(lines, "\n"+title)
		for _, c := range in {
			lines = append(lines, line(c))
		}
	}
	best := func(a, b call) bool { return a.called() > b.called() }
	newest := func(a, b call) bool { return a.At.After(b.At) }
	scoredAs := func(v string) func(call) bool {
		return func(c call) bool { return c.Closed == nil && c.state == callScored && c.Verdict == v }
	}
	liveIn := func(state int) func(call) bool {
		return func(c call) bool { return c.Closed == nil && c.state == state }
	}
	closedAs := func(reason string) func(call) bool {
		return func(c call) bool {
			return c.Closed != nil && c.Closed.Reason == reason && now.Sub(c.Closed.At) <= recentlyClosed
		}
	}
	lastClosed := func(a, b call) bool { return a.Closed.At.After(b.Closed.At) }

	group("<b>BUY</b>", scoredAs(model.Buy), best, func(c call) string { return c.scoredLine(where) })
	group("<b>BUY BELOW</b>, from the day it reached its price", scoredAs(model.BuyBelow), best, func(c call) string { return c.scoredLine(where) })
	group("<b>SELL</b>", scoredAs(model.Sell), best, func(c call) string { return c.scoredLine(where) })
	group("<b>HOLD</b>, points against the index", scoredAs(model.Hold), best, func(c call) string { return c.scoredLine(where) })
	group("<b>Under a week old</b>, not scored yet", liveIn(callYoung), newest, func(c call) string {
		line := "🆕 " + c.who(where) + " · " + c.Verdict
		if c.priced {
			line += " · " + c.moves()
		}
		return line
	})
	group("<b>BUY BELOW, waiting for its price</b>", liveIn(callWaiting), newest, func(c call) string {
		line := "🟡 " + c.who(where) + " · at " + c.money(c.Trigger)
		if c.last > 0 {
			line += fmt.Sprintf(", last %s", c.money(c.last))
		}
		return line
	})
	group("<b>BUY BELOW, lapsed</b>: not reached in three months", liveIn(callLapsed), newest, func(c call) string {
		line := "⚫ " + c.who(where) + " · at " + c.money(c.Trigger)
		if c.last > 0 {
			line += fmt.Sprintf(", last %s", c.money(c.last))
		}
		return line
	})
	group("<b>Not priced today</b>", liveIn(callUnpriced), newest, func(c call) string {
		return "❔ " + c.who(where) + " · " + c.Verdict
	})
	group("<b>Dropped</b>, after eight weeks off course: the final result", closedAs(ClosedDropped), lastClosed, func(c call) string {
		return c.closedLine("⛔", where)
	})
	replacedOrClosed := func(c call) bool { return closedAs(ClosedReplaced)(c) || closedAs(ClosedRechecked)(c) }
	group("<b>Replaced</b> by a newer call, or ✋ closed on the monthly re-check: the result until then", replacedOrClosed, lastClosed, func(c call) string {
		if c.Closed.Reason == ClosedRechecked {
			return c.closedLine("✋", where)
		}
		return c.closedLine("↪️", where)
	})
	return packLines(lines)
}

// closedLine is a dropped, replaced or closed call: who and when, its
// verdict, the day it closed, and its result to that day, where it lasted
// long enough to have one.
func (c call) closedLine(mark string, where *time.Location) string {
	line := fmt.Sprintf("%s %s · %s · until %s", mark, c.who(where), c.Verdict, c.Closed.At.In(where).Format("2 Jan"))
	if c.state == callBrief || !c.priced {
		return line + " · too soon to score"
	}
	return line + " · " + c.moves() + " · <b>" + tenths(100*c.called()) + "</b>"
}

// scoredLine is a scored verdict: its mark, who and when, its moves, and the
// points it has gone the way it called, or for a HOLD, against the index.
func (c call) scoredLine(where *time.Location) string {
	mark := "✅"
	if !c.onCourse() {
		mark = "❌"
	}
	line := mark + " " + c.who(where)
	if c.Verdict == model.BuyBelow {
		line += fmt.Sprintf(" · from %s on %s", c.money(c.Entry), c.EntryAt.In(where).Format("2 Jan"))
	}
	line += " · " + c.moves() + " · <b>" + tenths(100*c.called()) + "</b>"
	if c.Review != nil && mark == "❌" {
		line += " · <i>" + causeWords[c.Review.Cause] + "</i>"
	}
	// Four weeks or more off course, the reader sees the drop coming.
	if weeks := int(c.off / (7 * 24 * time.Hour)); mark == "❌" && weeks >= 4 {
		line += fmt.Sprintf(" · <i>off course %d weeks</i>", weeks)
	}
	return line
}

// tenths writes points to one decimal with their sign, and a figure that
// rounds to nothing as "0.0" rather than "-0.0".
func tenths(p float64) string {
	if math.Round(p*10) == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%+.1f", p)
}

// who is the share and when it was called, and how it was found: "<b>TXG</b>
// 10x Genomics · 5 Oct, weekly".
func (c call) who(where *time.Location) string {
	return fmt.Sprintf("%s · %s, %s", shareLabel(c.Symbol, c.Name), c.At.In(where).Format("2 Jan"), sourceShort[c.source()])
}

// moves is the share's move and the index's: "-21.8% vs +0.8%", the share's
// in US dollars where it is priced in another currency.
func (c call) moves() string {
	inDollars := ""
	if !c.dollar() {
		inDollars = " in US$"
	}
	return fmt.Sprintf("%s%s vs %s", signed(c.gain), inDollars, signed(c.index))
}

// money is a price in the share's own currency: "$46.50", "1.23 SGD".
func (c call) money(v float64) string {
	if c.dollar() {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("%.2f %s", v, c.Currency)
}

// sourceShort says in a word how a verdict's company was found.
var sourceShort = map[string]string{
	SourceTheme:    "weekly",
	SourceReaction: "reaction",
	SourceAnalysis: "/analyse",
	SourceNews:     "old look",
	SourceRecheck:  "re-check",
}

// clipName cuts a company's name to n runes, where it is longer.
func clipName(name string, n int) string {
	rs := []rune(strings.TrimSpace(name))
	if len(rs) <= n {
		return string(rs)
	}
	return strings.TrimSpace(string(rs[:n-1])) + "…"
}

// shareLabel is a share as a line of the scorecard names it: its symbol in
// bold, then its name as a reader would say it, or the symbol alone where
// the name is only the symbol again ("AAON AAON").
func shareLabel(symbol, name string) string {
	name = clipName(plainName(name), 24)
	if name == "" || strings.EqualFold(name, symbol) {
		return "<b>" + telegram.Escape(symbol) + "</b>"
	}
	return "<b>" + telegram.Escape(symbol) + "</b> " + telegram.Escape(name)
}

// legalEndings are the parts of a registered name a reader leaves off.
var legalEndings = []string{", Inc.", ", Inc", " Inc.", " Inc", " Corporation", " Corp.", " Corp", " Holdings Co",
	" Co.", " plc.", " plc", " Ltd.", " Ltd", " N.V.", " Common Stock", " Ordinary Shares"}

// plainName is a company's name as a reader would say it: "Micron
// Technology" for the registered "MICRON TECHNOLOGY INC". A name in capitals
// throughout is put in title case; one with its own capitals keeps them.
func plainName(name string) string {
	name = strings.TrimSpace(name)
	for changed := true; changed; {
		changed = false
		for _, end := range legalEndings {
			if len(name) > len(end) && strings.EqualFold(name[len(name)-len(end):], end) {
				name, changed = strings.TrimSpace(strings.TrimRight(name[:len(name)-len(end)], ",")), true
			}
		}
	}
	// A short word in capitals is the company's own spelling: AXT, GRAIL.
	if name != strings.ToUpper(name) || (!strings.Contains(name, " ") && len([]rune(name)) <= 5) {
		return name
	}
	words := strings.Fields(strings.ToLower(name))
	for i, w := range words {
		rs := []rune(w)
		rs[0] = unicode.ToUpper(rs[0])
		words[i] = string(rs)
	}
	return strings.Join(words, " ")
}

// sourceNames say how each source found its companies, for the breakdown.
var sourceNames = map[string]string{
	SourceTheme:    "weekly themes, from the numbers",
	SourceReaction: "reactions to the news",
	SourceAnalysis: "/analyse",
	SourceNews:     "the old closer look, from the news",
	SourceRecheck:  "changed on the monthly re-check",
}

// breakdown pools the BUYs and SELLs by one attribute, in the order given,
// and says how each group has done in the direction it called. It is left
// out where every call shares one value, since a split into one group says
// nothing the lines above did not.
func breakdown(title string, judged []scored, key func(scored) string, order []string, names map[string]string) string {
	groups := map[string][]scored{}
	for _, j := range judged {
		if _, directed := j.right(); directed {
			groups[key(j)] = append(groups[key(j)], j)
		}
	}
	if len(groups) < 2 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n<b>%s</b>, BUYs and SELLs together", title)
	for _, k := range order {
		group := groups[k]
		if len(group) == 0 {
			continue
		}
		var sum float64
		rights := 0
		for _, g := range group {
			sum += g.called()
			if ok, _ := g.right(); ok {
				rights++
			}
		}
		name := names[k]
		if name == "" {
			name = k
		}
		fmt.Fprintf(&b, "\n• %s: %d of %d on course (%d%%), on average %s the way called",
			telegram.Escape(name), rights, len(group), percentOf(rights, len(group)), pointsSigned(sum/float64(len(group))))
	}
	return b.String()
}

// clearly is Clearly, said as a margin: "5 points".
func clearly() string { return fmt.Sprintf("%g points", Clearly*100) }

func percentOf(n, of int) int {
	if of == 0 {
		return 0
	}
	return int(math.Round(100 * float64(n) / float64(of)))
}

// points is a difference of two percentages, said as one.
func points(f float64) string {
	p := 100 * f
	switch {
	case p >= 0.05:
		return fmt.Sprintf("%.1f points ahead of it", p)
	case p <= -0.05:
		return fmt.Sprintf("%.1f points behind it", -p)
	}
	return "level with it"
}

// pointsSigned is a difference in points with its sign: "+2.1 points".
func pointsSigned(f float64) string {
	return tenths(100*f) + " points"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func signed(f float64) string {
	return fmt.Sprintf("%+.1f%%", 100*f)
}
