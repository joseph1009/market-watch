package market

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/joseph1009/market-watch/internal/prices"
)

// Source is where the sessions come from: Massive's grouped daily bars.
type Source interface {
	Session(ctx context.Context, date time.Time) (map[string]prices.SessionBar, error)
	Splits(ctx context.Context, since, until time.Time) ([]prices.Split, error)
}

const (
	// Reach is how far back the store is filled: the free plan's two years,
	// less a few days so the oldest request never falls just outside them.
	Reach = 2*365*24*time.Hour - 5*24*time.Hour

	// servedWithin is how recent a day can be and still be not served yet
	// rather than a holiday. The plan serves a session some hours after it
	// closes; four days covers a long weekend on top.
	servedWithin = 4 * 24 * time.Hour

	// keepMinPrice and keepMinValue are what a listing must trade at, and
	// how much of it must change hands, to be kept for a day: below them a
	// share is too cheap or too thin for anything the store is read for,
	// and they are most of the ten thousand listings a day.
	keepMinPrice = 1
	keepMinValue = 1e6
)

// Sync fetches the sessions the store lacks, newest first, from today in New
// York back to Reach, at most limit of them (all, where limit is zero). New
// sessions are fetched before old gaps, so a short sync keeps the store
// current while a long one fills its history. It reads the splits since the
// last sync once the sessions are done. It returns how many days it asked
// for.
func (s *Store) Sync(ctx context.Context, src Source, now time.Time, limit int) (int, error) {
	s.syncing.Lock()
	defer s.syncing.Unlock()

	ny := now.In(newYork())
	today := time.Date(ny.Year(), ny.Month(), ny.Day(), 0, 0, 0, 0, time.UTC)
	oldest := today.Add(-Reach)

	asked := 0
	for day := today; !day.Before(oldest); day = day.AddDate(0, 0, -1) {
		if limit > 0 && asked == limit {
			break
		}
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday || s.Known(day) {
			continue
		}
		if day.Equal(today) && ny.Hour() < 18 {
			continue // today's session has not closed
		}
		bars, err := session(ctx, src, day)
		asked++
		if err != nil {
			return asked, err
		}
		if len(bars) == 0 {
			// A recent day may only be not served yet. An older one was a
			// holiday, and is marked so it is not asked for again.
			if today.Sub(day) > servedWithin {
				if err := s.SaveClosed(day); err != nil {
					return asked, err
				}
			}
			continue
		}
		kept := make(map[string]Bar, len(bars)/2)
		for sym, b := range bars {
			if prices.CommonShare(sym) && b.Close >= keepMinPrice && b.Close*b.Volume >= keepMinValue {
				kept[sym] = Bar{Open: b.Open, Close: b.Close, Volume: b.Volume}
			}
		}
		if err := s.Save(day, now, kept); err != nil {
			return asked, err
		}
	}
	return asked, s.syncSplits(ctx, src, today)
}

// attempts is how many times a day is asked for before the sync gives up:
// over a two-hour fill, a connection dropped now and then is ordinary.
const attempts = 3

// session asks for a day, again after a failure that another try could
// mend. A refused key, or the context ending, is not one.
func session(ctx context.Context, src Source, day time.Time) (map[string]prices.SessionBar, error) {
	var err error
	for try := 0; try < attempts; try++ {
		var bars map[string]prices.SessionBar
		if bars, err = src.Session(ctx, day); err == nil {
			return bars, nil
		}
		if errors.Is(err, prices.ErrMassiveKey) || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, err
}

// syncSplits reads the splits since the last check. Before the first, every
// session in the store was served after its splits and needs none.
func (s *Store) syncSplits(ctx context.Context, src Source, today time.Time) error {
	record, err := s.splits()
	if err != nil {
		return err
	}
	if record.Checked.IsZero() {
		record.Checked = today
		return s.saveSplits(record)
	}
	if !record.Checked.Before(today) {
		return nil
	}
	found, err := src.Splits(ctx, record.Checked, today)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, sp := range record.Splits {
		seen[sp.Symbol+sp.Date.Format(dayLayout)] = true
	}
	for _, sp := range found {
		key := sp.Symbol + sp.Date.Format(dayLayout)
		if seen[key] || sp.Date.After(today) {
			continue
		}
		seen[key] = true
		record.Splits = append(record.Splits, Split{Symbol: sp.Symbol, Date: sp.Date, From: sp.From, To: sp.To})
	}
	sort.Slice(record.Splits, func(i, j int) bool { return record.Splits[i].Date.Before(record.Splits[j].Date) })
	record.Checked = today
	return s.saveSplits(record)
}

// Missing counts the weekdays within Reach the store does not yet hold,
// which is how far the first fill has to go.
func (s *Store) Missing(now time.Time) int {
	ny := now.In(newYork())
	today := time.Date(ny.Year(), ny.Month(), ny.Day(), 0, 0, 0, 0, time.UTC)
	n := 0
	for day := today.AddDate(0, 0, -1); !day.Before(today.Add(-Reach)); day = day.AddDate(0, 0, -1) {
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday && !s.Known(day) {
			n++
		}
	}
	return n
}

// newYork is where a session's date is decided: at the brief's hour in
// Singapore, UTC and New York agree on the day, but a /now in the New York
// evening is already the next day in UTC.
func newYork() *time.Location {
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		return loc
	}
	return time.FixedZone("EST", -5*60*60)
}
