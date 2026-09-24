package prices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// massiveStub serves grouped bars for the dates given, and 403 -- "not served
// yet" -- for any other, counting what was asked.
func massiveStub(t *testing.T, days map[string]string) (*Massive, *[]string) {
	t.Helper()
	var (
		mu    sync.Mutex
		asked []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apiKey") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		day := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		asked = append(asked, day)
		mu.Unlock()
		body, ok := days[day]
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprintf(w, `{"status":"OK","results":[%s]}`, body)
	}))
	t.Cleanup(srv.Close)
	return &Massive{APIKey: "k", URL: srv.URL, HTTP: srv.Client(), Gap: time.Millisecond}, &asked
}

// The two latest sessions are compared across the market, walking back past
// a day not yet served and a weekend without asking for the weekend; what is
// too cheap, too thinly traded, not a common share or turned away by Keep is
// left out, and the rest come biggest move first.
func TestMoversCompareTheLastTwoSessionsAcrossTheMarket(t *testing.T) {
	m, asked := massiveStub(t, map[string]string{
		"2026-09-25": `{"T":"AAA","c":110,"v":1000000,"t":1790366400000,"n":5321},{"T":"BBB","c":80,"v":1000000},{"T":"PENNY","c":1.5,"v":90000000},{"T":"THIN","c":50,"v":100},{"T":"WARRW","c":9,"v":9000000},{"T":"KIMpL","c":30,"v":1000000},{"T":"FUND","c":130,"v":1000000},{"T":"BRK.B","c":505,"v":1000000}`,
		"2026-09-24": `{"T":"AAA","c":100,"v":1000000},{"T":"BBB","c":100,"v":1000000},{"T":"PENNY","c":1,"v":90000000},{"T":"THIN","c":25,"v":100},{"T":"WARRW","c":3,"v":9000000},{"T":"KIMpL","c":20,"v":1000000},{"T":"FUND","c":100,"v":1000000},{"T":"BRK.B","c":500,"v":1000000}`,
	})
	// Monday the 28th, New York evening: the 28th is not served yet, the
	// weekend is skipped, and Friday and Thursday are the two sessions.
	now := time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC)
	got, err := m.Movers(context.Background(), now, MoverRules{
		MinPrice: 5, MinDollarVolume: 1e6, Limit: 10,
		Keep: func(s string) bool { return s != "FUND" },
	})
	if err != nil {
		t.Fatal(err)
	}
	var symbols []string
	for _, g := range got {
		symbols = append(symbols, fmt.Sprintf("%s %+.0f%%", g.Symbol, g.Percent))
	}
	if strings.Join(symbols, ", ") != "BBB -20%, AAA +10%, BRK.B +1%" {
		t.Errorf("movers = %v", symbols)
	}
	if strings.Join(*asked, ",") != "2026-09-28,2026-09-25,2026-09-24" {
		t.Errorf("asked for %v", *asked)
	}
}

// A refused key stops the search at once rather than walking back a week.
func TestARefusedKeyIsReportedAtOnce(t *testing.T) {
	m, asked := massiveStub(t, nil)
	m.APIKey = "wrong"
	_, err := m.Movers(context.Background(), time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC), MoverRules{})
	if !errors.Is(err, ErrMassiveKey) || len(*asked) != 0 {
		t.Errorf("err = %v after %d requests", err, len(*asked))
	}
}

// Requests are spaced to the plan's limit.
func TestMassiveRequestsAreSpaced(t *testing.T) {
	m, _ := massiveStub(t, map[string]string{"2026-09-24": `{"T":"AAA","c":1,"v":1}`})
	m.Gap = 40 * time.Millisecond
	start := time.Now()
	for range 3 {
		if _, err := m.Session(context.Background(), time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 80*time.Millisecond {
		t.Errorf("three requests took %v, want at least two gaps", took)
	}
}
