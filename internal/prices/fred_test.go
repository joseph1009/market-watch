package prices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// FRED writes a missing observation as ".", which every numeric parser rejects.
// Holidays land in a business-day series constantly, so this is the normal case
// rather than an edge one.
const body = `{"observations":[
 {"date":"2026-09-09","value":"4.32"},
 {"date":"2026-09-08","value":"."},
 {"date":"2026-09-07","value":"4.28"},
 {"date":"2026-09-04","value":"4.25"},
 {"date":"2026-09-03","value":"4.21"},
 {"date":"2026-09-02","value":"4.19"},
 {"date":"2026-09-01","value":"4.11"}]}`

func newStub(t *testing.T, payload string) *FRED {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") == "" {
			t.Error("request carried no api_key")
		}
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return &FRED{APIKey: "test-key", HTTP: srv.Client(), URL: srv.URL}
}

func TestFetchReadsLatestAndSkipsMissingObservations(t *testing.T) {
	c := newStub(t, body)

	readings, errs := c.Fetch(context.Background(), []Indicator{{ID: "DGS10", Label: "10-year", Unit: "%"}})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(readings) != 1 {
		t.Fatalf("got %d readings, want 1", len(readings))
	}

	r := readings[0]
	if r.Latest != 4.32 {
		t.Errorf("Latest = %v, want 4.32", r.Latest)
	}
	// The "." row is skipped, so the previous value is the 7th, not the 8th.
	if !r.HasPrevious || r.Previous != 4.28 {
		t.Errorf("Previous = %v (%v), want 4.28", r.Previous, r.HasPrevious)
	}
	if got := r.Change(); got < 0.039 || got > 0.041 {
		t.Errorf("Change = %v, want ~0.04", got)
	}
	if r.AsOf.Format("2006-01-02") != "2026-09-09" {
		t.Errorf("AsOf = %s", r.AsOf)
	}
}

// A series with one observation must report "not known", not a move of zero.
func TestSingleObservationReportsNoChange(t *testing.T) {
	c := newStub(t, `{"observations":[{"date":"2026-09-09","value":"4.32"}]}`)

	readings, _ := c.Fetch(context.Background(), []Indicator{{ID: "DGS10"}})
	if len(readings) != 1 {
		t.Fatalf("got %d readings", len(readings))
	}
	if readings[0].HasPrevious || readings[0].HasWeekAgo {
		t.Error("a lone observation reported a change it cannot know")
	}
}

// Inflation is asked for as a yearly rate, and compared with last month rather
// than with a week of business days it does not have.
func TestAMonthlyRateAsksForTheTransformAndComparesMonths(t *testing.T) {
	var units string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		units = r.URL.Query().Get("units")
		_, _ = w.Write([]byte(`{"observations":[
			{"date":"2026-08-01","value":"3.35302"},
			{"date":"2026-07-01","value":"3.30386"},
			{"date":"2026-06-01","value":"3.46353"},
			{"date":"2026-05-01","value":"3.5"},
			{"date":"2026-04-01","value":"3.6"},
			{"date":"2026-03-01","value":"3.7"},
			{"date":"2026-02-01","value":"3.8"}]}`))
	}))
	defer srv.Close()
	c := &FRED{APIKey: "test-key", HTTP: srv.Client(), URL: srv.URL}

	readings, errs := c.Fetch(context.Background(), []Indicator{{ID: "CPIAUCSL", Unit: "%", Units: "pc1", Monthly: true}})
	if len(errs) != 0 || len(readings) != 1 {
		t.Fatalf("readings=%v errs=%v", readings, errs)
	}
	if units != "pc1" {
		t.Errorf("units = %q, want pc1, the change from a year earlier", units)
	}
	r := readings[0]
	if r.Latest != 3.35302 || !r.HasPrevious || r.Previous != 3.30386 {
		t.Errorf("Latest=%v Previous=%v (%v), want August against July", r.Latest, r.Previous, r.HasPrevious)
	}
	if r.HasWeekAgo {
		t.Error("a monthly series reported a week-ago reading, which would be February's")
	}
}

// Losing the 2-year is not a reason to lose the brief.
func TestOneFailingSeriesDoesNotSinkTheRest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := &FRED{APIKey: "k", HTTP: srv.Client(), URL: srv.URL}

	readings, errs := c.Fetch(context.Background(), []Indicator{{ID: "BAD"}, {ID: "DGS10"}})
	if len(errs) != 1 {
		t.Errorf("errs = %v, want 1", errs)
	}
	if len(readings) != 1 {
		t.Errorf("got %d readings, want the healthy series kept", len(readings))
	}
}

// No key is a supported state, not a failure: the levels sharpen the macro
// section, they do not carry it.
func TestNoKeyDisablesTheClientEntirely(t *testing.T) {
	c := &FRED{}
	if c.Enabled() {
		t.Error("Enabled with no key")
	}
	readings, errs := c.Fetch(context.Background(), Indicators)
	if len(readings) != 0 || len(errs) != 0 {
		t.Errorf("readings=%v errs=%v, want both empty", readings, errs)
	}
}

func TestObservationsWithNoUsableValuesAreAnError(t *testing.T) {
	c := newStub(t, `{"observations":[{"date":"2026-09-09","value":"."}]}`)
	_, errs := c.Fetch(context.Background(), []Indicator{{ID: "DGS10"}})
	if len(errs) != 1 {
		t.Errorf("errs = %v, want one", errs)
	}
}
