package fundamentals

import (
	"context"
	"regexp"
	"strings"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/prompts"
)

// The analysis ends with the companies to read next to this one, because a
// multiple means nothing alone. "Price to earnings of 21 times" is a fact about
// arithmetic until it sits beside the companies that compete with it, supply it
// or buy from it -- and those are exactly what the reader does not know when
// they are new to a name.
//
// The model proposes them and OpenFIGI checks them, on the same principle as
// the new-names section: a ticker it cannot confirm is dropped rather than
// shown, since a wrong symbol is the one thing here a reader might act on.

// RelatedMarker is the line the model puts before its list, and the line this
// cuts out of the prose before the analysis is rendered.
const RelatedMarker = "COMPANIES TO READ NEXT TO IT"

// Related is one company worth looking at alongside the analysed one.
type Related struct {
	Name     string
	Ticker   string
	Exchange string
	Listed   string
	Why      string
}

// Symbol is how it is written for a reader: "700.HK", "NVDA".
func (r Related) Symbol() string {
	switch {
	case r.Ticker == "":
		return ""
	case r.Exchange == "" || r.Exchange == "US":
		return r.Ticker
	default:
		return r.Ticker + "." + r.Exchange
	}
}

var relatedLine = regexp.MustCompile(`^\s*-?\s*([^|]+)\|([^|]*)\|([^|]*)\|(.+?)\s*$`)

// SplitRelated separates the analysis prose from the list of related companies.
//
// The list is parsed out rather than left in the text because its tickers have
// to be checked before the reader sees them, and because a pipe-delimited table
// reads badly in a chat message.
func SplitRelated(text string) (analysis string, related []Related) {
	at := strings.Index(text, RelatedMarker)
	if at < 0 {
		return text, nil
	}

	analysis = strings.TrimSpace(text[:at])
	for _, raw := range strings.Split(text[at+len(RelatedMarker):], "\n") {
		m := relatedLine.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		r := Related{
			Name:     strings.TrimSpace(m[1]),
			Ticker:   strings.ToUpper(strings.TrimSpace(m[2])),
			Exchange: strings.ToUpper(strings.TrimSpace(m[3])),
			Why:      strings.TrimSpace(m[4]),
		}
		if r.Name == "" || r.Why == "" {
			continue
		}
		if r.Ticker == "?" || r.Ticker == "-" {
			r.Ticker, r.Exchange = "", ""
		}
		related = append(related, r)
	}
	return analysis, related
}

// VerifyRelated checks each proposed ticker against the exchange and drops the
// ones that do not hold up.
//
// A company whose ticker cannot be confirmed is dropped entirely here, unlike
// in the news section where the story itself still had value. The whole purpose
// of this list is to name something the reader can go and look at; a name
// without a symbol they can trust does not serve it.
func VerifyRelated(ctx context.Context, v discover.Verifier, related []Related) []Related {
	if v == nil || len(related) == 0 {
		return nil
	}

	var queries []discover.Query
	var asked []int
	for i, r := range related {
		if r.Ticker == "" || r.Exchange == "" {
			continue
		}
		if _, ok := discover.Exchanges[r.Exchange]; !ok {
			continue
		}
		queries = append(queries, discover.Query{Ticker: r.Ticker, Exchange: r.Exchange})
		asked = append(asked, i)
	}
	if len(queries) == 0 {
		return nil
	}

	names, err := v.Verify(ctx, queries)
	if err != nil {
		return nil // unverified is not shown at all
	}

	var out []Related
	for n, i := range asked {
		if n >= len(names) || names[n] == "" {
			continue
		}
		if !discover.SameCompany(names[n], related[i].Name) {
			continue
		}
		r := related[i]
		r.Listed = names[n]
		out = append(out, r)
	}
	return out
}

// RelatedFor is the instruction appended to the analysis prompt.
func RelatedFor(snap Snapshot) string {
	text, err := prompts.Render("analysis.related", struct{ Marker, Company string }{
		Marker:  RelatedMarker,
		Company: snap.Company,
	})
	if err != nil {
		// Checked at startup, so this is a prompts file changed underneath a
		// running process. The analysis is still worth writing without the
		// list, which is verified and trimmed separately anyway.
		return ""
	}
	return "\n" + text
}
