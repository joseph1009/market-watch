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
// Only US-GAAP figures in dollars are compared. A foreign filer reporting
// under IFRS, or in its own currency, is left out of the group rather than
// converted.

const (
	frameURL = "https://data.sec.gov/api/xbrl/frames/us-gaap/%s/USD/CY%d.json"

	// frameFresh is how long a frame kept on disk is used before it is read
	// again. A year's frame fills slowly as filers report, and a day's change
	// to it moves no median.
	frameFresh = 7 * 24 * time.Hour

	// MinPeers is the fewest companies a comparison is made against. Below it
	// a middle value is one or two companies' figures.
	MinPeers = 5

	// PeerMarketCap is the smallest company counted in a group: below it, a
	// shell or a company just listed pulls the middle about.
	PeerMarketCap = 500e6

	// largestNamed is how many of the group's largest companies are named, so
	// the reader can judge whether the group is a fair one.
	largestNamed = 6
)

// Peer is a company in the same industry group.
type Peer struct {
	Symbol    string
	Name      string
	CIK       int
	MarketCap float64 // in dollars, as the market's list gives it today
}

// PeerGroup is where a company stands in its industry group, on one
// calendar year's figures.
type PeerGroup struct {
	Industry string
	Year     int
	Size     int // the companies in the group other than this one
	Largest  []string
	Lines    []PeerLine
}

// PeerLine is one measure: the company's figure, the group's middle value
// and the range of its middle half, and how many of the group it beats.
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

// frames are the figures one comparison reads: each concept's tags, in the
// order the snapshot prefers them.
var peerConcepts = []string{"revenue", "grossProfit", "operatingIncome", "netIncome",
	"operatingCashFlow", "capitalExpenditure", "researchDevelopment"}

// Peers sets the company against the others in its industry group, on the
// last calendar year whose figures are mostly filed. Industry names the
// group, as the market's list does. The figures are read from the SEC's
// frames, and kept in dir for a week where dir is set.
func (c *Client) Peers(ctx context.Context, cik int, industry string, peers []Peer, dir string, now time.Time) (*PeerGroup, error) {
	year := now.Year() - 1
	if now.Month() < time.April {
		year-- // last year's annual reports are not all filed until March
	}

	figures := map[string][2]map[int]float64{} // concept → this year, the year before
	for _, key := range peerConcepts {
		con, ok := conceptFor(key)
		if !ok {
			continue
		}
		var pair [2]map[int]float64
		for i, y := range []int{year, year - 1} {
			if i == 1 && key != "revenue" {
				break // only revenue is needed a year earlier, for growth
			}
			got, err := c.frameFor(ctx, con, y, dir, now)
			if err != nil {
				return nil, err
			}
			pair[i] = got
		}
		figures[key] = pair
	}
	rev, rev0 := figures["revenue"][0], figures["revenue"][1]
	at := func(key string, cik int) (float64, bool) {
		v, ok := figures[key][0][cik]
		return v, ok
	}
	share := func(key string) func(Peer) (float64, bool) {
		return func(p Peer) (float64, bool) {
			v, ok := at(key, p.CIK)
			r := rev[p.CIK]
			if !ok || r <= 0 {
				return 0, false
			}
			return v / r, true
		}
	}

	measures := []struct {
		label string
		times bool
		of    func(Peer) (float64, bool)
	}{
		{"Revenue growth", false, func(p Peer) (float64, bool) {
			r, r0 := rev[p.CIK], rev0[p.CIK]
			if r <= 0 || r0 <= 0 {
				return 0, false
			}
			return r/r0 - 1, true
		}},
		{"Gross margin", false, share("grossProfit")},
		{"Operating margin", false, share("operatingIncome")},
		{"Net margin", false, share("netIncome")},
		{"Free cash flow margin", false, func(p Peer) (float64, bool) {
			cash, ok1 := at("operatingCashFlow", p.CIK)
			spent, ok2 := at("capitalExpenditure", p.CIK)
			if !ok1 || !ok2 || rev[p.CIK] <= 0 {
				return 0, false
			}
			return (cash - spent) / rev[p.CIK], true
		}},
		{"R&D as % of revenue", false, share("researchDevelopment")},
		{fmt.Sprintf("Market value to sales in %d", year), true, func(p Peer) (float64, bool) {
			if p.MarketCap <= 0 || rev[p.CIK] <= 0 {
				return 0, false
			}
			return p.MarketCap / rev[p.CIK], true
		}},
		{fmt.Sprintf("Market value to profit in %d", year), true, func(p Peer) (float64, bool) {
			profit, ok := at("netIncome", p.CIK)
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

	group := &PeerGroup{Industry: industry, Year: year}
	sort.SliceStable(others, func(i, j int) bool { return others[i].MarketCap > others[j].MarketCap })
	for _, p := range others {
		if _, ok := rev[p.CIK]; ok {
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
		if v, ok := m.of(*self); ok {
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

// frameFor is one concept's figure for every filer for one calendar year, by
// CIK. A filer that tagged it under more than one of the concept's tags gets
// the one the snapshot prefers.
func (c *Client) frameFor(ctx context.Context, con concept, year int, dir string, now time.Time) (map[int]float64, error) {
	out := map[int]float64{}
	for _, ref := range slices.Backward(con.refs) {
		if ref.taxonomy != "us-gaap" {
			continue
		}
		got, err := c.frame(ctx, ref.tag, year, dir, now)
		if err != nil {
			return nil, err
		}
		for cik, v := range got {
			out[cik] = v
		}
	}
	return out, nil
}

// frame is one tag's figures for one calendar year, read from dir when kept
// there within the week.
func (c *Client) frame(ctx context.Context, tag string, year int, dir string, now time.Time) (map[int]float64, error) {
	c.framesMu.Lock()
	defer c.framesMu.Unlock()
	path := ""
	if dir != "" {
		path = filepath.Join(dir, fmt.Sprintf("%s-CY%d.json", tag, year))
		if info, err := os.Stat(path); err == nil && now.Sub(info.ModTime()) < frameFresh {
			if data, err := os.ReadFile(path); err == nil {
				var kept map[int]float64
				if json.Unmarshal(data, &kept) == nil {
					return kept, nil
				}
			}
		}
	}

	var body struct {
		Data []struct {
			CIK int     `json:"cik"`
			Val float64 `json:"val"`
		} `json:"data"`
	}
	url := fmt.Sprintf(frameURL, tag, year)
	if c.FramesURL != "" {
		url = fmt.Sprintf(c.FramesURL, tag, year)
	}
	err := c.getJSON(ctx, url, &body)
	if errors.Is(err, ErrNotReported) {
		err = nil // nobody tagged it that year, which is kept like any answer
	}
	if err != nil {
		return nil, fmt.Errorf("the SEC's %s figures for %d: %w", tag, year, err)
	}
	out := make(map[int]float64, len(body.Data))
	for _, d := range body.Data {
		out[d.CIK] = d.Val
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

// Show writes one of the line's values as a reader would say it: "26.1%",
// or "8.2 times" for a multiple.
func (l PeerLine) Show(v float64) string {
	if l.Times {
		return fmt.Sprintf("%.1f times", v)
	}
	return fmt.Sprintf("%.1f%%", 100*v)
}

// Facts writes the comparison out for the analysis and the verdicts.
func (g *PeerGroup) Facts() string {
	if g == nil || len(g.Lines) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nHow the company compares with its industry group, from the SEC's figures for calendar %d (each filer's fiscal year nearest to it). ", g.Year)
	fmt.Fprintf(&b, "The group is Nasdaq's %q: %d other US-listed companies worth US$%s or more that report in US dollars under US accounting rules. ",
		g.Industry, g.Size, Number(PeerMarketCap/1e6)+"m")
	if len(g.Largest) > 0 {
		fmt.Fprintf(&b, "Its largest are %s. ", strings.Join(g.Largest, ", "))
	}
	b.WriteString("Nasdaq's groups can be broad, so say so where the group mixes different businesses. The market values are today's, set against that year's figures, so a company growing fast looks dearer on them than on this year's.\n")
	for _, l := range g.Lines {
		show := l.Show
		company := "not reported by this company"
		if l.Reported {
			company = show(l.Company)
		}
		fmt.Fprintf(&b, "- %s: %s. The group's middle value is %s, and its middle half runs from %s to %s.",
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
