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
	Connected  bool      `json:"connected,omitempty"`

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

// on is the currency's rate on the day of t, or the last day before it the
// history has, or zero where the history starts later.
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
		Connected:  idea.Connected,
	}
	if idea.Trading != nil && idea.Trading.Last > 0 {
		r.Price, r.Currency = idea.Trading.Last, idea.Trading.Currency
	}
	if chart == "" || r.Verdict == "" {
		return Record{}, false
	}
	return r, true
}

// Scorecard is the record of every verdict given.
type Scorecard struct {
	Path    string
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
	s.records = append(s.records, records...)
	return s.save()
}

// All returns every record, oldest first.
func (s *Scorecard) All() []Record { return s.records }

// Due returns the charts that need a current price to score what is old
// enough, newest verdicts first, at most limit of them. The benchmark is not
// included; the caller always needs it.
func (s *Scorecard) Due(now time.Time, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for i := len(s.records) - 1; i >= 0; i-- {
		r := s.records[i]
		if now.Sub(r.At) < MinAge || seen[r.Chart] {
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
// false while the session after it has not happened.
func settle(r Record, path, bench Path, rates Rates) (Record, bool) {
	if r.Entry > 0 && r.EntryBenchmark > 0 {
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
// to be right about.
func (s scored) right() (bool, bool) {
	need := Clearly * math.Min(float64(s.age)/float64(year), 1)
	switch s.Verdict {
	case model.Buy:
		return s.ahead() >= need, true
	case model.Sell:
		return s.ahead() <= -need, true
	}
	return false, false
}

// Summary reads the record back for /scorecard. paths are each chart's
// sessions, bench the index fund's, and rates the exchange rates of the
// currencies Currencies names.
func (s *Scorecard) Summary(now time.Time, paths map[string]Path, bench Path, rates Rates, where *time.Location) string {
	var b strings.Builder
	b.WriteString("<b>📊 Scorecard</b>\n\n")

	if len(s.records) == 0 {
		b.WriteString("No verdicts yet. They start with the next brief's \"worth a closer look\".")
		return b.String()
	}

	index, indexOK := bench.last()
	var judged []scored
	young, unpriced := 0, 0
	for _, r := range s.records {
		if now.Sub(r.At) < MinAge {
			young++
			continue
		}
		entry, ok := settle(r, paths[r.Chart], bench, rates)
		current, have := paths[r.Chart].last()
		if !ok || !have || !indexOK || entry.Entry <= 0 || entry.EntryBenchmark <= 0 {
			unpriced++
			continue
		}
		gain, ok := r.gain(entry, current.Close, rates)
		if !ok {
			unpriced++
			continue
		}
		judged = append(judged, scored{Record: entry, gain: gain, index: index.Close/entry.EntryBenchmark - 1, age: now.Sub(r.At)})
	}

	fmt.Fprintf(&b, "%d verdicts since %s. ", len(s.records), s.records[0].At.In(where).Format("2 Jan"))
	switch {
	case len(judged) == 0 && young > 0:
		fmt.Fprintf(&b, "None is a week old yet, so there is nothing to score: a verdict is judged once it has had a week.")
		return b.String()
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

	for _, v := range []string{model.Buy, model.Hold, model.Sell} {
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

	if len(judged) > 0 {
		sort.Slice(judged, func(i, j int) bool { return judged[i].ahead() > judged[j].ahead() })
		b.WriteString("\n\n<b>Best and worst so far</b>")
		shown := map[int]bool{}
		for _, i := range []int{0, len(judged) - 1} {
			if shown[i] {
				continue
			}
			shown[i] = true
			j := judged[i]
			inDollars := ""
			if !j.dollar() {
				inDollars = " in US dollars"
			}
			fmt.Fprintf(&b, "\n• %s <code>%s</code>, %s on %s: %s%s, against %s for the index",
				telegram.Escape(j.Name), telegram.Escape(j.Symbol), j.Verdict,
				j.At.In(where).Format("2 Jan"), signed(j.gain), inDollars, signed(j.index))
		}
	}

	fmt.Fprintf(&b, "\n\n<i>A BUY promises to beat the S&amp;P 500 by %s over twelve months, and a SELL to trail it by as much. Each is measured from the first price after it was given, the next session's open, for the share and the index alike. Scored early, each is held to that pace: about %.1f points a month. Shares listed abroad are counted in US dollars. Each verdict is a twelve-month call, so a few weeks say little. Read this for a pattern once there are dozens of each, not for any one name.</i>",
		clearly(), Clearly*100/12)
	return b.String()
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
