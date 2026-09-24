package ideas

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// DefaultPicks is how many followed companies the screen may choose: six of
// the day's twenty. Fewer is fine, and usual on a quiet day.
const DefaultPicks = 6

// Row is one followed company as the screen sees it: the figures written out
// on one line, and the day's articles that name it.
type Row struct {
	Ticker, Name, Sector string
	Line                 string
	Articles             []model.Article
}

// Screener chooses which followed companies are worth a verdict today.
//
// Nearly a hundred companies are followed, and a verdict on each every day
// would take hours of the most expensive model and say HOLD to most of them.
// So one pass reads them all as a table -- the day's move against the week,
// month and year, where the price sits against its averages, what analysts
// expect and which way that has moved, and the day's headlines -- and picks
// the few where what changed and how the share moved do not fit each other.
// Only those go on to the facts and the verdicts.
type Screener struct {
	Completer Completer
	Max       int
}

// Pick returns the chosen companies as ideas, in the screen's order, each
// with the screen's reason as its link to the day.
func (s *Screener) Pick(ctx context.Context, brief string, cited []model.Article, rows []Row) ([]model.Idea, model.Usage, error) {
	if s.Completer == nil || len(rows) == 0 {
		return nil, model.Usage{}, nil
	}
	system, err := config.RenderPrompt("screen.system", struct{ Max int }{s.max()})
	if err != nil {
		return nil, model.Usage{}, err
	}
	text, usage, err := s.Completer.Complete(ctx, system, screenPrompt(brief, cited, rows))
	if err != nil {
		return nil, usage, err
	}

	byTicker := make(map[string]Row, len(rows))
	for _, r := range rows {
		byTicker[r.Ticker] = r
	}
	var out []model.Idea
	for _, line := range strings.Split(text, "\n") {
		m := pickLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		row, ok := byTicker[strings.ToUpper(m[1])]
		if !ok {
			continue // a ticker it was not shown is not one it can choose
		}
		delete(byTicker, row.Ticker)
		out = append(out, model.Idea{
			Name: row.Name, Listed: row.Name, Ticker: row.Ticker, Exchange: "US",
			Followed: true, Link: strings.TrimSpace(m[2]), Articles: row.Articles,
		})
		if len(out) == s.max() {
			break
		}
	}
	return out, usage, nil
}

var pickLine = regexp.MustCompile(`^\s*-?\s*([A-Za-z]{1,5}(?:[.-][A-Za-z])?)\s*\|\s*(.+?)\s*$`)

func screenPrompt(brief string, cited []model.Article, rows []Row) string {
	var b strings.Builder
	b.WriteString("Today's brief:\n\n")
	b.WriteString(strings.TrimSpace(brief))

	b.WriteString("\n\nThe articles it cites, by number:\n")
	for i, a := range cited {
		fmt.Fprintf(&b, "[%d] %s (%s)\n", i+1, oneLine(a.Title), a.SourceName)
	}

	b.WriteString("\nThe companies the investor follows, one per line: ticker, name, section, then the figures and today's articles that name it.\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s | %s | %s | %s", r.Ticker, r.Name, r.Sector, r.Line)
		var nums []string
		for _, a := range r.Articles {
			if n := number(a, cited); n > 0 {
				nums = append(nums, fmt.Sprintf("[%d]", n))
			}
		}
		if len(nums) > 0 {
			b.WriteString(" | today's articles " + strings.Join(nums, ""))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Screener) max() int {
	if s.Max > 0 {
		return s.Max
	}
	return DefaultPicks
}
