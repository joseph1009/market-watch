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
// the market's numbers, the daily reactions to news, and /analyse.
const (
	SourceNews     = "news"
	SourceTheme    = "theme"
	SourceReaction = "reaction"
	SourceAnalysis = "analysis"
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
}

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
	return s, nil
}

// Add records verdicts and saves.
func (s *Scorecard) Add(records ...Record) error {
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
	return s.save()
}

// AddOnce records r and saves, unless the same verdict on the same share,
// from the same source, was recorded within window before it: the same call
// made twice, which should count once. It checks and adds in one step, so
// two analyses of one share finishing together are not both counted.
func (s *Scorecard) AddOnce(r Record, window time.Duration) (added bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repeats(r, window) {
		return false, nil
	}
	s.records = append(s.records, r)
	return true, s.save()
}

// repeats reports whether r was recorded within window before it.
func (s *Scorecard) repeats(r Record, window time.Duration) bool {
	for i := len(s.records) - 1; i >= 0; i-- {
		old := s.records[i]
		if r.At.Sub(old.At) > window {
			break
		}
		if old.Symbol == r.Symbol && old.Verdict == r.Verdict && old.source() == r.source() {
			return true
		}
	}
	return false
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
		if seen[r.Chart] {
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

// Called is how far a verdict has gone the way it said against the index
// since its entry, as a fraction -- ahead of it for a BUY, behind it for a
// SELL, and plain ahead for a HOLD -- and whether it could be priced yet.
func Called(r Record, path, bench Path, rates Rates) (float64, bool) {
	entry, ok := settle(r, path, bench, rates)
	current, have := path.last()
	index, indexOK := bench.last()
	if !ok || !have || !indexOK || entry.Entry <= 0 || entry.EntryBenchmark <= 0 {
		return 0, false
	}
	gain, ok := r.gain(entry, current.Close, rates)
	if !ok {
		return 0, false
	}
	return scored{Record: entry, gain: gain, index: index.Close/entry.EntryBenchmark - 1}.called(), true
}

// Since returns the records from one source given since a time, oldest
// first.
func (s *Scorecard) Since(source string, since time.Time) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range s.records {
		if r.source() == source && !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out
}

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

	index, indexOK := bench.last()
	var judged []scored
	var calls []call
	young, unpriced, waiting, lapsed := 0, 0, 0, 0
	for _, r := range s.records {
		entry, settled := settle(r, paths[r.Chart], bench, rates)
		current, have := paths[r.Chart].last()
		c := call{scored: scored{Record: entry}, last: current.Close}
		if settled && have && indexOK && entry.Entry > 0 && entry.EntryBenchmark > 0 {
			if gain, ok := r.gain(entry, current.Close, rates); ok {
				c.gain, c.index, c.priced = gain, index.Close/entry.EntryBenchmark-1, true
			}
		}
		// A BUY BELOW is a call from the day its price came, and is scored
		// from then, once that is a week ago.
		since := r.At
		if r.Verdict == model.BuyBelow && settled {
			since = entry.EntryAt
		}
		c.age = now.Sub(since)
		switch {
		case r.Verdict == model.BuyBelow && !settled && now.Sub(r.At) > TriggerWindow:
			c.state = callLapsed
			lapsed++
		case r.Verdict == model.BuyBelow && !settled:
			c.state = callWaiting
			waiting++
		case c.age < MinAge:
			c.state = callYoung
			young++
		case !c.priced:
			c.state = callUnpriced
			unpriced++
		default:
			c.state = callScored
			judged = append(judged, c.scored)
		}
		calls = append(calls, c)
	}

	fmt.Fprintf(&b, "%d verdicts since %s. ", len(s.records), s.records[0].At.In(where).Format("2 Jan"))
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
		[]string{SourceTheme, SourceReaction, SourceAnalysis, SourceNews}, sourceNames))

	fmt.Fprintf(&b, "\n\n<i>A BUY promises to beat the S&amp;P 500 by %s over twelve months, and a SELL to trail it by as much. Each is measured from the first price after it was given, the next session's open, for the share and the index alike. A BUY BELOW is a BUY that waits for a lower price: it is measured from the first close at or under that price, against the index's close that day, and lapses if the price does not come within three months. Scored early, each is held to that pace: about %.1f points a month. Shares listed abroad are counted in US dollars. Each verdict is a twelve-month call, so a few weeks say little. Read this for a pattern once there are dozens of each, not for any one name.</i>",
		clearly(), Clearly*100/12)

	return append([]string{b.String()}, callMessages(calls, where)...)
}

// call is one verdict as the list of calls shows it: scored, or why not.
type call struct {
	scored
	state  int
	priced bool
	last   float64 // the share's latest close, in its own currency
}

// Why a verdict is or is not scored.
const (
	callScored = iota
	callYoung
	callWaiting
	callLapsed
	callUnpriced
)

// callMessages lists every verdict on a line of its own: those scored by
// verdict, best first, then those too new to score, the BUY BELOWs waiting
// for their price or lapsed, and those that could not be priced. A line is
// never split across two messages.
func callMessages(calls []call, where *time.Location) []string {
	lines := []string{"<b>📋 Each call</b>\n<i>Its share's move since the call, against the S&amp;P 500's, then the points it has gone the way it called: ✅ on course to the 5-point mark, ❌ not.</i>"}
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
		return func(c call) bool { return c.state == callScored && c.Verdict == v }
	}

	group("<b>BUY</b>", scoredAs(model.Buy), best, func(c call) string { return c.scoredLine(where) })
	group("<b>BUY BELOW</b>, from the day it reached its price", scoredAs(model.BuyBelow), best, func(c call) string { return c.scoredLine(where) })
	group("<b>SELL</b>", scoredAs(model.Sell), best, func(c call) string { return c.scoredLine(where) })
	group("<b>HOLD</b>, points against the index", scoredAs(model.Hold), best, func(c call) string { return c.scoredLine(where) })
	group("<b>Under a week old</b>, not scored yet", func(c call) bool { return c.state == callYoung }, newest, func(c call) string {
		line := "🆕 " + c.who(where) + " · " + c.Verdict
		if c.priced {
			line += " · " + c.moves()
		}
		return line
	})
	group("<b>BUY BELOW, waiting for its price</b>", func(c call) bool { return c.state == callWaiting }, newest, func(c call) string {
		line := "🟡 " + c.who(where) + " · at " + c.money(c.Trigger)
		if c.last > 0 {
			line += fmt.Sprintf(", last %s", c.money(c.last))
		}
		return line
	})
	group("<b>BUY BELOW, lapsed</b>: not reached in three months", func(c call) bool { return c.state == callLapsed }, newest, func(c call) string {
		line := "⚫ " + c.who(where) + " · at " + c.money(c.Trigger)
		if c.last > 0 {
			line += fmt.Sprintf(", last %s", c.money(c.last))
		}
		return line
	})
	group("<b>Not priced today</b>", func(c call) bool { return c.state == callUnpriced }, newest, func(c call) string {
		return "❔ " + c.who(where) + " · " + c.Verdict
	})

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

// scoredLine is a scored verdict: its mark, who and when, its moves, and the
// points it has gone the way it called, or for a HOLD, against the index.
func (c call) scoredLine(where *time.Location) string {
	mark := "⚪"
	if right, directed := c.right(); directed {
		mark = "❌"
		if right {
			mark = "✅"
		}
	}
	line := mark + " " + c.who(where)
	if c.Verdict == model.BuyBelow {
		line += fmt.Sprintf(" · from %s on %s", c.money(c.Entry), c.EntryAt.In(where).Format("2 Jan"))
	}
	return line + " · " + c.moves() + " · <b>" + tenths(100*c.called()) + "</b>"
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
	return fmt.Sprintf("<b>%s</b> %s · %s, %s", telegram.Escape(c.Symbol), telegram.Escape(clipName(c.Name, 24)),
		c.At.In(where).Format("2 Jan"), sourceShort[c.source()])
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
}

// clipName cuts a company's name to n runes, where it is longer.
func clipName(name string, n int) string {
	rs := []rune(strings.TrimSpace(name))
	if len(rs) <= n {
		return string(rs)
	}
	return strings.TrimSpace(string(rs[:n-1])) + "…"
}

// sourceNames say how each source found its companies, for the breakdown.
var sourceNames = map[string]string{
	SourceTheme:    "weekly themes, from the numbers",
	SourceReaction: "reactions to the news",
	SourceAnalysis: "/analyse",
	SourceNews:     "the old closer look, from the news",
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
	return fmt.Sprintf("%+.1f points", 100*f)
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
