package fundamentals

import (
	"math"
	"testing"
	"time"
)

// NVIDIA split ten for one in June 2024. Its year to January 2022 was last
// filed in February 2024, before the split, so its figures a share are put on
// the new basis; its year to January 2023 was filed again after the split,
// restated, and is left alone.
func TestFiguresFiledBeforeASplitAreRestated(t *testing.T) {
	tagged := []Observation{
		{End: day("2024-05-31"), Value: 10, Filed: day("2024-08-28")},
		{End: day("2024-06-30"), Value: 10, Filed: day("2025-05-28")},
	}
	shares := []Observation{
		{Start: day("2021-02-01"), End: day("2022-01-30"), Value: 2.535e9, Filed: day("2024-02-21")},
		{Start: day("2023-01-30"), End: day("2023-04-30"), Value: 2.49e9, Filed: day("2024-05-29")},
		{Start: day("2023-05-01"), End: day("2023-07-30"), Value: 24.9e9, Filed: day("2024-08-28")},
		{Start: day("2022-01-31"), End: day("2023-01-29"), Value: 25.07e9, Filed: day("2025-02-26")},
	}
	list := splits(tagged, shares)
	if len(list) != 1 || list[0].Ratio != 10 || !list[0].On.Equal(day("2024-05-31")) {
		t.Fatalf("splits = %+v, want one of ten on 31 May 2024", list)
	}

	byKey := map[string][]Observation{
		"epsDiluted": {
			{End: day("2022-01-30"), Value: 3.85, Filed: day("2024-02-21")},
			{End: day("2023-01-29"), Value: 0.17, Filed: day("2025-02-26")},
		},
		"dilutedShares": shares,
	}
	adjustForSplits(byKey, list)
	if got := byKey["epsDiluted"][0].Value; math.Abs(got-0.385) > 1e-9 {
		t.Errorf("EPS filed before the split = %v, want 0.385", got)
	}
	if got := byKey["epsDiluted"][1].Value; got != 0.17 {
		t.Errorf("EPS filed after the split = %v, want it left at 0.17", got)
	}
	if got := byKey["dilutedShares"][0].Value; math.Abs(got-25.35e9) > 1 {
		t.Errorf("shares filed before the split = %v, want 25.35bn", got)
	}
}

// A tagged split the share counts do not bear out is not taken: Tesla tags a
// split of three at the end of 2020, two years before it split three for one.
func TestASplitTheShareCountsDoNotShowIsIgnored(t *testing.T) {
	tagged := []Observation{{End: day("2020-12-31"), Value: 3, Filed: day("2021-02-08")}}
	shares := []Observation{
		{End: day("2019-09-30"), Value: 1.0e9, Filed: day("2020-10-26")},
		{End: day("2019-12-31"), Value: 1.02e9, Filed: day("2021-02-08")},
	}
	if list := splits(tagged, shares); len(list) != 0 {
		t.Errorf("splits = %+v, want none", list)
	}
}

// Cash and short-term investments are added only where both are of one date.
func TestCashIsNotAddedToInvestmentsOfAnotherDate(t *testing.T) {
	b := Balance{AsOf: day("2026-07-26"),
		Figures: map[string]Value{"cash": known(22.44e9), "marketableSecurities": known(49.12e9), "longTermDebt": known(8.5e9)},
		Older:   map[string]time.Time{"marketableSecurities": day("2025-10-26")}}
	pot, at, with := b.CashPot()
	if pot.Amount != 22.44e9 || with || !at.Equal(day("2026-07-26")) {
		t.Errorf("CashPot = %v at %s with investments %v, want cash alone at 26 Jul 2026", pot.Amount, at, with)
	}
	delete(b.Older, "marketableSecurities")
	if pot, _, with := b.CashPot(); pot.Amount != 71.56e9 || !with {
		t.Errorf("CashPot = %v, %v; want both on one date", pot.Amount, with)
	}
}

// A section heading written in bold is a heading.
func TestBoldHeadingsArePlain(t *testing.T) {
	got := PlainHeadings("**THE BUSINESS**\n### What it sells\n- **Chips.** It sells chips.\n**Not a heading**")
	want := "THE BUSINESS\n### What it sells\n- **Chips.** It sells chips.\n**Not a heading**"
	if got != want {
		t.Errorf("PlainHeadings =\n%s\nwant\n%s", got, want)
	}
}
