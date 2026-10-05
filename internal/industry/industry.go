// Package industry explains how an industry fits together, for /industry: its
// building blocks from raw materials to the customer, what each part does and
// how it makes money, and the listed companies worth looking into in each.
//
// It complements /analyse. An analysis reads one company's accounts in depth;
// this gives the map that company sits on -- who supplies it, who it competes
// with, and which part of the industry the money and the growth are in.
//
// The model may search the web, since an industry's structure, its leaders and
// its size are not in any feed. Every company it names is checked against
// OpenFIGI, and one whose ticker cannot be confirmed is dropped, as everywhere
// else in the service.
package industry

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/model"
)

// Marker is the line the model puts before its list of companies, and the
// line this cuts out of the prose.
const Marker = "COMPANIES BY PART"

// Completer is the model.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

// Company is one listed company worth looking into, under the part of the
// industry it belongs to.
type Company struct {
	Part string
	fundamentals.Related
}

// Explanation is what the reader is sent.
type Explanation struct {
	Topic     string
	Text      string // the prose, in "### " sub-headings over bullets
	Companies []Company
	Usage     model.Usage

	// Sources are the pages the prose cites by number, [3], in that order,
	// as the analysis's are (fundamentals.SplitSources).
	Sources []fundamentals.Source `json:",omitempty"`
}

// Explainer writes the explanation.
type Explainer struct {
	Completer Completer
	Verifier  discover.Verifier
	Now       func() time.Time
}

// Explain explains the industry the reader named, in their words: "robotics",
// "AI", "automobiles".
func (e *Explainer) Explain(ctx context.Context, topic string) (Explanation, error) {
	topic = strings.TrimSpace(topic)
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	prompt := fmt.Sprintf("Today is %s.\n\nThe industry the reader asked about, in their words: %s\n",
		now.Format("Monday 2 January 2006"), topic)
	text, usage, err := e.Completer.Complete(ctx, config.Prompt("industry.system"), prompt)
	if err != nil {
		return Explanation{}, fmt.Errorf("explain %q: %w", topic, err)
	}
	// The sources first, wherever the model put them: after the companies,
	// the companies' list would take them in.
	text, sources := fundamentals.SplitSources(text)
	prose, companies := Split(text)
	return Explanation{
		Topic:     topic,
		Text:      prose,
		Companies: Verify(ctx, e.Verifier, companies),
		Usage:     usage,
		Sources:   sources,
	}, nil
}

var companyLine = regexp.MustCompile(`^\s*-?\s*([^|]+)\|([^|]+)\|([^|]*)\|([^|]*)\|(.+?)\s*$`)

// Split separates the prose from the list of companies after Marker, written
// as part|name|ticker|exchange|why.
func Split(text string) (prose string, companies []Company) {
	at := strings.Index(text, Marker)
	if at < 0 {
		return strings.TrimSpace(text), nil
	}
	prose = strings.TrimSpace(text[:at])
	for _, raw := range strings.Split(text[at+len(Marker):], "\n") {
		m := companyLine.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		c := Company{Part: strings.TrimSpace(m[1]), Related: fundamentals.Related{
			Name:     strings.TrimSpace(m[2]),
			Ticker:   strings.ToUpper(strings.TrimSpace(m[3])),
			Exchange: strings.ToUpper(strings.TrimSpace(m[4])),
			Why:      strings.TrimSpace(m[5]),
		}}
		if c.Part == "" || c.Name == "" || c.Why == "" || strings.EqualFold(c.Part, "part") {
			continue
		}
		companies = append(companies, c)
	}
	return prose, companies
}

// Verify keeps the companies whose ticker the exchange confirms belongs to
// the company named, in the order given. A failed check shows none.
func Verify(ctx context.Context, v discover.Verifier, companies []Company) []Company {
	related := make([]fundamentals.Related, len(companies))
	for i, c := range companies {
		related[i] = c.Related
	}
	kept := map[string]string{}
	for _, r := range fundamentals.VerifyRelated(ctx, v, related) {
		kept[r.Ticker+"|"+r.Exchange+"|"+r.Name] = r.Listed
	}
	var out []Company
	for _, c := range companies {
		if listed, ok := kept[c.Ticker+"|"+c.Exchange+"|"+c.Name]; ok {
			c.Listed = listed
			out = append(out, c)
		}
	}
	return out
}
