package discover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenFIGI is Bloomberg's open symbology service. It maps a ticker and an
// exchange to the company that actually holds it, on every major market, and
// needs no key for the volumes here.
//
// It is used as a check rather than a lookup: the model proposes a ticker, and
// this says whether a company of that name really trades under it. An invented
// symbol comes back either empty or as somebody else, and is dropped. Without
// that check, a confident wrong ticker would be indistinguishable from a right
// one -- and the reader might act on it.
const (
	openFIGIURL = "https://api.openfigi.com/v3/mapping"

	// Unauthenticated callers get a modest allowance, so requests are batched
	// and paced. A day's candidates are a handful, well inside it.
	figiJobsPerRequest = 10
	figiPause          = 300 * time.Millisecond
)

// Exchanges are the markets a candidate may be listed on, as OpenFIGI codes.
// Beyond these the reader is unlikely to be able to trade, and the further the
// list stretches the more room there is for a symbol to mean two things.
var Exchanges = map[string]string{
	"US": "United States",
	"HK": "Hong Kong",
	"JP": "Tokyo",
	"LN": "London",
	"NA": "Euronext Amsterdam",
	"FP": "Euronext Paris",
	"GR": "Frankfurt",
	"SP": "Singapore",
	"AU": "Australia",
	"KS": "Korea",
	"TT": "Taiwan",
	"IN": "India",
	"CH": "China, Shanghai or Shenzhen", // OpenFIGI's CN is Canada
}

// Verifier confirms that a ticker belongs to the company it is claimed for.
type Verifier interface {
	Verify(ctx context.Context, queries []Query) ([]string, error)
}

// Query is one ticker to check.
type Query struct {
	Ticker   string
	Exchange string
}

// FIGI verifies against OpenFIGI.
type FIGI struct {
	HTTP *http.Client
	URL  string

	// APIKey raises the rate allowance. Optional, and unset here.
	APIKey string
}

// Verify returns the registered company name for each query, in order, with an
// empty string where nothing trades under that symbol.
func (f *FIGI) Verify(ctx context.Context, queries []Query) ([]string, error) {
	names := make([]string, len(queries))

	for start := 0; start < len(queries); start += figiJobsPerRequest {
		end := min(start+figiJobsPerRequest, len(queries))
		batch := queries[start:end]

		if start > 0 {
			select {
			case <-ctx.Done():
				return names, ctx.Err()
			case <-time.After(figiPause):
			}
		}

		jobs := make([]map[string]string, 0, len(batch))
		for _, q := range batch {
			jobs = append(jobs, map[string]string{
				"idType":   "TICKER",
				"idValue":  q.Ticker,
				"exchCode": q.Exchange,
			})
		}
		body, err := json.Marshal(jobs)
		if err != nil {
			return names, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url(), bytes.NewReader(body))
		if err != nil {
			return names, err
		}
		req.Header.Set("Content-Type", "application/json")
		if f.APIKey != "" {
			req.Header.Set("X-OPENFIGI-APIKEY", f.APIKey)
		}

		resp, err := f.client().Do(req)
		if err != nil {
			return names, err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			return names, readErr
		}
		if resp.StatusCode != http.StatusOK {
			return names, fmt.Errorf("openfigi: %s", resp.Status)
		}

		// Each job answers with either matches or a warning, in the order asked.
		var results []struct {
			Data []struct {
				Name     string `json:"name"`
				Ticker   string `json:"ticker"`
				ExchCode string `json:"exchCode"`
			} `json:"data"`
			Warning string `json:"warning"`
		}
		if err := json.Unmarshal(data, &results); err != nil {
			return names, fmt.Errorf("openfigi: %w", err)
		}
		for i, r := range results {
			if i >= len(batch) || len(r.Data) == 0 {
				continue
			}
			names[start+i] = r.Data[0].Name
		}
	}
	return names, nil
}

func (f *FIGI) url() string {
	if f.URL != "" {
		return f.URL
	}
	return openFIGIURL
}

func (f *FIGI) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return http.DefaultClient
}

// SameCompany reports whether a registered name and the name the news used
// describe the same company.
//
// They never match exactly: the news writes "Tencent", the exchange registers
// "TENCENT HOLDINGS LTD". So the test is whether the distinctive words survive
// in both once the legal furniture is stripped off. A wrong ticker usually
// returns a completely different company, which fails this easily.
func SameCompany(registered, claimed string) bool {
	a, b := significantWords(registered), significantWords(claimed)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, w := range b {
		for _, other := range a {
			if w == other || strings.HasPrefix(other, w) || strings.HasPrefix(w, other) {
				return true
			}
		}
	}
	return false
}

// corporateForms are the words that say a company is a company, and so say
// nothing about which one.
var corporateForms = map[string]bool{
	"inc": true, "inc.": true, "incorporated": true, "corp": true, "corp.": true,
	"corporation": true, "ltd": true, "ltd.": true, "limited": true, "plc": true,
	"llc": true, "lp": true, "co": true, "co.": true, "company": true,
	"holdings": true, "holding": true, "group": true, "sa": true, "nv": true,
	"ag": true, "se": true, "ab": true, "as": true, "oyj": true, "spa": true,
	"the": true, "and": true, "&": true, "class": true, "adr": true,
}

func significantWords(name string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(name)) {
		w = strings.Trim(w, ".,()-")
		if w == "" || corporateForms[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}
