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
// written down with the price the share stood at and the S&P 500 beside it,
// and /scorecard later asks how both have moved since. BUY means "at least
// Clearly better than the index over twelve months, in US dollars", so that
// is what it is measured against.
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

// Record is one verdict as it was given.
type Record struct {
	At         time.Time `json:"at"`
	Name       string    `json:"name"`
	Symbol     string    `json:"symbol"`
	Chart      string    `json:"chart"`
	Verdict    string    `json:"verdict"`
	Confidence string    `json:"confidence,omitempty"`
	Connected  bool      `json:"connected,omitempty"`

	// Price is where the share stood, in the currency it trades in, and
	// Benchmark where the S&P 500 fund stood, both from the chart source so
	// the later reading comes from the same place.
	Price     float64 `json:"price"`
	Currency  string  `json:"currency,omitempty"`
	Benchmark float64 `json:"benchmark"`

	// FX is what one unit of Currency was worth in US dollars when the
	// verdict was given. Zero for a dollar share, and for one given before the
	// rate was kept or when it could not be read: the scoring then looks the
	// rate up from its history, to the day.
	FX float64 `json:"fx,omitempty"`
}

// dollar reports whether the share is priced in US dollars. The first
// records carry no currency, and all of them were.
func (r Record) dollar() bool { return r.Currency == "" || r.Currency == "USD" }

// gain is how far the share has moved since the verdict, in US dollars, given
// its price now in its own currency. The index is a dollar fund, and a Tokyo
// share that rose 10% while the yen fell 10% made a dollar investor nothing.
// False where the exchange rate then or now cannot be read.
func (r Record) gain(now float64, rates Rates) (float64, bool) {
	moved := now / r.Price
	if r.dollar() {
		return moved - 1, true
	}
	then := r.FX
	if then <= 0 {
		then = rates.on(r.Currency, r.At)
	}
	current := rates.Latest(r.Currency)
	if then <= 0 || current <= 0 {
		return 0, false
	}
	return moved*current/then - 1, true
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

// NewRecord writes an idea down, or says it cannot be: without a price to
// start from there is nothing to measure later.
func NewRecord(idea model.Idea, chart string, benchmark float64, at time.Time) (Record, bool) {
	r := Record{
		At:         at,
		Name:       idea.Name,
		Symbol:     idea.Symbol(),
		Chart:      chart,
		Verdict:    idea.Verdict,
		Confidence: idea.Confidence,
		Connected:  idea.Connected,
		Benchmark:  benchmark,
	}
	if idea.Trading != nil && idea.Trading.Last > 0 {
		r.Price, r.Currency = idea.Trading.Last, idea.Trading.Currency
	}
	if chart == "" || r.Price <= 0 || benchmark <= 0 || r.Verdict == "" {
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

// Summary reads the record back for /scorecard. prices are each chart's price
// now, benchmark the index fund's, and rates the exchange rates of the
// currencies Currencies names.
func (s *Scorecard) Summary(now time.Time, prices map[string]float64, benchmark float64, rates Rates, where *time.Location) string {
	var b strings.Builder
	b.WriteString("<b>📊 Scorecard</b>\n\n")

	if len(s.records) == 0 {
		b.WriteString("No verdicts yet. They start with the next brief's \"worth a closer look\".")
		return b.String()
	}

	var judged []scored
	young, unpriced := 0, 0
	for _, r := range s.records {
		if now.Sub(r.At) < MinAge {
			young++
			continue
		}
		p := prices[r.Chart]
		if p <= 0 || benchmark <= 0 || r.Price <= 0 || r.Benchmark <= 0 {
			unpriced++
			continue
		}
		gain, ok := r.gain(p, rates)
		if !ok {
			unpriced++
			continue
		}
		judged = append(judged, scored{Record: r, gain: gain, index: benchmark/r.Benchmark - 1, age: now.Sub(r.At)})
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

	fmt.Fprintf(&b, "\n\n<i>A BUY promises to beat the S&amp;P 500 by %s over twelve months, and a SELL to trail it by as much. Scored early, each is held to that pace: about %.1f points a month. Shares listed abroad are counted in US dollars. Each verdict is a twelve-month call, so a few weeks say little. Read this for a pattern once there are dozens of each, not for any one name.</i>",
		clearly(), Clearly*100/12)
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func signed(f float64) string {
	return fmt.Sprintf("%+.1f%%", 100*f)
}
