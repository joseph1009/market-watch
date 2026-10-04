package fundamentals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// framed is one filer's figure in a test frame, with its first and last day.
type framed struct {
	val        float64
	start, end string
}

// calendarYear and calendarQuarter are a calendar filer's periods.
func calendarYear(y int, v float64) framed {
	return framed{v, fmt.Sprintf("%d-01-01", y), fmt.Sprintf("%d-12-31", y)}
}

func calendarQuarter(y, q int, v float64) framed {
	start := time.Date(y, time.Month(3*q-2), 1, 0, 0, 0, 0, time.UTC)
	return framed{v, start.Format(time.DateOnly), start.AddDate(0, 3, -1).Format(time.DateOnly)}
}

// framesServer answers the frames API from a table of tag and period to each
// filer's figure, and counts the requests.
func framesServer(t *testing.T, figures map[string]map[int]framed) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		// /frames/<tag>/<period>
		got, ok := figures[strings.TrimPrefix(r.URL.Path, "/frames/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Data []map[string]any `json:"data"`
		}
		for cik, f := range got {
			body.Data = append(body.Data, map[string]any{"cik": cik, "val": f.val, "start": f.start, "end": f.end})
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// years is each filer's calendar-year figure.
func years(y int, values map[int]float64) map[int]framed {
	out := map[int]framed{}
	for cik, v := range values {
		out[cik] = calendarYear(y, v)
	}
	return out
}

// Six chip makers, all on calendar years with no quarter filed since 2025:
// the company (CIK 1) grew fastest and earns the widest operating margin; one
// peer files no 2024 revenue, so has no growth.
func chipFrames() map[string]map[int]framed {
	return map[string]map[int]framed{
		"Revenues/CY2025":            years(2025, map[int]float64{1: 300, 2: 100, 3: 200, 4: 400, 5: 500, 6: 600, 7: 700}),
		"Revenues/CY2024":            years(2024, map[int]float64{1: 150, 2: 100, 3: 190, 4: 360, 5: 450, 6: 540}),
		"OperatingIncomeLoss/CY2025": years(2025, map[int]float64{1: 120, 2: 10, 3: 20, 4: 40, 5: 50, 6: 60, 7: 70}),
		"NetIncomeLoss/CY2025":       years(2025, map[int]float64{1: 90, 2: -5, 3: 10, 4: 30, 5: 40, 6: 50, 7: 60}),
	}
}

func chipPeers() []Peer {
	peers := []Peer{{Symbol: "MU", Name: "Micron", CIK: 1, MarketCap: 3000}}
	for cik := 2; cik <= 7; cik++ {
		peers = append(peers, Peer{Symbol: fmt.Sprintf("P%d", cik), Name: fmt.Sprintf("Peer %d", cik), CIK: cik, MarketCap: float64(cik * 1000)})
	}
	return peers
}

func peerLines(g *PeerGroup) map[string]PeerLine {
	lines := map[string]PeerLine{}
	for _, l := range g.Lines {
		lines[l.Label] = l
	}
	return lines
}

func TestPeersPlaceTheCompanyInItsGroup(t *testing.T) {
	srv, asked := framesServer(t, chipFrames())
	c := &Client{FramesURL: srv.URL + "/frames/%s/%s", Unpaced: true}
	dir := t.TempDir()
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

	g, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), OwnFigures{}, dir, now)
	if err != nil || g == nil {
		t.Fatalf("Peers = %v, %v", g, err)
	}
	if g.Year != 2025 || g.Size != 6 || g.Largest[0] != "Peer 7" {
		t.Errorf("group = year %d, size %d, largest %v; want 2025, 6, Peer 7 first", g.Year, g.Size, g.Largest)
	}
	lines := peerLines(g)
	op := lines["Operating margin"]
	if !op.Reported || op.Company != 0.4 || op.Median != 0.1 || op.Below != 6 || op.Count != 6 {
		t.Errorf("operating margin = %+v, want 40%% against a middle of 10%%, above all 6", op)
	}
	if growth := lines["Revenue growth"]; growth.Count != 5 || growth.Company != 1 {
		t.Errorf("growth = %+v, want 100%% against the 5 that filed both years", growth)
	}
	if _, ok := lines["Gross margin"]; ok {
		t.Error("a measure no peer reports was compared")
	}
	if pe := lines["P/E"]; pe.Count != 5 {
		t.Errorf("P/E counted %d peers, want the 5 with a profit", pe.Count)
	}

	facts := g.Facts()
	for _, want := range []string{
		"Each company is on its latest twelve months filed", "nearest calendar 2025",
		`Nasdaq's "Semiconductors": 6 other US-listed companies worth US$500m or more`,
		"Its largest are Peer 7, Peer 6",
		"- Operating margin: 40.0%. The group's median is 10.0%, and its 25th to 75th percentile runs from 10.0% to 10.0%. Higher than 6 of the 6 with this figure.",
		"- Price to sales: 10.0×.",
	} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %q:\n%s", want, facts)
		}
	}

	// The frames are read from disk, not asked for again.
	before := asked.Load()
	if _, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), OwnFigures{}, dir, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if asked.Load() != before {
		t.Errorf("asked the SEC %d more times for frames kept on disk", asked.Load()-before)
	}
}

// A filer's twelve months are its last full year, plus the quarters filed
// since, less the same quarters a year before. Micron's years end in late
// August and its fourth quarter is never filed on its own, so its twelve
// months to May 2026 come from the year to August 2025 and three quarters
// either side.
func TestPeersPutEveryCompanyOnItsLatestTwelveMonths(t *testing.T) {
	q := func(start, end string, v float64) framed { return framed{v, start, end} }
	frames := map[string]map[int]framed{
		"Revenues/CY2025": {
			1: q("2024-08-30", "2025-08-28", 37), // Micron's fiscal 2025
			2: calendarYear(2025, 400),
		},
		"Revenues/CY2024Q4": {1: q("2024-08-30", "2024-11-28", 8)},
		"Revenues/CY2025Q1": {1: q("2024-11-29", "2025-02-27", 8), 2: calendarQuarter(2025, 1, 90)},
		"Revenues/CY2025Q2": {1: q("2025-02-28", "2025-05-29", 9), 2: calendarQuarter(2025, 2, 95)},
		"Revenues/CY2025Q4": {1: q("2025-08-29", "2025-11-27", 13.6)},
		"Revenues/CY2026Q1": {1: q("2025-11-28", "2026-02-26", 23.9), 2: calendarQuarter(2026, 1, 110)},
		"Revenues/CY2026Q2": {1: q("2026-02-27", "2026-05-28", 41.5), 2: calendarQuarter(2026, 2, 120)},
	}
	for cik := 3; cik <= 7; cik++ { // five more on the calendar year alone
		frames["Revenues/CY2025"][cik] = calendarYear(2025, 100)
	}
	srv, _ := framesServer(t, frames)
	c := &Client{FramesURL: srv.URL + "/frames/%s/%s", Unpaced: true}
	peers := chipPeers()
	g, err := c.Peers(context.Background(), 1, "Semiconductors", peers, OwnFigures{}, "", time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || g == nil {
		t.Fatalf("Peers = %v, %v", g, err)
	}
	ps := peerLines(g)["Price to sales"]
	// Micron: 37 + 13.6 + 23.9 + 41.5 - 8 - 8 - 9 = 91; 3000 / 91.
	if want := 3000 / 91.0; ps.Company < want-0.01 || ps.Company > want+0.01 {
		t.Errorf("Micron's price to sales = %.2f, want %.2f on its twelve months to May 2026", ps.Company, want)
	}
	if ps.Count != 6 || ps.Below != 2 {
		t.Errorf("price to sales = %+v, want Micron above 2 of 6 peers", ps)
	}

	// A calendar filer: 400 + 110 + 120 - 90 - 95 for the twelve months to
	// June 2026.
	s := &series{years: []fact{factOf(calendarYear(2025, 400))}}
	for _, f := range []framed{calendarQuarter(2025, 1, 90), calendarQuarter(2025, 2, 95), calendarQuarter(2026, 1, 110), calendarQuarter(2026, 2, 120)} {
		s.quarters = append(s.quarters, factOf(f))
	}
	end, ok := latestTwelve(s, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if v, _ := twelveMonths(s, end); !ok || v != 445 || end.Format(time.DateOnly) != "2026-06-30" {
		t.Errorf("twelve months = %v to %s, want 445 to 2026-06-30", v, end.Format(time.DateOnly))
	}
}

func factOf(f framed) fact {
	start, _ := time.Parse(time.DateOnly, f.start)
	end, _ := time.Parse(time.DateOnly, f.end)
	return fact{Val: f.val, Start: start, End: end}
}

// The company's own figures, worked out as the page's box works them out,
// stand in for its frames', so the table and the box agree.
func TestPeersUseTheCompanysOwnFigures(t *testing.T) {
	srv, _ := framesServer(t, chipFrames())
	c := &Client{FramesURL: srv.URL + "/frames/%s/%s", Unpaced: true}
	own := OwnFigures{Period: "Sep 2025–Aug 2026", Figures: map[string]float64{PeerPE: 14.5, PeerOperatingMargin: 0.5}}
	g, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), own, "", time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || g == nil {
		t.Fatalf("Peers = %v, %v", g, err)
	}
	lines := peerLines(g)
	if pe := lines["P/E"]; pe.Company != 14.5 {
		t.Errorf("P/E = %v, want the box's 14.5", pe.Company)
	}
	if op := lines["Operating margin"]; op.Company != 0.5 {
		t.Errorf("operating margin = %v, want the company's own 50%%", op.Company)
	}
	if growth := lines["Revenue growth"]; growth.Company != 1 {
		t.Errorf("growth = %v, want the frames' where no own figure is given", growth.Company)
	}
	if facts := g.Facts(); !strings.Contains(facts, "this one on Sep 2025–Aug 2026") {
		t.Errorf("facts do not name the company's months:\n%s", facts)
	}
}

// Early in the year last year's reports are not all filed, so the year
// before is compared.
func TestPeersUseTheYearBeforeUntilApril(t *testing.T) {
	srv, _ := framesServer(t, map[string]map[int]framed{})
	c := &Client{FramesURL: srv.URL + "/frames/%s/%s", Unpaced: true}
	g, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), OwnFigures{}, "", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || g != nil {
		t.Errorf("Peers = %+v, %v; want nothing to compare and no error", g, err)
	}
}

// A company the group's figures do not include is still placed nowhere, but
// the group is described.
func TestPeersSayWhenTheCompanyHasNoFigure(t *testing.T) {
	srv, _ := framesServer(t, chipFrames())
	c := &Client{FramesURL: srv.URL + "/frames/%s/%s", Unpaced: true}
	g, err := c.Peers(context.Background(), 99, "Semiconductors", chipPeers()[1:], OwnFigures{}, "", time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || g == nil {
		t.Fatalf("Peers = %v, %v", g, err)
	}
	if facts := g.Facts(); !strings.Contains(facts, "- Operating margin: not reported by this company.") || !strings.Contains(facts, "6 of the group have this figure.") {
		t.Errorf("facts:\n%s", facts)
	}
}

func TestQuarterPeriodsNameTheQuartersThatHaveEnded(t *testing.T) {
	got := quarterPeriods(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), 7)
	if want := []string{"CY2026Q3", "CY2026Q2", "CY2026Q1"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("quarterPeriods = %v, want %v", got, want)
	}
}
