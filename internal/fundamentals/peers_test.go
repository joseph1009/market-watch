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

// framesServer answers the frames API from a table of tag and year to each
// filer's figure, and counts the requests.
func framesServer(t *testing.T, figures map[string]map[int]float64) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		// /frames/<tag>/CY<year>
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/frames/"), "/")
		got, ok := figures[parts[0]+"/"+parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Data []map[string]any `json:"data"`
		}
		for cik, v := range got {
			body.Data = append(body.Data, map[string]any{"cik": cik, "val": v})
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// Six chip makers: the company (CIK 1) grew fastest and earns the widest
// operating margin; one peer files no 2024 revenue, so has no growth.
func chipFrames() map[string]map[int]float64 {
	return map[string]map[int]float64{
		"Revenues/CY2025":            {1: 300, 2: 100, 3: 200, 4: 400, 5: 500, 6: 600, 7: 700},
		"Revenues/CY2024":            {1: 150, 2: 100, 3: 190, 4: 360, 5: 450, 6: 540},
		"OperatingIncomeLoss/CY2025": {1: 120, 2: 10, 3: 20, 4: 40, 5: 50, 6: 60, 7: 70},
		"NetIncomeLoss/CY2025":       {1: 90, 2: -5, 3: 10, 4: 30, 5: 40, 6: 50, 7: 60},
	}
}

func chipPeers() []Peer {
	peers := []Peer{{Symbol: "MU", Name: "Micron", CIK: 1, MarketCap: 3000}}
	for cik := 2; cik <= 7; cik++ {
		peers = append(peers, Peer{Symbol: fmt.Sprintf("P%d", cik), Name: fmt.Sprintf("Peer %d", cik), CIK: cik, MarketCap: float64(cik * 1000)})
	}
	return peers
}

func TestPeersPlaceTheCompanyInItsGroup(t *testing.T) {
	srv, asked := framesServer(t, chipFrames())
	c := &Client{FramesURL: srv.URL + "/frames/%s/CY%d", Unpaced: true}
	dir := t.TempDir()
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

	g, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), dir, now)
	if err != nil || g == nil {
		t.Fatalf("Peers = %v, %v", g, err)
	}
	if g.Year != 2025 || g.Size != 6 || g.Largest[0] != "Peer 7" {
		t.Errorf("group = year %d, size %d, largest %v; want 2025, 6, Peer 7 first", g.Year, g.Size, g.Largest)
	}
	lines := map[string]PeerLine{}
	for _, l := range g.Lines {
		lines[l.Label] = l
	}
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
	if pe := lines["Market value to profit in 2025"]; pe.Count != 5 {
		t.Errorf("profit multiple counted %d peers, want the 5 with a profit", pe.Count)
	}

	facts := g.Facts()
	for _, want := range []string{
		`calendar 2025`, `Nasdaq's "Semiconductors": 6 other US-listed companies worth US$500m or more`,
		"Its largest are Peer 7, Peer 6",
		"- Operating margin: 40.0%. The group's middle value is 10.0%, and its middle half runs from 10.0% to 10.0%. Higher than 6 of the 6 with this figure.",
		"- Market value to sales in 2025: 10.0 times.",
	} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %q:\n%s", want, facts)
		}
	}

	// A week's frames are read from disk, not asked for again.
	before := asked.Load()
	if _, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), dir, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if asked.Load() != before {
		t.Errorf("asked the SEC %d more times for frames kept on disk", asked.Load()-before)
	}
}

// Early in the year last year's reports are not all filed, so the year
// before is compared.
func TestPeersUseTheYearBeforeUntilApril(t *testing.T) {
	srv, _ := framesServer(t, map[string]map[int]float64{})
	c := &Client{FramesURL: srv.URL + "/frames/%s/CY%d", Unpaced: true}
	g, err := c.Peers(context.Background(), 1, "Semiconductors", chipPeers(), "", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || g != nil {
		t.Errorf("Peers = %+v, %v; want nothing to compare and no error", g, err)
	}
}

// A company the group's figures do not include is still placed nowhere, but
// the group is described.
func TestPeersSayWhenTheCompanyHasNoFigure(t *testing.T) {
	srv, _ := framesServer(t, chipFrames())
	c := &Client{FramesURL: srv.URL + "/frames/%s/CY%d", Unpaced: true}
	g, err := c.Peers(context.Background(), 99, "Semiconductors", chipPeers()[1:], "", time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || g == nil {
		t.Fatalf("Peers = %v, %v", g, err)
	}
	if facts := g.Facts(); !strings.Contains(facts, "- Operating margin: not reported by this company.") || !strings.Contains(facts, "6 of the group have this figure.") {
		t.Errorf("facts:\n%s", facts)
	}
}
