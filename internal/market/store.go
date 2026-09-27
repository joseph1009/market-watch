// Package market keeps two years of the whole US market's daily bars on the
// data volume, and reads them for the recommendations: which shares have
// risen steadily, which industries have risen together or are starting to,
// and which share moved several times its usual on the day.
//
// The bars come from Massive's grouped daily endpoint, a session a request
// (internal/prices). Its free plan allows five requests a minute and reaches
// two years back, so the first fill takes about two hours and every day after
// it takes one request. What is kept is every common share that traded at
// least a dollar a share and a million dollars in the day: about six thousand
// listings, eighty kilobytes a day compressed, forty megabytes for the two
// years.
package market

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Bar is one listing's session as the store keeps it.
type Bar struct {
	Open, Close, Volume float64
}

// Store is the directory the sessions are kept in: one file a session, named
// by its date, and a marker for each weekday that had none.
type Store struct {
	Dir string

	// syncing lets one Sync run at a time: the background fill and the
	// closer look's top-up would otherwise fetch the same days twice.
	syncing sync.Mutex
}

const (
	dayLayout  = "2006-01-02"
	sessionExt = ".json.gz"
	closedExt  = ".closed"
	splitsFile = "splits.json"
)

// dayFile is one session on disk. Fetched is when it was read, which decides
// whether a later split has already been applied to it: the source adjusts
// for every split up to the day it answers.
type dayFile struct {
	Date    string                `json:"date"`
	Fetched time.Time             `json:"fetched"`
	Bars    map[string][3]float64 `json:"bars"`
}

func (s *Store) path(day time.Time, ext string) string {
	return filepath.Join(s.Dir, day.Format(dayLayout)+ext)
}

// Known reports whether a day is in the store, as a session or as a weekday
// the market was shut.
func (s *Store) Known(day time.Time) bool {
	for _, ext := range []string{sessionExt, closedExt} {
		if _, err := os.Stat(s.path(day, ext)); err == nil {
			return true
		}
	}
	return false
}

// Save writes a session.
func (s *Store) Save(day, fetched time.Time, bars map[string]Bar) error {
	f := dayFile{Date: day.Format(dayLayout), Fetched: fetched.UTC(), Bars: make(map[string][3]float64, len(bars))}
	for sym, b := range bars {
		f.Bars[sym] = [3]float64{round(b.Open), round(b.Close), float64(int64(b.Volume))}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return s.write(s.path(day, sessionExt), func(w *os.File) error {
		z := gzip.NewWriter(w)
		if _, err := z.Write(data); err != nil {
			return err
		}
		return z.Close()
	})
}

// round keeps a price to the hundredth of a cent, which is finer than any
// share on the list trades in and keeps the files small.
func round(v float64) float64 { return float64(int64(v*10000+0.5)) / 10000 }

// SaveClosed marks a weekday the market was shut, so it is not asked for
// again.
func (s *Store) SaveClosed(day time.Time) error {
	return s.write(s.path(day, closedExt), func(*os.File) error { return nil })
}

func (s *Store) write(path string, fill func(*os.File) error) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := fill(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Days lists the sessions held, oldest first.
func (s *Store) Days() ([]time.Time, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var days []time.Time
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), sessionExt)
		if !ok {
			continue
		}
		if day, err := time.Parse(dayLayout, name); err == nil {
			days = append(days, day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	return days, nil
}

func (s *Store) read(day time.Time) (dayFile, error) {
	f, err := os.Open(s.path(day, sessionExt))
	if err != nil {
		return dayFile{}, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return dayFile{}, fmt.Errorf("%s: %w", day.Format(dayLayout), err)
	}
	defer z.Close()
	var out dayFile
	if err := json.NewDecoder(z).Decode(&out); err != nil {
		return dayFile{}, fmt.Errorf("%s: %w", day.Format(dayLayout), err)
	}
	return out, nil
}

// Split is a change in a share's count that took effect on Date: From old
// shares became To new ones.
type Split struct {
	Symbol   string    `json:"symbol"`
	Date     time.Time `json:"date"`
	From, To float64
}

type splitRecord struct {
	// Checked is the last day the splits were read up to. Zero before the
	// first fill, whose sessions all arrive already adjusted.
	Checked time.Time `json:"checked"`
	Splits  []Split   `json:"splits"`
}

func (s *Store) splits() (splitRecord, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, splitsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return splitRecord{}, nil
	}
	if err != nil {
		return splitRecord{}, err
	}
	var r splitRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return splitRecord{}, fmt.Errorf("%s: %w", splitsFile, err)
	}
	return r, nil
}

func (s *Store) saveSplits(r splitRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return s.write(filepath.Join(s.Dir, splitsFile), func(w *os.File) error {
		_, err := w.Write(data)
		return err
	})
}

// Load reads the sessions from the day from on, for the symbols keep accepts
// (all, where keep is nil), with every split applied that the source had not
// applied when it served the session. A session that cannot be read is
// skipped rather than failing the whole: a day missing from two years moves
// no average.
func (s *Store) Load(from time.Time, keep func(string) bool) (*Panel, error) {
	days, err := s.Days()
	if err != nil {
		return nil, err
	}
	record, err := s.splits()
	if err != nil {
		return nil, err
	}
	bySymbol := map[string][]Split{}
	for _, sp := range record.Splits {
		bySymbol[sp.Symbol] = append(bySymbol[sp.Symbol], sp)
	}

	p := &Panel{series: map[string]*Series{}}
	for _, day := range days {
		if day.Before(from) {
			continue
		}
		f, err := s.read(day)
		if err != nil {
			continue
		}
		i := len(p.Dates)
		p.Dates = append(p.Dates, day)
		for _, ser := range p.series {
			ser.grow()
		}
		for sym, bar := range f.Bars {
			if keep != nil && !keep(sym) {
				continue
			}
			ser := p.series[sym]
			if ser == nil {
				ser = &Series{Symbol: sym}
				for range p.Dates {
					ser.grow()
				}
				p.series[sym] = ser
			}
			open, close, volume := bar[0], bar[1], bar[2]
			for _, sp := range bySymbol[sym] {
				// A split the day's bar predates, which had not happened
				// when the source served it.
				if day.Before(sp.Date) && f.Fetched.Before(sp.Date) {
					open *= sp.From / sp.To
					close *= sp.From / sp.To
					volume *= sp.To / sp.From
				}
			}
			ser.Open[i], ser.Close[i], ser.Volume[i] = open, close, volume
		}
	}
	return p, nil
}
