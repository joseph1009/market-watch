// Massive, formerly Polygon.io: the whole US market's day in one request.
//
// The quote feed and the chart source answer for the companies asked about,
// which are the ones already followed. What neither can say is what moved
// among the thousands nobody asked about -- and those are where the closer
// look's new names have to come from. The grouped daily bars answer that: one
// request returns every US listing's open, close and volume for a session, so
// two requests compare the last two sessions across the market.
//
// The free plan allows five requests a minute and serves each session some
// hours after it has closed: in the New York evening the day's session is not
// served yet. That is enough: this runs the next morning, before the open, and
// needs two sessions, or a few more attempts across a weekend or a holiday.
package prices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMassiveURL = "https://api.massive.com"

	// massiveGap spaces requests to stay inside five a minute.
	massiveGap = 12500 * time.Millisecond

	// massiveBody bounds a grouped reply: about 12,000 listings at around
	// 150 bytes each.
	massiveBody = 16 << 20

	// sessionsBack is how far back to look for the last two sessions. A
	// Friday holiday before a weekend puts the second one five days back.
	sessionsBack = 7
)

// ErrMassiveKey means the key was refused, which no retry will change.
var ErrMassiveKey = errors.New("massive: the API key was refused")

// Massive reads the grouped daily bars. An empty APIKey disables it.
type Massive struct {
	APIKey string
	HTTP   *http.Client
	URL    string

	// Gap overrides massiveGap, for tests.
	Gap time.Duration

	mu   sync.Mutex
	last time.Time
}

// Enabled reports whether there is a key to use.
func (m *Massive) Enabled() bool { return m != nil && m.APIKey != "" }

// SessionBar is one listing's session.
type SessionBar struct {
	Symbol                 string
	Open, High, Low, Close float64
	Volume, VWAP           float64
	Date                   time.Time
}

// DollarVolume is what changed hands in the session, which says whether a
// move was made by the market or by a handful of trades.
func (b SessionBar) DollarVolume() float64 { return b.Close * b.Volume }

// Session reads every US listing's bar for one date. An empty map with no
// error means the date was not a session, or is not served yet.
func (m *Massive) Session(ctx context.Context, date time.Time) (map[string]SessionBar, error) {
	if !m.Enabled() {
		return nil, fmt.Errorf("massive: no key")
	}
	if err := m.wait(ctx); err != nil {
		return nil, err
	}

	day := date.Format(time.DateOnly)
	url := fmt.Sprintf("%s/v2/aggs/grouped/locale/us/market/stocks/%s?adjusted=true&apiKey=%s",
		m.baseURL(), day, m.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client().Do(req)
	if err != nil {
		// The URL carries the key, and the error quotes the URL.
		return nil, errors.New(strings.ReplaceAll(err.Error(), m.APIKey, "KEY"))
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrMassiveKey
	case http.StatusForbidden:
		// "Attempted to request today's data before end of day": not served
		// yet on this plan, which for this purpose is not a session.
		return map[string]SessionBar{}, nil
	default:
		return nil, fmt.Errorf("massive %s: %s", day, resp.Status)
	}

	var doc struct {
		Results []struct {
			T string `json:"T"`
			// The session's start in milliseconds. Unread, but it must be
			// named: Go matches JSON keys to fields ignoring case, and without
			// a field of its own the number would land on T.
			Start int64   `json:"t"`
			O     float64 `json:"o"`
			H     float64 `json:"h"`
			L     float64 `json:"l"`
			C     float64 `json:"c"`
			V     float64 `json:"v"`
			VW    float64 `json:"vw"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, massiveBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("massive %s: %w", day, err)
	}
	out := make(map[string]SessionBar, len(doc.Results))
	for _, r := range doc.Results {
		out[r.T] = SessionBar{Symbol: r.T, Open: r.O, High: r.H, Low: r.L, Close: r.C, Volume: r.V, VWAP: r.VW, Date: date}
	}
	return out, nil
}

// MarketMove is one listing's move between the last two sessions.
type MarketMove struct {
	Symbol          string
	Close, Previous float64
	Percent         float64
	DollarVolume    float64
	Date            time.Time
}

// MoverRules say which moves count.
type MoverRules struct {
	// MinPrice and MinDollarVolume keep out the shares whose moves mean
	// little: a penny stock doubles on nothing, and a listing that traded a
	// few hundred thousand dollars was moved by a few people.
	MinPrice, MinDollarVolume float64

	// Keep, where set, is asked of each symbol: the caller's test of whether
	// it is an operating company worth naming, rather than a fund or a
	// warrant.
	Keep func(symbol string) bool

	Limit int
}

// Movers compares the last two sessions served before now and returns the
// largest moves, either way, that pass the rules.
func (m *Massive) Movers(ctx context.Context, now time.Time, rules MoverRules) ([]MarketMove, error) {
	var sessions []map[string]SessionBar
	ny := now.In(newYork())
	day := time.Date(ny.Year(), ny.Month(), ny.Day(), 0, 0, 0, 0, time.UTC)
	for back := 0; back <= sessionsBack && len(sessions) < 2; back++ {
		date := day.AddDate(0, 0, -back)
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue // no request spent on a day with no session
		}
		bars, err := m.Session(ctx, date)
		if err != nil {
			return nil, err
		}
		if len(bars) > 0 {
			sessions = append(sessions, bars)
		}
	}
	if len(sessions) < 2 {
		return nil, fmt.Errorf("massive: found %d of the two sessions needed in the last %d days", len(sessions), sessionsBack)
	}
	return movers(sessions[0], sessions[1], rules), nil
}

// ordinary is the shape of a common share's symbol: letters, and a class
// after a dot. Preferred shares carry lower-case letters that do not fit it.
var ordinary = regexp.MustCompile(`^[A-Z]{1,5}(\.[A-Z])?$`)

// common reports whether a symbol is a common share. On Nasdaq a fifth letter
// of W, U or R marks a warrant, a unit or a right, whose price is a leveraged
// or partial claim on the share and moves accordingly.
func common(sym string) bool {
	if !ordinary.MatchString(sym) {
		return false
	}
	return len(sym) != 5 || !strings.ContainsAny(sym[4:], "WUR")
}

func movers(latest, previous map[string]SessionBar, rules MoverRules) []MarketMove {
	var out []MarketMove
	for sym, bar := range latest {
		prev, ok := previous[sym]
		if !ok || prev.Close <= 0 || bar.Close <= 0 || !common(sym) {
			continue
		}
		if bar.Close < rules.MinPrice || bar.DollarVolume() < rules.MinDollarVolume {
			continue
		}
		if rules.Keep != nil && !rules.Keep(sym) {
			continue
		}
		out = append(out, MarketMove{
			Symbol: sym, Close: bar.Close, Previous: prev.Close,
			Percent:      (bar.Close/prev.Close - 1) * 100,
			DollarVolume: bar.DollarVolume(),
			Date:         bar.Date,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := math.Abs(out[i].Percent), math.Abs(out[j].Percent); a != b {
			return a > b
		}
		return out[i].Symbol < out[j].Symbol
	})
	if rules.Limit > 0 && len(out) > rules.Limit {
		out = out[:rules.Limit]
	}
	return out
}

// wait holds a request back until it is the plan's gap after the last one.
func (m *Massive) wait(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	gap := m.Gap
	if gap == 0 {
		gap = massiveGap
	}
	if !m.last.IsZero() {
		if d := time.Until(m.last.Add(gap)); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
	}
	m.last = time.Now()
	return nil
}

func (m *Massive) baseURL() string {
	if m.URL != "" {
		return strings.TrimRight(m.URL, "/")
	}
	return defaultMassiveURL
}

func (m *Massive) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// newYork is where a session's date is decided. The scheduled brief runs in the
// New York morning, when UTC agrees on the day, but a /now in the New York
// evening is already the next day in UTC.
func newYork() *time.Location {
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		return loc
	}
	return time.FixedZone("EST", -5*60*60)
}
