package fundamentals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// The company beside its industry. Until 2026-10-03 the analysis and the
// verdicts were told not to say whether a margin or a growth rate was high for
// the industry: they had one company's figures and nothing to set them
// against. The SEC's frames API gives one figure for every filer for one
// period in a single request, such as every company's revenue for calendar
// 2025. Set against the companies in the same industry group of Nasdaq's
// list, that says where the company stands.
//
// Each company is on its latest twelve months filed, as the page's own
// figures are. Until 2026-10-04 the group was on calendar 2025, and Micron's
// market value over its 2025 profit read 145.1 times on the page while the box
// above it gave a P/E of 14.5 on its latest twelve months: the owner asked for
// one set of months. The frames hold years and quarters but rarely a fourth
// quarter on its own, so twelve months are built as a company's last full
// year, plus the quarters filed since, less the same quarters a year before.
// Cash flows are filed year to date, so their quarters are not in the frames
// and the free cash flow margin stays on the year.
//
// Only US-GAAP figures in dollars are compared. A foreign filer reporting
// under IFRS, or in its own currency, is left out of the group rather than
// converted.

const (
	frameURL = "https://data.sec.gov/api/xbrl/frames/us-gaap/%s/USD/%s.json"

	// frameFresh is how long a frame kept on disk is used before it is read
	// again. A period's frame fills slowly as filers report, and a day's
	// change to it moves no median. One for a period that ended over a year
	// ago barely changes, and is kept for frameSettled.
	frameFresh   = 7 * 24 * time.Hour
	frameSettled = 90 * 24 * time.Hour

	// MinPeers is the fewest companies a comparison is made against. Below it
	// a median is one or two companies' figures.
	MinPeers = 5

	// PeerMarketCap is the smallest company counted in a group: below it, a
	// shell or a company just listed pulls the middle about.
	PeerMarketCap = 500e6

	// largestNamed is how many of the group's largest companies are named, so
	// the reader can judge whether the group is a fair one.
	largestNamed = 6

	// recentQuarters is how far back the quarters reach, in months, for the
	// latest twelve months and the same quarters a year before. Revenue
	// reaches a year further, for its growth on the twelve months before.
	recentQuarters  = 27
	revenueQuarters = 39
)

// Peer is a company in the same industry group.
type Peer struct {
	Symbol    string
	Name      string
	CIK       int
	MarketCap float64 // in dollars, as the market's list gives it today
}

// PeerGroup is where a company stands in its industry group, each company on
// its latest twelve months filed.
type PeerGroup struct {
	Industry string
	Year     int    // the calendar year the free cash flow margin is on
	Own      string // the months the company's own figures are on, where given
	Size     int    // the companies in the group other than this one
	Largest  []string
	Lines    []PeerLine
}

// PeerLine is one measure: the company's figure, the group's median
// and its 25th to 75th percentile, and how many of the group it beats.
type PeerLine struct {
	Label    string
	Times    bool // a multiple rather than a percentage
	Company  float64
	Reported bool // whether the company's own figure is known
	Median   float64
	Low      float64 // the group's lower quartile
	High     float64 // and its upper quartile
	Count    int     // the companies in the group with this figure
	Below    int     // how many of them are lower than the company
}

// The keys of the measures, for OwnFigures.
const (
	PeerGrowth          = "growth"
	PeerGrossMargin     = "grossMargin"
	PeerOperatingMargin = "operatingMargin"
	PeerNetMargin       = "netMargin"
	PeerRD              = "rd"
	PeerPS              = "ps"
	PeerPE              = "pe"
)

// OwnFigures are the company's own figures for the comparison, worked out as
// the page works them out, so the table beside the industry agrees with the
// box above it. Figures is by the keys above; Period names their months.
type OwnFigures struct {
	Period  string
	Figures map[string]float64
}

// PeerFigures are the snapshot's own figures for the comparison: its latest
// twelve months, and the P/E and price to sales the box shows.
func (s Snapshot) PeerFigures() OwnFigures {
	own := OwnFigures{Figures: map[string]float64{}}
	if s.TTM == nil {
		return own
	}
	own.Period = s.TTM.Label
	f := func(key string) (float64, bool) {
		v := s.TTM.Figures[key]
		return v.Amount, v.Known
	}
	rev, ok := f("revenue")
	if !ok || rev <= 0 {
		return own
	}
	if ago := s.TTM.YearAgoRevenue; ago.Known && ago.Amount > 0 {
		own.Figures[PeerGrowth] = rev/ago.Amount - 1
	}
	for key, measure := range map[string]string{"grossProfit": PeerGrossMargin, "operatingIncome": PeerOperatingMargin,
		"netIncome": PeerNetMargin, "researchDevelopment": PeerRD} {
		if v, ok := f(key); ok {
			own.Figures[measure] = v / rev
		}
	}
	g := s.Glance()
	if g.PE.Known && g.On == s.TTM.Label {
		own.Figures[PeerPE] = g.PE.Amount
	}
	if g.PS.Known && g.On == s.TTM.Label {
		own.Figures[PeerPS] = g.PS.Amount
	}
	return own
}

// fact is one filer's figure in a frame, with the dates it covers.
type fact struct {
	Val        float64
	Start, End time.Time
	frame      string // the frame it was read from: "CY2025"
}

// series is one concept's facts for one filer: its full years and its
// quarters.
type series struct {
	years, quarters []fact
}

// frames are the figures one comparison reads: each concept's tags, in the
// order the snapshot prefers them.
var peerConcepts = []string{"revenue", "grossProfit", "operatingIncome", "netIncome",
	"operatingCashFlow", "capitalExpenditure", "researchDevelopment"}

// Peers sets the company against the others in its industry group, each on
// its latest twelve months filed. Industry names the group, as the market's
// list does; own is the company's figures as the page shows them, used in
// place of its frames' where given. The figures are read from the SEC's
// frames, and kept in dir where dir is set.
func (c *Client) Peers(ctx context.Context, cik int, industry string, peers []Peer, own OwnFigures, dir string, now time.Time) (*PeerGroup, error) {
	year := now.Year() - 1
	if now.Month() < time.April {
		year-- // last year's annual reports are not all filed until March
	}

	wanted := map[int]bool{cik: true}
	for _, p := range peers {
		wanted[p.CIK] = true
	}
	data := map[string]map[int]*series{} // concept → filer → its facts
	for _, key := range peerConcepts {
		con, ok := conceptFor(key)
		if !ok {
			continue
		}
		var periods []string
		switch key {
		case "operatingCashFlow", "capitalExpenditure":
			periods = []string{yearPeriod(year)}
		case "revenue":
			periods = append([]string{yearPeriod(year + 1), yearPeriod(year), yearPeriod(year - 1)}, quarterPeriods(now, revenueQuarters)...)
		default:
			periods = append([]string{yearPeriod(year + 1), yearPeriod(year)}, quarterPeriods(now, recentQuarters)...)
		}
		byFiler := map[int]*series{}
		for _, p := range periods {
			got, err := c.frameFor(ctx, con, p, dir, now)
			if err != nil {
				return nil, err
			}
			for filer, f := range got {
				if !wanted[filer] {
					continue
				}
				f.frame = p
				s := byFiler[filer]
				if s == nil {
					s = &series{}
					byFiler[filer] = s
				}
				if strings.Contains(p, "Q") {
					s.quarters = append(s.quarters, f)
				} else {
					s.years = append(s.years, f)
				}
			}
		}
		data[key] = byFiler
	}

	// Each filer's twelve months end where its revenue's latest do, and its
	// other figures are read for the same months.
	ends := map[int]time.Time{}
	for filer, s := range data["revenue"] {
		if end, ok := latestTwelve(s, now); ok {
			ends[filer] = end
		}
	}
	twelve := func(key string, filer int) (float64, bool) {
		end, ok := ends[filer]
		s := data[key][filer]
		if !ok || s == nil {
			return 0, false
		}
		return twelveMonths(s, end)
	}
	share := func(key string) func(Peer) (float64, bool) {
		return func(p Peer) (float64, bool) {
			v, ok := twelve(key, p.CIK)
			r, okr := twelve("revenue", p.CIK)
			if !ok || !okr || r <= 0 {
				return 0, false
			}
			return v / r, true
		}
	}
	inYear := func(key string, filer int) (float64, bool) {
		if s := data[key][filer]; s != nil {
			for _, f := range s.years {
				if f.frame == yearPeriod(year) {
					return f.Val, true
				}
			}
		}
		return 0, false
	}

	measures := []struct {
		key, label string
		times      bool
		of         func(Peer) (float64, bool)
	}{
		{PeerGrowth, "Revenue growth", false, func(p Peer) (float64, bool) {
			r, ok := twelve("revenue", p.CIK)
			s := data["revenue"][p.CIK]
			if !ok || r <= 0 || s == nil {
				return 0, false
			}
			r0, ok := twelveMonths(s, ends[p.CIK].AddDate(-1, 0, 0))
			if !ok || r0 <= 0 {
				return 0, false
			}
			return r/r0 - 1, true
		}},
		{PeerGrossMargin, "Gross margin", false, share("grossProfit")},
		{PeerOperatingMargin, "Operating margin", false, share("operatingIncome")},
		{PeerNetMargin, "Net margin", false, share("netIncome")},
		{"", fmt.Sprintf("Free cash flow margin in %d", year), false, func(p Peer) (float64, bool) {
			cash, ok1 := inYear("operatingCashFlow", p.CIK)
			spent, ok2 := inYear("capitalExpenditure", p.CIK)
			rev, ok3 := inYear("revenue", p.CIK)
			if !ok1 || !ok2 || !ok3 || rev <= 0 {
				return 0, false
			}
			return (cash - spent) / rev, true
		}},
		{PeerRD, "R&D as % of revenue", false, share("researchDevelopment")},
		{PeerPS, "Price to sales", true, func(p Peer) (float64, bool) {
			r, ok := twelve("revenue", p.CIK)
			if p.MarketCap <= 0 || !ok || r <= 0 {
				return 0, false
			}
			return p.MarketCap / r, true
		}},
		{PeerPE, "P/E", true, func(p Peer) (float64, bool) {
			profit, ok := twelve("netIncome", p.CIK)
			if !ok || profit <= 0 || p.MarketCap <= 0 {
				return 0, false // a loss has no multiple
			}
			return p.MarketCap / profit, true
		}},
	}

	var self *Peer
	var others []Peer
	for _, p := range peers {
		switch {
		case p.CIK == cik:
			self = &p
		case p.CIK > 0:
			others = append(others, p)
		}
	}
	if self == nil {
		self = &Peer{CIK: cik}
	}

	group := &PeerGroup{Industry: industry, Year: year, Own: own.Period}
	sort.SliceStable(others, func(i, j int) bool { return others[i].MarketCap > others[j].MarketCap })
	for _, p := range others {
		if _, ok := ends[p.CIK]; ok {
			group.Size++
			if len(group.Largest) < largestNamed {
				group.Largest = append(group.Largest, p.Name)
			}
		}
	}
	for _, m := range measures {
		var values []float64
		for _, p := range others {
			if v, ok := m.of(p); ok {
				values = append(values, v)
			}
		}
		if len(values) < MinPeers {
			continue
		}
		slices.Sort(values)
		line := PeerLine{Label: m.label, Times: m.times, Count: len(values),
			Median: quantile(values, 0.5), Low: quantile(values, 0.25), High: quantile(values, 0.75)}
		v, ok := own.Figures[m.key]
		if m.key == "" || !ok {
			v, ok = m.of(*self)
		}
		if ok {
			line.Company, line.Reported = v, true
			for _, x := range values {
				if x < v {
					line.Below++
				}
			}
		}
		group.Lines = append(group.Lines, line)
	}
	if len(group.Lines) == 0 {
		return nil, nil
	}
	return group, nil
}

// withinDays is whether two days are within the slack a fiscal calendar of
// weeks gives: Micron's years end on the Thursday nearest the end of August.
func withinDays(a, b time.Time) bool {
	d := a.Sub(b)
	return d > -12*24*time.Hour && d < 12*24*time.Hour
}

// latestTwelve is the latest end, up to now, that a filer's twelve months
// can be worked out for: the newest of its years and quarters that can.
func latestTwelve(s *series, now time.Time) (time.Time, bool) {
	var ends []time.Time
	for _, f := range append(append([]fact{}, s.years...), s.quarters...) {
		if !f.End.After(now) {
			ends = append(ends, f.End)
		}
	}
	slices.SortFunc(ends, func(a, b time.Time) int { return b.Compare(a) })
	for _, end := range ends {
		if _, ok := twelveMonths(s, end); ok {
			return end, true
		}
	}
	return time.Time{}, false
}

// twelveMonths is a filer's figure for the twelve months to end: a full year
// that ends then, or the last full year before it, plus each quarter filed
// since, less the same quarter a year before. Where the year cannot be
// bridged, four quarters in a row that end then are summed instead.
func twelveMonths(s *series, end time.Time) (float64, bool) {
	var base *fact
	for i, y := range s.years {
		if y.End.Before(end.Add(12*24*time.Hour)) && end.Sub(y.End) < 300*24*time.Hour && (base == nil || y.End.After(base.End)) {
			base = &s.years[i]
		}
	}
	if base != nil {
		if withinDays(base.End, end) {
			return base.Val, true
		}
		sum, last, ok := base.Val, base.End, true
		for ok && !withinDays(last, end) && last.Before(end) {
			q, found := quarterAfter(s.quarters, last)
			before, found2 := quarterEnding(s.quarters, q.End.AddDate(-1, 0, 0))
			if !found || !found2 {
				ok = false
				break
			}
			sum += q.Val - before.Val
			last = q.End
		}
		if ok && withinDays(last, end) {
			return sum, true
		}
	}
	// Four quarters in a row, walking back from end.
	sum, at := 0.0, end
	for range 4 {
		q, found := quarterEnding(s.quarters, at)
		if !found {
			return 0, false
		}
		sum += q.Val
		at = q.Start.AddDate(0, 0, -1)
	}
	return sum, true
}

// quarterAfter is the quarter that begins the day after a period ends, give
// or take a few days.
func quarterAfter(quarters []fact, end time.Time) (fact, bool) {
	for _, q := range quarters {
		if d := q.Start.Sub(end); d > 0 && d < 8*24*time.Hour {
			return q, true
		}
	}
	return fact{}, false
}

// quarterEnding is the quarter that ends on a day, give or take the slack of
// a fiscal calendar of weeks.
func quarterEnding(quarters []fact, end time.Time) (fact, bool) {
	for _, q := range quarters {
		if withinDays(q.End, end) {
			return q, true
		}
	}
	return fact{}, false
}

// yearPeriod and quarterPeriods name frames: "CY2025", "CY2025Q3".
func yearPeriod(year int) string { return fmt.Sprintf("CY%d", year) }

// quarterPeriods are the calendar quarters that ended in the months back
// from now, newest first.
func quarterPeriods(now time.Time, months int) []string {
	var out []string
	from := now.AddDate(0, -months, 0)
	y, q := now.Year(), (int(now.Month())-1)/3+1
	for {
		// The quarter's last day.
		end := time.Date(y, time.Month(3*q)+1, 0, 0, 0, 0, 0, time.UTC)
		if end.Before(from) {
			return out
		}
		if !end.After(now) {
			out = append(out, fmt.Sprintf("CY%dQ%d", y, q))
		}
		if q--; q == 0 {
			y, q = y-1, 4
		}
	}
}

// quantile is the value a share q of the way through sorted values, between
// neighbours where it falls between them.
func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	i := int(pos)
	if i+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[i] + (pos-float64(i))*(sorted[i+1]-sorted[i])
}

func conceptFor(key string) (concept, bool) {
	for _, c := range incomeConcepts {
		if c.key == key {
			return c, true
		}
	}
	return concept{}, false
}

// frameFor is one concept's figure for every filer for one period, by CIK. A
// filer that tagged it under more than one of the concept's tags gets the one
// the snapshot prefers.
func (c *Client) frameFor(ctx context.Context, con concept, period string, dir string, now time.Time) (map[int]fact, error) {
	out := map[int]fact{}
	for _, ref := range slices.Backward(con.refs) {
		if ref.taxonomy != "us-gaap" {
			continue
		}
		got, err := c.frame(ctx, ref.tag, period, dir, now)
		if err != nil {
			return nil, err
		}
		for cik, v := range got {
			out[cik] = v
		}
	}
	return out, nil
}

// frame is one tag's figures for one period, read from dir when kept there
// recently enough.
func (c *Client) frame(ctx context.Context, tag, period string, dir string, now time.Time) (map[int]fact, error) {
	c.framesMu.Lock()
	defer c.framesMu.Unlock()
	path := ""
	if dir != "" {
		path = filepath.Join(dir, fmt.Sprintf("%s-%s.json", tag, period))
		fresh := frameFresh
		if settled(period, now) {
			fresh = frameSettled
		}
		if info, err := os.Stat(path); err == nil && now.Sub(info.ModTime()) < fresh {
			if data, err := os.ReadFile(path); err == nil {
				// A frame kept before 4 October 2026 holds bare figures
				// without their dates, fails here, and is read again.
				var kept map[int]fact
				if json.Unmarshal(data, &kept) == nil {
					return kept, nil
				}
			}
		}
	}

	var body struct {
		Data []struct {
			CIK   int     `json:"cik"`
			Val   float64 `json:"val"`
			Start string  `json:"start"`
			End   string  `json:"end"`
		} `json:"data"`
	}
	url := fmt.Sprintf(frameURL, tag, period)
	if c.FramesURL != "" {
		url = fmt.Sprintf(c.FramesURL, tag, period)
	}
	err := c.getJSON(ctx, url, &body)
	if errors.Is(err, ErrNotReported) {
		err = nil // nobody tagged it then, which is kept like any answer
	}
	if err != nil {
		return nil, fmt.Errorf("the SEC's %s figures for %s: %w", tag, period, err)
	}
	out := make(map[int]fact, len(body.Data))
	for _, d := range body.Data {
		start, err1 := time.Parse(time.DateOnly, d.Start)
		end, err2 := time.Parse(time.DateOnly, d.End)
		if err1 != nil || err2 != nil {
			continue
		}
		out[d.CIK] = fact{Val: d.Val, Start: start, End: end}
	}
	if path != "" {
		if data, err := json.Marshal(out); err == nil {
			if os.MkdirAll(dir, 0o755) == nil {
				_ = os.WriteFile(path, data, 0o644) // a frame not kept is read again next time
			}
		}
	}
	return out, nil
}

// settled is whether a frame's period ended over a year before now, so late
// filers have long since filed.
func settled(period string, now time.Time) bool {
	var y, q int
	if n, _ := fmt.Sscanf(period, "CY%dQ%d", &y, &q); n == 2 {
		return now.Sub(time.Date(y, time.Month(3*q)+1, 0, 0, 0, 0, 0, time.UTC)) > 365*24*time.Hour
	}
	if n, _ := fmt.Sscanf(period, "CY%d", &y); n == 1 {
		return now.Sub(time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)) > 365*24*time.Hour
	}
	return false
}

// Show writes one of the line's values as a reader would say it: "26.1%",
// or "8.2×" for a multiple.
func (l PeerLine) Show(v float64) string {
	if l.Times {
		return fmt.Sprintf("%.1f×", v)
	}
	return fmt.Sprintf("%.1f%%", 100*v)
}

// Facts writes the comparison out for the analysis and the verdicts.
func (g *PeerGroup) Facts() string {
	if g == nil || len(g.Lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nHow the company compares with its industry group, from the SEC's figures. Each company is on its latest twelve months filed")
	if g.Own != "" {
		fmt.Fprintf(&b, ", and this one on %s, the same months as its own P/E and price to sales above", g.Own)
	}
	fmt.Fprintf(&b, ". The free cash flow margin is on each filer's fiscal year nearest calendar %d, as cash flows are filed only year to date. ", g.Year)
	fmt.Fprintf(&b, "The group is Nasdaq's %q: %d other US-listed companies worth US$%s or more that report in US dollars under US accounting rules. ",
		g.Industry, g.Size, Number(PeerMarketCap/1e6)+"m")
	if len(g.Largest) > 0 {
		fmt.Fprintf(&b, "Its largest are %s. ", strings.Join(g.Largest, ", "))
	}
	b.WriteString("Nasdaq's groups can be broad, so say so where the group mixes different businesses. The group's P/E and price to sales are today's market value over each company's twelve months.\n")
	for _, l := range g.Lines {
		show := l.Show
		company := "not reported by this company"
		if l.Reported {
			company = show(l.Company)
		}
		fmt.Fprintf(&b, "- %s: %s. The group's median is %s, and its 25th to 75th percentile runs from %s to %s.",
			l.Label, company, show(l.Median), show(l.Low), show(l.High))
		if l.Reported {
			fmt.Fprintf(&b, " Higher than %d of the %d with this figure.", l.Below, l.Count)
		} else {
			fmt.Fprintf(&b, " %d of the group have this figure.", l.Count)
		}
		b.WriteString("\n")
	}
	return b.String()
}
