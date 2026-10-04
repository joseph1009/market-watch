package fundamentals

import (
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// filedMicron is Micron's figures as filed up to its quarter to 28 May 2026,
// in millions: the quarters of the year to 3 September 2026 but the last, and
// the year before as a whole and its first nine months.
func filedMicron() *Snapshot {
	period := func(start, end string, v float64, form string) Observation {
		return Observation{Start: day(start), End: day(end), Value: v * 1e6, Unit: "USD", Form: form, Filed: day(end).AddDate(0, 1, 0)}
	}
	line := func(q1, q2, q3, nine, lastYear, lastNine float64) []Observation {
		return []Observation{
			period("2025-08-29", "2026-05-28", nine, "10-Q"),
			period("2026-02-27", "2026-05-28", q3, "10-Q"),
			period("2025-11-28", "2026-02-26", q2, "10-Q"),
			period("2025-08-29", "2025-11-27", q1, "10-Q"),
			period("2024-08-30", "2025-08-28", lastYear, "10-K"),
			period("2024-08-30", "2025-05-29", lastNine, "10-Q"),
		}
	}
	instant := func(end string, v float64) Observation {
		return Observation{End: day(end), Value: v * 1e6, Unit: "USD", Form: "10-Q", Filed: day(end).AddDate(0, 1, 0)}
	}
	s := &Snapshot{Ticker: "MU", Currency: "USD", years: 3, obs: map[string][]Observation{
		"revenue":   line(13643, 23860, 41456, 78959, 37378, 26063),
		"netIncome": line(5240, 13785, 28243, 47268, 8539, 5338),
		"assets":    {instant("2026-05-28", 134112), instant("2025-08-28", 82798)},
		"cash":      {instant("2026-05-28", 24995), instant("2025-08-28", 9642)},
		"equity":    {instant("2026-05-28", 100724), instant("2025-08-28", 54165)},
		// Filed under a line that measures it otherwise than the release.
		"longTermDebt": {instant("2026-05-28", 8840), instant("2025-08-28", 11533)},
	}}
	s.build()
	return s
}

// micronRelease is what its release of 30 September 2026 prints.
func micronRelease() ReleaseFigures {
	return ReleaseFigures{Currency: "USD", MoneyScale: "millions", ShareScale: "millions",
		Periods: []ReleasePeriod{
			{Months: 3, End: "2026-09-03", Figures: map[string]float64{"revenue": 54229, "netIncome": 37701, "epsDiluted": 32.87}},
			{Months: 3, End: "2026-05-28", Figures: map[string]float64{"revenue": 41456, "netIncome": 28243}},
			{Months: 3, End: "2025-08-28", Figures: map[string]float64{"revenue": 11315, "netIncome": 3201}},
			{Months: 12, End: "2026-09-03", Figures: map[string]float64{"revenue": 133188, "netIncome": 84969}},
			{Months: 12, End: "2025-08-28", Figures: map[string]float64{"revenue": 37378, "netIncome": 8539}},
		},
		Balances: []ReleaseBalance{
			{Date: "2026-09-03", Figures: map[string]float64{"assets": 195888, "cash": 38364, "equity": 138378, "longTermDebt": 4688}},
			{Date: "2026-05-28", Figures: map[string]float64{"assets": 134112, "cash": 24995, "equity": 100724, "longTermDebt": 5140}},
		},
	}
}

var releaseFiled = day("2026-09-30")

func TestAReleaseAddsTheQuarterTheFilingsLack(t *testing.T) {
	s := filedMicron()
	if s.Quarters[0].End != day("2026-05-28") {
		t.Fatalf("filed quarters start at %s", s.Quarters[0].Label)
	}
	note, err := s.AddRelease(micronRelease(), releaseFiled)
	if err != nil {
		t.Fatalf("not added: %v", err)
	}
	q := s.Quarters[0]
	if q.End != day("2026-09-03") || !q.FromRelease || q.Figure("revenue").Amount != 54229e6 || q.Figure("epsDiluted").Amount != 32.87 {
		t.Errorf("latest quarter: %+v", q)
	}
	if s.Quarters[1].FromRelease {
		t.Errorf("a filed quarter is marked as the release's: %s", s.Quarters[1].Label)
	}
	if s.TTM == nil || !s.TTM.FromRelease || s.TTM.Figure("revenue").Amount != 133188e6 {
		t.Errorf("twelve months: %+v", s.TTM)
	}
	if y := s.Years[0]; y.End != day("2026-09-03") || !y.FromRelease || y.Figure("netIncome").Amount != 84969e6 {
		t.Errorf("latest year: %+v", y)
	}
	if s.YTD != nil {
		t.Errorf("a year so far is left after a full year: %s", s.YTD.Label)
	}
	if b := s.Balance; b.AsOf != day("2026-09-03") || !b.FromRelease || b.Figure("cash").Amount != 38364e6 {
		t.Errorf("balance sheet: %+v", b)
	}
	// The line that does not match keeps its filed figure, dated.
	if b := s.Balance; b.Figure("longTermDebt").Amount != 8840e6 || b.Older["longTermDebt"] != day("2026-05-28") || !strings.Contains(note, "longTermDebt") {
		t.Errorf("long-term debt: %v at %v, note %q", b.Figure("longTermDebt"), b.Older, note)
	}
	if !strings.Contains(s.Table(), "(at 28 May 2026, from an earlier balance sheet)") {
		t.Error("the older line is not dated in the table")
	}
	if !strings.Contains(s.quarterTable(), "3 months to 3 Sep 2026*") {
		t.Errorf("the release's quarter is not marked:\n%s", s.quarterTable())
	}
}

func TestAReleaseThatDoesNotMatchTheFilingsIsNotUsed(t *testing.T) {
	r := micronRelease()
	r.Periods[1].Figures["revenue"] = 40000 // misread
	s := filedMicron()
	if _, err := s.AddRelease(r, releaseFiled); err == nil {
		t.Fatal("a release at odds with the filings was used")
	}
	if s.Quarters[0].End != day("2026-05-28") || s.ReleaseAdded {
		t.Errorf("the quarters changed: %s", s.Quarters[0].Label)
	}

	r = micronRelease()
	r.Periods = r.Periods[:1] // nothing to check it against
	if _, err := filedMicron().AddRelease(r, releaseFiled); err == nil {
		t.Error("a release with no column to check was used")
	}

	r = micronRelease()
	r.Currency = "EUR"
	if _, err := filedMicron().AddRelease(r, releaseFiled); err == nil {
		t.Error("a release in another currency was used")
	}
}

func TestAReleaseBalanceSheetWithoutItsCoreIsLeftOut(t *testing.T) {
	r := micronRelease()
	delete(r.Balances[0].Figures, "cash")
	s := filedMicron()
	note, err := s.AddRelease(r, releaseFiled)
	if err != nil {
		t.Fatal(err)
	}
	if note == "" || s.Balance.AsOf != day("2026-05-28") || s.Balance.FromRelease {
		t.Errorf("a balance sheet without its cash was used: %s, %+v", note, s.Balance)
	}
	if !s.Quarters[0].FromRelease {
		t.Error("the quarter was lost with the balance sheet")
	}
}

func TestReleaseFiguresAreReadFromTheReply(t *testing.T) {
	f, err := ParseReleaseFigures("Here they are:\n```json\n" +
		`{"currency":"USD","moneyScale":"millions","periods":[{"months":3,"end":"2026-09-03","figures":{"revenue":54229}}]}` + "\n```")
	if err != nil || len(f.Periods) != 1 || f.Periods[0].Figures["revenue"] != 54229 {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := ParseReleaseFigures("I could not read the tables."); err == nil {
		t.Error("a reply with no figures was read")
	}
}
