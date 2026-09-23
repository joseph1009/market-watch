// Package ideas finds companies worth a closer look and judges them.
//
// It starts where the brief ends. The new-names section says which companies
// the day's stories were about; this goes further, to the companies the news
// bears on without naming -- the supplier whose order book a customer's outlook
// just changed, the rival a price cut hurts -- and then gives each one a
// verdict: buy, hold or sell over twelve months.
//
// Two model calls, with a fetch between them. Research runs with web search,
// because finding who supplies whom is exactly what the day's articles do not
// say. Every ticker it proposes is checked against the exchange, the same way
// the new names are, and a company that does not check out is dropped: a
// verdict on the wrong symbol is the worst thing this package could produce.
// The facts are then read -- the share's trading, and for a US listing its SEC
// accounts -- and the verdicts are written from those, with no tools, so the
// figures they cite are ones this service fetched rather than ones a web page
// asserted.
//
// The verdicts go to the owner, and to the channel with the daily brief, where
// they are published under a note saying a model wrote them and that they are
// not advice. See internal/app/ideas.go.
package ideas

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prompts"
)

// DefaultMax is how many companies the research may propose. Each costs a
// fetch of its accounts and a verdict, and a message of six is still read on a
// phone; one of fifteen is skimmed.
const DefaultMax = 6

// Completer is the model call both stages make.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, model.Usage, error)
}

// Input is what the research starts from.
type Input struct {
	// Brief is the brief's prose, overview and sections, with its [n]
	// citations, and Cited the articles those numbers refer to.
	Brief string
	Cited []model.Article

	// Candidates are the day's new names, and Tracked every company name and
	// ticker the watchlists carry, so the research can tell what the investor
	// already follows.
	Candidates []model.Candidate
	Tracked    []string
}

// Researcher proposes and verifies the companies.
type Researcher struct {
	Completer Completer
	Verifier  discover.Verifier
	Max       int
}

// Propose returns the companies worth a closer look, every ticker verified.
func (r *Researcher) Propose(ctx context.Context, in Input) ([]model.Idea, model.Usage, error) {
	if r.Completer == nil {
		return nil, model.Usage{}, nil
	}
	system, err := prompts.Render("ideas.system", struct{ Max int }{r.max()})
	if err != nil {
		return nil, model.Usage{}, err
	}

	text, usage, err := r.Completer.Complete(ctx, system, researchPrompt(in))
	if err != nil {
		// Unwrapped: the answerer names the stage it was asked for, and the
		// caller says what the section was.
		return nil, usage, err
	}

	found := parseIdeas(text, in.Cited)
	found, err = verify(ctx, r.Verifier, found)
	if err != nil {
		// Unchecked tickers are not shown, and a verdict needs a ticker, so a
		// failed check leaves nothing to judge.
		return nil, usage, fmt.Errorf("verifying tickers: %w", err)
	}
	if len(found) > r.max() {
		found = found[:r.max()]
	}
	return found, usage, nil
}

func (r *Researcher) max() int {
	if r.Max > 0 {
		return r.Max
	}
	return DefaultMax
}

func researchPrompt(in Input) string {
	var b strings.Builder
	b.WriteString("Today's brief:\n\n")
	b.WriteString(strings.TrimSpace(in.Brief))

	b.WriteString("\n\nThe articles it cites, by number:\n")
	for i, a := range in.Cited {
		fmt.Fprintf(&b, "[%d] %s (%s)\n", i+1, oneLine(a.Title), a.SourceName)
	}

	if len(in.Candidates) > 0 {
		b.WriteString("\nNew names the brief found in today's news:\n")
		for _, c := range in.Candidates {
			symbol := c.Symbol()
			switch {
			case c.Private:
				symbol = "private"
			case symbol == "":
				symbol = "ticker unknown"
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n", c.Name, symbol, oneLine(c.Why))
		}
	}

	if len(in.Tracked) > 0 {
		b.WriteString("\nCompanies the investor already tracks: ")
		b.WriteString(strings.Join(in.Tracked, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

var ideaLine = regexp.MustCompile(`^\s*-?\s*([^|]+)\|([^|]+)\|([^|]+)\|([^|]+)\|([^|]*)\|(.+?)\s*$`)

// parseIdeas reads the research's lines back, keeping a company once and only
// with a ticker to check.
func parseIdeas(text string, cited []model.Article) []model.Idea {
	var out []model.Idea
	seen := map[string]bool{}

	for _, raw := range strings.Split(text, "\n") {
		m := ideaLine.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		idea := model.Idea{
			Name:      strings.TrimSpace(m[1]),
			Ticker:    strings.ToUpper(strings.TrimSpace(m[2])),
			Exchange:  strings.ToUpper(strings.TrimSpace(m[3])),
			Connected: strings.Contains(strings.ToLower(m[4]), "connect"),
			Link:      strings.TrimSpace(m[6]),
		}
		switch idea.Ticker {
		case "", "?", "-", "PRIVATE":
			continue // nothing to verify, so nothing to judge
		}
		if idea.Name == "" || idea.Link == "" {
			continue
		}
		key := idea.Ticker + "." + idea.Exchange
		if seen[key] {
			continue
		}
		seen[key] = true

		for _, field := range strings.Split(m[5], ",") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.Trim(field, "[] ")))
			if err != nil || n < 1 || n > len(cited) {
				continue
			}
			if !hasArticle(idea.Articles, cited[n-1].ID) {
				idea.Articles = append(idea.Articles, cited[n-1])
			}
		}
		out = append(out, idea)
	}
	return out
}

// verify keeps the ideas whose ticker belongs to the company named, and drops
// the rest. Unlike a new name, an idea without a trustworthy ticker has no
// value: its whole point is a verdict on a share someone could buy.
func verify(ctx context.Context, v discover.Verifier, ideas []model.Idea) ([]model.Idea, error) {
	if v == nil {
		return nil, fmt.Errorf("no verifier")
	}

	var queries []discover.Query
	var asked []int
	for i, idea := range ideas {
		if _, ok := discover.Exchanges[idea.Exchange]; !ok {
			continue
		}
		queries = append(queries, discover.Query{Ticker: idea.Ticker, Exchange: idea.Exchange})
		asked = append(asked, i)
	}
	if len(queries) == 0 {
		return nil, nil
	}

	names, err := v.Verify(ctx, queries)
	if err != nil {
		return nil, err
	}

	var out []model.Idea
	for n, i := range asked {
		if n >= len(names) || names[n] == "" || !discover.SameCompany(names[n], ideas[i].Name) {
			continue
		}
		idea := ideas[i]
		idea.Listed = names[n]
		out = append(out, idea)
	}
	return out, nil
}

func hasArticle(articles []model.Article, id string) bool {
	for _, a := range articles {
		if a.ID == id {
			return true
		}
	}
	return false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
