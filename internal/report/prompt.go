// Package report turns collected articles into a written market brief using
// the Claude API.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/marketdata"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/prompts"
)

// The response is delimited rather than JSON: the sections are prose that goes
// straight to the reader, so plain text avoids a round trip through JSON
// escaping, and a malformed marker costs one section instead of the whole run.
const (
	overviewMarker = "## OVERVIEW"
	sectionMarker  = "## SECTION:"
)

// systemPrompt governs the brief. Its text lives in internal/prompts.
var systemPrompt = prompts.Get("brief.system")

// MinSectionArticles is how much news a watchlist needs before it earns a
// section. Below this the model has nothing to work with and writes around the
// absence, which costs tokens and tells the reader nothing.
//
// Three rather than one, because two articles about a sector is usually one
// story and its follow-up, and a section built on that reads thinner than no
// section at all.
const MinSectionArticles = 3

// splitByCoverage divides watchlists into those with enough news to be worth a
// section and those to be named as quiet.
func splitByCoverage(articles []model.Article, groups []model.Group, minimum int) (active []model.Group, quiet []string) {
	for _, g := range groups {
		if len(articlesInGroup(articles, g.ID)) >= minimum {
			active = append(active, g)
			continue
		}
		quiet = append(quiet, g.Name)
	}
	return active, quiet
}

// buildPrompt renders the articles into the user turn. Articles are ordered by
// watchlist so related stories sit together, which reads better than the
// recency order the collector produces.
func buildPrompt(articles []model.Article, groups []model.Group, levels []marketdata.Reading, quotes []model.Quote, now time.Time, display *time.Location) string {
	if display == nil {
		display = time.UTC
	}

	nums := newNumbering(articles)

	var b strings.Builder
	fmt.Fprintf(&b, "Date: %s\n", now.In(display).Format("Monday, 2 January 2006"))
	fmt.Fprintf(&b, "Articles: %d from %d sources, each numbered for citation\n\n",
		len(articles), countSources(articles))

	// Market levels come first and are labelled as levels, not as news. The
	// articles say what people wrote; this says where things actually are, and
	// conflating the two would let a level be reported as though an outlet had
	// claimed it.
	if block := renderMarketData(levels); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := renderPrices(quotes, display); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}

	b.WriteString("Watchlists, in the order their sections must appear:\n")
	if len(groups) == 0 {
		b.WriteString("(none -- write the overview only)\n")
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "- %s: %s", g.ID, g.Name)
		if len(g.Tickers) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(g.Tickers, ", "))
		}
		b.WriteString("\n")
	}

	for _, g := range groups {
		matched := articlesInGroup(articles, g.ID)
		fmt.Fprintf(&b, "\n=== %s (%s) -- %d articles ===\n", g.Name, g.ID, len(matched))
		if len(matched) == 0 {
			b.WriteString("(no articles matched this watchlist today)\n")
			continue
		}
		writeArticles(&b, matched, display, nums)
	}

	// Everything no section claimed still informs the overview, so it is offered
	// separately rather than dropped. That has to mean "claimed by a section
	// being written", not "matched something": an article belonging only to a
	// watchlist too quiet for a section would otherwise fall out of the prompt
	// entirely, which is how a sector goes silently missing.
	if general := uncovered(articles, groups); len(general) > 0 {
		fmt.Fprintf(&b, "\n=== General market news -- %d articles ===\n", len(general))
		writeArticles(&b, general, display, nums)
	}

	return b.String()
}

// numbering gives every article a citation number, stable across the whole
// prompt, so a claim can point at the article it came from and the rendered
// brief can turn that pointer into a link.
type numbering struct {
	order []model.Article
	index map[string]int
}

func newNumbering(articles []model.Article) *numbering {
	n := &numbering{order: articles, index: make(map[string]int, len(articles))}
	for i, a := range articles {
		n.index[a.ID] = i + 1
	}
	return n
}

func (n *numbering) of(a model.Article) int {
	if n == nil {
		return 0
	}
	return n.index[a.ID]
}

func writeArticles(b *strings.Builder, articles []model.Article, display *time.Location, nums *numbering) {
	for _, a := range articles {
		fmt.Fprintf(b, "\n[%d] %s\n  %s, %s", nums.of(a), a.Title, a.SourceName,
			a.Published.In(display).Format("15:04 on 2 Jan"))
		// Said on the article rather than in a list at the end: the model is
		// deciding what to write about while it reads this line.
		if !a.Covered.IsZero() {
			fmt.Fprintf(b, " -- already reported in the brief of %s", a.Covered.In(display).Format("2 Jan"))
		}
		b.WriteString("\n")
		if a.Summary != "" {
			fmt.Fprintf(b, "  %s\n", a.Summary)
		}
	}
}

func articlesInGroup(articles []model.Article, groupID string) []model.Article {
	out := make([]model.Article, 0, len(articles))
	for _, a := range articles {
		if a.InGroup(groupID) {
			out = append(out, a)
		}
	}
	return out
}

// uncovered returns the articles no listed watchlist will report on: both the
// ones that matched nothing and the ones whose only watchlist is too quiet for
// a section of its own.
func uncovered(articles []model.Article, groups []model.Group) []model.Article {
	covered := make(map[string]bool, len(groups))
	for _, g := range groups {
		covered[g.ID] = true
	}

	out := make([]model.Article, 0, len(articles))
	for _, a := range articles {
		claimed := false
		for _, id := range a.GroupIDs {
			if covered[id] {
				claimed = true
				break
			}
		}
		if !claimed {
			out = append(out, a)
		}
	}
	return out
}

func countSources(articles []model.Article) int {
	seen := make(map[string]struct{}, len(articles))
	for _, a := range articles {
		seen[a.SourceID] = struct{}{}
	}
	return len(seen)
}

// sortedGroupIDs is used only in errors and logs, where a stable order makes
// two runs comparable.
func sortedGroupIDs(groups []model.Group) []string {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	sort.Strings(ids)
	return ids
}

// renderMarketData writes the levels block.
//
// Directions are stated rather than implied by a sign, because the reader of
// this block is a language model and "-0.04" invites it to describe a fall as a
// rise. A move that is not known is left out rather than shown as zero.
func renderMarketData(levels []marketdata.Reading) string {
	if len(levels) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nMarket levels, as reported by FRED. These are measured values, not claims made by any article -- use them to anchor the macro section, and do not attribute them to a source:\n")
	for _, r := range levels {
		fmt.Fprintf(&b, "- %s: %.2f%s as of %s",
			r.Label, r.Latest, r.Unit, r.AsOf.Format("2 Jan"))
		if r.HasPrevious {
			fmt.Fprintf(&b, ", %s since the previous session", describeMove(r.Change()))
		}
		if r.HasWeekAgo {
			fmt.Fprintf(&b, ", %s over the past week", describeMove(r.WeeklyChange()))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func describeMove(delta float64) string {
	const flat = 0.005 // below this the move rounds to nothing at two decimals
	switch {
	case delta > flat:
		return fmt.Sprintf("up %.2f", delta)
	case delta < -flat:
		return fmt.Sprintf("down %.2f", -delta)
	default:
		return "unchanged"
	}
}

// renderPrices writes what shares actually did.
//
// Labelled as measured, like the market levels and for the same reason: an
// article says what someone wrote, and this says what the market paid. A model
// given both without the distinction will happily report a price as though an
// outlet had claimed it.
//
// The funds are named as funds. SPY is not the S&P 500, it is a fund that
// tracks it, and on a bad day the two differ -- so the brief says "SPY fund"
// rather than quietly passing one off as the other.
func renderPrices(quotes []model.Quote, display *time.Location) string {
	if len(quotes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nPrices, as measured on the exchange rather than reported by any article. Use them to say how the market answered the news, and do not attribute them to a source:\n")
	for _, q := range quotes {
		fmt.Fprintf(&b, "- %s: %.2f, %s on the day", prices.LabelFor(q.Symbol), q.Price, q.Move())
		if q.Previous > 0 {
			fmt.Fprintf(&b, " (previous close %.2f)", q.Previous)
		}
		if !q.AsOf.IsZero() {
			fmt.Fprintf(&b, ", as at %s", q.AsOf.In(display).Format("15:04 on 2 Jan"))
		}
		b.WriteString("\n")
	}
	b.WriteString("A move of \"flat\" means the price barely changed; say so rather than inventing a direction. Prices are US listings only, so a company quoted elsewhere has none here -- say that instead of guessing.\n")
	return b.String()
}
