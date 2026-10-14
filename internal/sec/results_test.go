package sec

import (
	"encoding/json"
	"testing"
	"time"
)

// Results are the 8-Ks under Item 2.02, newest first, once a day, back to the
// time asked for; other 8-Ks and other forms are not results.
func TestResultsDatesAreTheItem202Filings(t *testing.T) {
	var doc submissions
	if err := json.Unmarshal([]byte(`{"name":"X","filings":{"recent":{
		"form":["8-K","8-K","10-Q","8-K","8-K","8-K"],
		"filingDate":["2026-07-30","2026-07-30","2026-07-31","2026-06-02","2026-04-29","2025-01-30"],
		"items":["2.02,9.01","2.02","","5.02","2.02,9.01","2.02"],
		"accessionNumber":["a","b","c","d","e","f"]}}}`), &doc); err != nil {
		t.Fatal(err)
	}
	got := resultsDates(doc, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 2 || got[0].Format(time.DateOnly) != "2026-07-30" || got[1].Format(time.DateOnly) != "2026-04-29" {
		t.Errorf("results dates = %v, want 30 Jul and 29 Apr 2026", got)
	}
}
