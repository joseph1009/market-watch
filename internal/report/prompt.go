// Package report turns collected articles into a written market brief using
// the Claude API.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The response is delimited rather than JSON: the sections are prose that goes
// straight to the reader, so plain text avoids a round trip through JSON
// escaping, and a malformed marker costs one section instead of the whole run.
const (
	overviewMarker = "## OVERVIEW"
	sectionMarker  = "## SECTION:"
)

const systemPrompt = `You write a daily stock-market brief for a single reader who follows the US market from Singapore. They have already missed the trading day by the time they read this: it lands the next morning, local time. Write what a well-informed colleague would tell them over coffee.

You will be given the day's news articles, already matched to the reader's watchlists. Write from those articles and nothing else.

Rules:
- Use only what the articles state. Do not add prices, percentages, dates or events that are not in the text you were given.
- Prefer what changed and why it matters over a list of headlines. Group related stories into a single thread rather than repeating each one.
- If the articles genuinely do not support a claim, leave it out. A short section is fine; an invented one is not.
- Where sources disagree or a story is only a report or rumour, say so plainly.
- No preamble, no sign-off, no "here is your brief". Start with the substance.

This is read on a phone. The reader wants the detail and the technical substance -- keep every number, attribution and caveat. What they do not want is density. Break the same content into more, smaller pieces:

- Give every block a short topic label, then " - ", then the point. For example: "Oil - Brent topped $100 for the first time since July."
- Two or three sentences per block, and never more than about forty-five words. If a block runs long, split it into two labelled blocks. Do not solve it by cutting substance.
- Blank line between every block.
- Where the content is a set of separate items -- company moves, data prints, who said what on the committee -- write one item per line starting with "- ". Reach for a list whenever the items do not share a causal thread.
- Lead with the point, then the detail. Do not build up to the conclusion.
- No markdown headings of your own beyond the markers below, no bold, no emoji.

Output format, exactly:

## OVERVIEW
Open with one short line -- under fifteen words, no label -- naming the single thing that defined the day. Then four to eight labelled blocks: the dominant themes, notable moves, and anything the reader should act on or watch. This is the part they read if they read nothing else.

## SECTION: <watchlist-id>
Three to six labelled blocks or lists on that watchlist, covering only what the overview did not already say. Repeat the marker for each watchlist you were given, using its exact id.

Emit a SECTION block for every watchlist id you are given, in the order given. If a watchlist has no meaningful news, write a single short sentence saying so.`

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
func buildPrompt(articles []model.Article, groups []model.Group, now time.Time, display *time.Location) string {
	if display == nil {
		display = time.UTC
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Date: %s\n", now.In(display).Format("Monday, 2 January 2006"))
	fmt.Fprintf(&b, "Articles: %d from %d sources\n\n", len(articles), countSources(articles))

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
		writeArticles(&b, matched, display)
	}

	// Everything no section claimed still informs the overview, so it is offered
	// separately rather than dropped. That has to mean "claimed by a section
	// being written", not "matched something": an article belonging only to a
	// watchlist too quiet for a section would otherwise fall out of the prompt
	// entirely, which is how a sector goes silently missing.
	if general := uncovered(articles, groups); len(general) > 0 {
		fmt.Fprintf(&b, "\n=== General market news -- %d articles ===\n", len(general))
		writeArticles(&b, general, display)
	}

	return b.String()
}

func writeArticles(b *strings.Builder, articles []model.Article, display *time.Location) {
	for _, a := range articles {
		fmt.Fprintf(b, "\n- %s\n  %s, %s\n", a.Title, a.SourceName, a.Published.In(display).Format("15:04 on 2 Jan"))
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
