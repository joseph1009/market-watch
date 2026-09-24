package triage

import (
	"slices"

	"github.com/joseph1009/market-watch/internal/model"
)

// ThinSection is how many articles a section is filled to from the reserve.
// Crypto had eleven the morning before its section went missing, and a
// section is written from as few as that without feeling padded.
const ThinSection = 10

// TopUp fills the sections with fewer than want articles to write from, using
// the ones the sorting rated 3 and would have put there (Article.Reserve). It
// returns a copy of articles, in the same order, and how many placements it
// made.
//
// Only an article placed nowhere is used, so a top-up never adds a third
// sector to a story or retells one that another section already carries; and
// only one no earlier brief carried, since a repeat is left out of a section
// anyway. Articles arrive ranked, so the strongest of the reserve go first.
//
// A top-up is a proposal, not a placement: the review must confirm each one
// or it is withdrawn (applyReview), so a thin section stays thin when what
// was held back is not worth reading. Without the review, do not call this.
func TopUp(articles []model.Article, groups []model.Group, want int) ([]model.Article, int) {
	out := make([]model.Article, len(articles))
	copy(out, articles)

	added := 0
	for _, g := range groups {
		have := 0
		for _, a := range out {
			if a.InGroup(g.ID) && (a.Rating == 0 || a.Rating >= MinReviewRating) {
				have++
			}
		}
		for i := range out {
			if have >= want {
				break
			}
			a := &out[i]
			if !slices.Contains(a.Reserve, g.ID) || !a.Covered.IsZero() {
				continue
			}
			// Topped up already, for another thin section, counts as placed
			// nowhere by the sorting: it may fill both.
			if (len(a.GroupIDs) > 0 && !a.ToppedUp) || len(a.GroupIDs) >= MaxPlacements {
				continue
			}
			// A fresh slice: GroupIDs is shared with the caller's copy.
			a.GroupIDs = append(slices.Clip(a.GroupIDs), g.ID)
			a.ToppedUp = true
			have++
			added++
		}
	}
	return out, added
}
