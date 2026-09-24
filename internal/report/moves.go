package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// A section opens with the watchlist's biggest share moves on the day, in one
// line: what the exchange did, before what the articles said about it.
const (
	// MinMoveShown is the smallest move the line shows. Below one percent a
	// share did what shares do on an ordinary day, and a line of them would
	// read as news.
	MinMoveShown = 1.0

	// MaxMovesShown keeps the line to one line on a phone.
	MaxMovesShown = 4
)

// sectionMoves picks a watchlist's biggest moves, largest first.
//
// A quote older than since is left out: it is a session an earlier brief
// reported, as on the morning after a US holiday, and showing it again would
// present old moves as the day's.
func sectionMoves(g model.Group, quotes []model.Quote, since time.Time) []model.Quote {
	bySymbol := make(map[string]model.Quote, len(quotes))
	for _, q := range quotes {
		bySymbol[q.Symbol] = q
	}

	var out []model.Quote
	for _, t := range g.Symbols() {
		q, ok := bySymbol[strings.ToUpper(t)]
		if !ok || math.Abs(q.Percent) < MinMoveShown {
			continue
		}
		if !since.IsZero() && !q.AsOf.IsZero() && !q.AsOf.After(since) {
			continue
		}
		out = append(out, q)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return math.Abs(out[i].Percent) > math.Abs(out[j].Percent)
	})
	if len(out) > MaxMovesShown {
		out = out[:MaxMovesShown]
	}
	return out
}

// trend describes where a share stands against its own history: its averages,
// its range over the year, and whether the day was busy. It is given for the
// shares that moved furthest beyond the market, where the question is what
// kind of move it was.
//
// Every figure is description, not forecast, and the prompt says so.
func trend(q model.Quote, t model.Trading) string {
	var parts []string
	against := func(avg float64) string {
		if avg <= 0 || q.Price <= 0 {
			return ""
		}
		gap := (q.Price - avg) / avg * 100
		switch {
		case gap >= 0.05:
			return fmt.Sprintf(" (the price is %.1f%% above it)", gap)
		case gap <= -0.05:
			return fmt.Sprintf(" (the price is %.1f%% below it)", -gap)
		default:
			return " (the price is at it)"
		}
	}
	if t.MA50 > 0 {
		parts = append(parts, fmt.Sprintf("50-day average %.2f%s", t.MA50, against(t.MA50)))
	}
	if t.MA200 > 0 {
		parts = append(parts, fmt.Sprintf("200-day average %.2f%s", t.MA200, against(t.MA200)))
	}
	if t.High52 > 0 && t.Low52 > 0 {
		parts = append(parts, fmt.Sprintf("52-week high %.2f on %s, low %.2f on %s",
			t.High52, t.HighAt.Format("2 Jan 2006"), t.Low52, t.LowAt.Format("2 Jan 2006")))
	}
	// A session still running has traded only part of a day's volume, which
	// set against whole days always looks quiet.
	if !t.Partial && t.VolumeLast > 0 && t.VolumeAvg30 > 0 {
		parts = append(parts, fmt.Sprintf("volume %.1f times its 30-day average", t.VolumeLast/t.VolumeAvg30))
	}
	return strings.Join(parts, "; ")
}
