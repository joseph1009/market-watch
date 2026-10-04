package fundamentals

import (
	"math"
	"slices"
	"sort"
	"time"
)

// Stock splits. A filing restates the periods it shows for any split before
// it, but a period no later filing shows keeps the figures it was filed with.
// NVIDIA's year to January 2022 read US$3.85 a share beside US$0.17 a year
// later (4 October 2026): the later year was restated for the ten-for-one
// split of June 2024 and the earlier one was not. So a figure a share, and a
// count of shares, filed before a split is put on the split's basis here.
//
// The split is the one the company tags, StockholdersEquityNoteStockSplitConversionRatio1,
// which most US filers do. The tag's dates are untidy: Tesla tags a split of
// three with the end of 2020, though it split in 2022. So a split is taken
// only where the filings bear it out: the share count filed just after its
// date is about the ratio times the one filed just before.

// splitTag is the tag a company records its stock splits under.
const splitTag = "StockholdersEquityNoteStockSplitConversionRatio1"

// split is a stock split: each share became Ratio shares, and figures filed
// before On are on the old basis.
type split struct {
	On    time.Time
	Ratio float64
}

// splits reads the splits the filings bear out from the tagged ones, given
// the share counts as kept (the latest filing of each period).
func splits(tagged []Observation, shares []Observation) []split {
	// The candidate days for each ratio, earliest first.
	byRatio := map[float64][]time.Time{}
	for _, o := range tagged {
		if o.Value >= 1.5 && o.Value <= 100 {
			byRatio[o.Value] = append(byRatio[o.Value], o.End, o.Filed)
		}
	}
	var out []split
	for ratio, days := range byRatio {
		slices.SortFunc(days, func(a, b time.Time) int { return a.Compare(b) })
		var last time.Time
		for _, day := range days {
			// One split is tagged in many filings, so a ratio can recur; a
			// second split of the same ratio is a year or more after the
			// first.
			if !last.IsZero() && day.Sub(last) < 365*24*time.Hour {
				continue
			}
			if bornOut(shares, day, ratio) {
				out = append(out, split{On: day, Ratio: ratio})
				last = day
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].On.Before(out[j].On) })
	return out
}

// bornOut is whether the share count jumps by about ratio across day: the
// count of the latest period filed before it, against the earliest period
// filed on or after it.
func bornOut(shares []Observation, day time.Time, ratio float64) bool {
	var before, after *Observation
	for i, o := range shares {
		if o.Value <= 0 {
			continue
		}
		if o.Filed.Before(day) {
			if before == nil || o.End.After(before.End) {
				before = &shares[i]
			}
		} else if after == nil || o.End.Before(after.End) {
			after = &shares[i]
		}
	}
	if before == nil || after == nil {
		return false
	}
	jump := after.Value / before.Value
	return math.Abs(jump/ratio-1) < 0.2
}

// adjustForSplits puts each figure a share, and each count of shares, filed
// before a split on the split's basis.
func adjustForSplits(byKey map[string][]Observation, list []split) {
	if len(list) == 0 {
		return
	}
	for key, perShare := range map[string]bool{"epsDiluted": true, "dilutedShares": false, "sharesOutstanding": false} {
		obs := byKey[key]
		for i := range obs {
			for _, s := range list {
				if !obs[i].Filed.Before(s.On) {
					continue
				}
				if perShare {
					obs[i].Value /= s.Ratio
				} else {
					obs[i].Value *= s.Ratio
				}
			}
		}
	}
}
