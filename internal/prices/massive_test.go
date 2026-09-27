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

// A session's bars come back by symbol; a day not served yet is an empty
// map, not an error.
func TestASessionIsReadWholeAndADayNotServedIsEmpty(t *testing.T) {
	m, asked := massiveStub(t, map[string]string{
		"2026-09-25": `{"T":"AAA","o":100,"c":110,"v":1000000,"t":1790366400000,"n":5321},{"T":"BRK.B","o":500,"c":505,"v":1000}`,
	})
	bars, err := m.Session(context.Background(), time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 || bars["AAA"].Open != 100 || bars["AAA"].Close != 110 || bars["BRK.B"].Volume != 1000 {
		t.Errorf("bars = %+v", bars)
	}
	none, err := m.Session(context.Background(), time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC))
	if err != nil || len(none) != 0 {
		t.Errorf("a day not served = %v, %v", none, err)
	}
	if strings.Join(*asked, ",") != "2026-09-25,2026-09-28" {
		t.Errorf("asked for %v", *asked)
	}
}

// A refused key is reported as such, which no retry will change.
func TestARefusedKeyIsReportedAtOnce(t *testing.T) {
	m, asked := massiveStub(t, nil)
	m.APIKey = "wrong"
	_, err := m.Session(context.Background(), time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrMassiveKey) || len(*asked) != 0 {
		t.Errorf("err = %v after %d requests", err, len(*asked))
	}
}

// Splits are read for a stretch of days, following the next page, with the
// key added to each request and never taken from the reply.
func TestSplitsAreReadAcrossPages(t *testing.T) {
	var srv *httptest.Server
	var keys []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.URL.Query().Get("apiKey"))
		if r.URL.Query().Get("cursor") == "" {
			if r.URL.Query().Get("execution_date.gte") != "2026-09-01" || r.URL.Query().Get("execution_date.lte") != "2026-09-25" {
				t.Errorf("asked for %s", r.URL.RawQuery)
			}
			fmt.Fprintf(w, `{"results":[{"ticker":"NVDA","execution_date":"2026-09-10","split_from":1,"split_to":10}],"next_url":"%s/v3/reference/splits?cursor=two"}`, srv.URL)
			return
		}
		w.Write([]byte(`{"results":[{"ticker":"DPU","execution_date":"2026-09-17","split_from":50,"split_to":1},{"ticker":"BAD","execution_date":"soon","split_from":1,"split_to":2}]}`))
	}))
	defer srv.Close()
	m := &Massive{APIKey: "k", URL: srv.URL, HTTP: srv.Client(), Gap: time.Millisecond}

	got, err := m.Splits(context.Background(), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Symbol != "NVDA" || got[0].To != 10 || got[1].Symbol != "DPU" || got[1].From != 50 {
		t.Errorf("splits = %+v", got)
	}
	if strings.Join(keys, ",") != "k,k" {
		t.Errorf("keys sent = %v", keys)
	}
}

// A common share's symbol is letters with perhaps a class; a warrant, a unit,
// a right or a preferred share is not one.
func TestCommonShares(t *testing.T) {
	for sym, want := range map[string]bool{"AAPL": true, "BRK.B": true, "GOOGL": true, "WARRW": false, "SPACU": false, "KIMpL": false, "ACP^A": false} {
		if got := CommonShare(sym); got != want {
			t.Errorf("CommonShare(%q) = %v", sym, got)
		}
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
