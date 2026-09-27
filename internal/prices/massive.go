// Massive, formerly Polygon.io: the whole US market's day in one request.
//
// The quote feed and the chart source answer for the companies asked about.
// What neither can say is how the thousands nobody asked about have done --
// which industries have risen together over a year, which share moved four
// times its usual on the news -- and those are where the recommendations
// start. The grouped daily bars answer that: one request returns every US
// listing's open, close and volume for a session, and two years of them,
// kept on the data volume, are the market's history (internal/market).
//
// The free plan allows five requests a minute, reaches two years back, and
// serves each session some hours after it has closed: in the New York
// evening the day's session is not served yet.
package prices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

// Split is a change in a share's count: split_from old shares became
// split_to new ones on Date. A two-for-one is 1 to 2, a one-for-fifty reverse
// split 50 to 1.
type Split struct {
	Symbol   string
	Date     time.Time
	From, To float64
}

// Splits reads the splits that took effect from since to until, both days
// included.
func (m *Massive) Splits(ctx context.Context, since, until time.Time) ([]Split, error) {
	if !m.Enabled() {
		return nil, fmt.Errorf("massive: no key")
	}
	next := fmt.Sprintf("%s/v3/reference/splits?execution_date.gte=%s&execution_date.lte=%s&limit=1000",
		m.baseURL(), since.Format(time.DateOnly), until.Format(time.DateOnly))
	var out []Split
	// A page holds a thousand; a busy month has a few hundred, so the second
	// page is there for safety rather than use.
	for page := 0; next != "" && page < 5; page++ {
		if err := m.wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next+"&apiKey="+m.APIKey, nil)
		if err != nil {
			return nil, err
		}
		resp, err := m.client().Do(req)
		if err != nil {
			return nil, errors.New(strings.ReplaceAll(err.Error(), m.APIKey, "KEY"))
		}
		var doc struct {
			Results []struct {
				Ticker string  `json:"ticker"`
				Date   string  `json:"execution_date"`
				From   float64 `json:"split_from"`
				To     float64 `json:"split_to"`
			} `json:"results"`
			Next string `json:"next_url"`
		}
		switch resp.StatusCode {
		case http.StatusOK:
			err = json.NewDecoder(io.LimitReader(resp.Body, massiveBody)).Decode(&doc)
		case http.StatusUnauthorized:
			err = ErrMassiveKey
		default:
			err = fmt.Errorf("massive splits: %s", resp.Status)
		}
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range doc.Results {
			day, err := time.Parse(time.DateOnly, r.Date)
			if err != nil || r.From <= 0 || r.To <= 0 || r.Ticker == "" {
				continue
			}
			out = append(out, Split{Symbol: r.Ticker, Date: day, From: r.From, To: r.To})
		}
		// The next page's address carries no key; it is added on the way out.
		next = doc.Next
	}
	return out, nil
}

// ordinary is the shape of a common share's symbol: letters, and a class
// after a dot. Preferred shares carry lower-case letters that do not fit it.
var ordinary = regexp.MustCompile(`^[A-Z]{1,5}(\.[A-Z])?$`)

// CommonShare reports whether a symbol is a common share's. On Nasdaq a fifth
// letter of W, U or R marks a warrant, a unit or a right, whose price is a
// leveraged or partial claim on the share and moves accordingly.
func CommonShare(sym string) bool {
	if !ordinary.MatchString(sym) {
		return false
	}
	return len(sym) != 5 || !strings.ContainsAny(sym[4:], "WUR")
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
