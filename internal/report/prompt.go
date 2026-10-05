// Package report turns collected articles into a written market brief.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// The response is delimited rather than JSON: the sections are prose that goes
// straight to the reader, so plain text avoids a round trip through JSON
// escaping, and a malformed marker costs one section instead of the whole run.
const (
	summaryMarker  = "## IN SHORT"
	overviewMarker = "## OVERVIEW"
	sectionMarker  = "## SECTION:"
	termsMarker    = "## TERMS"
)

// systemPrompt governs the brief. Its text lives in config/prompts.md.
var systemPrompt = config.Prompt("brief.system")

// MinSectionArticles is how much news a watchlist needs before it earns a
// section. Below this the model has nothing to work with and writes around the
// absence, which costs tokens and tells the reader nothing.
//
// Three rather than one, because two articles about a sector is usually one
// story and its follow-up, and a section built on that reads thinner than no
// section at all.
const MinSectionArticles = 3

// MaxSectionArticles is how many articles one section is written from.
//
// The other end of the same question. Sections are written in three to six
// blocks whatever they are handed, so the articles past the first couple of
// dozen are read, paid for and discarded: one night's semiconductor section
// was given fifty-three and used five. Articles arrive ranked, so the cut
// falls on the weakest claim in the section, and the cost of being wrong is
// that a story is left out of a sector that already has twenty-five better
// ones.
const MaxSectionArticles = 25

// MinGeneralRating is the lowest rating an article needs to be offered as
// general market news.
//
// The general block is everything no section claimed, and it is the largest
// thing in the prompt: on the night it was measured, 216 articles and over
// half the tokens, with not one of the brief's 113 citations drawn from it.
// It exists so a story outside every watchlist can still reach the overview,
// which is worth keeping -- but that story is never a 2. Unrated articles
// stay: with sorting off, or after a failed batch, a rating of nothing means
// nothing was judged, not that it was judged unimportant.
const MinGeneralRating = 4

// MinSectionRating is the lowest rating an article needs to be written about in
// a section.
//
// It only ever turns away name matches: an article placed by judgment is
// rated 4 or more before it is placed at all, or 3 where it filled a thin
// section and the review confirmed it (triage.TopUp). Until this, a keyword match was
// in whatever its rating, on the grounds that the reader had named the subject.
// The first brief read article by article showed what that let in -- a third of
// the keyword matches were rated 1 or 2, and every one was noise: board
// appointments, product features, listicles, and above all broker notes that
// name a bank and are about something else. "Morgan Stanley sees Shell hitting
// new highs" matched Financials; the reader named Morgan Stanley to follow
// Morgan Stanley, not its analysts' view of an oil company.
//
// Three rather than four, so a named company keeps the benefit of the doubt
// that judgment does not get. Unrated articles stay, as in the general block.
const MinSectionRating = 3

// splitByCoverage divides watchlists into those with enough news to be worth a
// section and those to be named as quiet.
//
// It counts what a section would be written from, not what matched: a sector
// whose only news was three board appointments is quiet, and saying so is
// better than a section padded out of them.
func splitByCoverage(articles []model.Article, groups []model.Group, minimum int) (active []model.Group, quiet []string) {
	for _, g := range groups {
		if len(worthWriting(articles, g.ID)) >= minimum {
			active = append(active, g)
			continue
		}
		quiet = append(quiet, g.Name)
	}
	return active, quiet
}

// market is what was measured rather than written: FRED's levels, the day's
// prices, and the history of the shares that moved furthest.
type market struct {
	levels []prices.Reading
	quotes []model.Quote

	// trends are keyed by symbol, for the shares that moved furthest beyond
	// the market.
	trends map[string]model.Trading

	// since is the previous brief: a price from before it is a session that
	// brief reported, not today's move.
	since time.Time

	// calendar is what is due, for the look ahead.
	calendar model.Calendar

	// moves are the outsized moves across the whole market.
	moves []model.MarketMove

	// board is the market in its parts, and the gaps between them.
	board model.Board
}

// buildPrompt renders the articles into the user turn. Articles are ordered by
// watchlist so related stories sit together, which reads better than the
// recency order the collector produces.
func buildPrompt(articles []model.Article, groups []model.Group, m market, now time.Time, display *time.Location) string {
	if display == nil {
		display = time.UTC
	}

	// Numbered over every article kept, not just the ones written out below,
	// so a citation number means the same thing whatever the caps do. The
	// numbers the prompt shows are therefore not contiguous, which costs
	// nothing: they are identifiers, not a count.
	nums := newNumbering(articles)

	// Chosen before anything is written, so the header can say how much the
	// model is actually looking at.
	shown := make([]model.Article, 0, len(articles))
	sections := make([][]model.Article, len(groups))
	for i, g := range groups {
		sections[i] = sectionArticles(articles, g.ID)
		shown = append(shown, sections[i]...)
	}
	general := generalArticles(articles, groups)
	shown = append(shown, general...)

	var b strings.Builder
	fmt.Fprintf(&b, "Date: %s\n", now.In(display).Format("Monday, 2 January 2006"))
	fmt.Fprintf(&b, "Articles: %d from %d sources, each numbered for citation\n\n",
		len(shown), countSources(shown))

	// Market levels come first and are labelled as levels, not as news. The
	// articles say what people wrote; this says where things actually are, and
	// conflating the two would let a level be reported as though an outlet had
	// claimed it.
	if block := renderMarketData(m.levels); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := renderBoard(m.board); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := renderPrices(offBoard(m.quotes, m.board), m.trends, display); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := renderMarketMoves(m.moves); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := renderCalendar(m.calendar, display); block != "" {
		b.WriteString(block)
		b.WriteString("\n")
	}

	b.WriteString("Watchlists, in the order their sections must appear:\n")
	if len(groups) == 0 {
		b.WriteString("(none -- write the overview only)\n")
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "- %s: %s", g.ID, g.Name)
		if symbols := g.Symbols(); len(symbols) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(symbols, ", "))
		}
		b.WriteString("\n")
	}

	for i, g := range groups {
		matched := sections[i]
		fmt.Fprintf(&b, "\n=== %s (%s) -- %d articles ===\n", g.Name, g.ID, len(matched))
		if moves := sectionMoves(g, m.quotes, m.since); len(moves) > 0 {
			fmt.Fprintf(&b, "Shown to the reader above this section: biggest moves %s\n", model.Moves(moves))
		}
		if len(matched) == 0 {
			b.WriteString("(no articles matched this watchlist today)\n")
			continue
		}
		writeArticles(&b, matched, display, nums)
	}

	if len(general) > 0 {
		fmt.Fprintf(&b, "\n=== General market news -- %d articles ===\n", len(general))
		writeArticles(&b, general, display, nums)
	}

	return b.String()
}

// sectionArticles is what one section is written from: the articles the
// watchlist claimed and rated well enough to write about, strongest first,
// capped at MaxSectionArticles.
//
// The order is the ranking the collector already did, so the cap takes the tail
// rather than an arbitrary slice, and it is applied after the rating so noise
// cannot take a place a real story needed. An article cut here does not
// reappear as general news: its sector is being written about, and offering it
// back as uncovered would put a semiconductor story in the overview because the
// semiconductor section was full.
func sectionArticles(articles []model.Article, groupID string) []model.Article {
	matched := worthWriting(articles, groupID)
	if len(matched) > MaxSectionArticles {
		matched = matched[:MaxSectionArticles]
	}
	return matched
}

// worthWriting is the watchlist's articles less the ones rated below
// MinSectionRating. Unrated articles are kept: with sorting off, or after a
// failed batch, no rating means nothing was judged.
func worthWriting(articles []model.Article, groupID string) []model.Article {
	matched := articlesInGroup(articles, groupID)
	out := make([]model.Article, 0, len(matched))
	for _, a := range matched {
		if a.Rating == 0 || a.Rating >= MinSectionRating {
			out = append(out, a)
		}
	}
	return out
}

// generalArticles is everything no section claimed and worth the overview's
// attention.
//
// Offered separately rather than dropped, because an article belonging only to
// a watchlist too quiet for a section would otherwise fall out of the prompt
// entirely, which is how a sector goes silently missing. "Claimed" means
// claimed by a section being written, not merely matched.
func generalArticles(articles []model.Article, groups []model.Group) []model.Article {
	rest := uncovered(articles, groups)
	out := make([]model.Article, 0, len(rest))
	for _, a := range rest {
		if a.Rating == 0 || a.Rating >= MinGeneralRating {
			out = append(out, a)
		}
	}
	return out
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
func renderMarketData(levels []prices.Reading) string {
	if len(levels) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nMarket levels, as reported by FRED. These are measured values, not claims made by any article -- use them to anchor the macro section, and do not attribute them to a source:\n")
	for _, r := range levels {
		b.WriteString("- " + r.Line() + "\n")
	}
	return b.String()
}

// renderBoard writes the market in its parts, a group at a time, then the
// gaps between pairs of funds with their usual sizes and what each says.
// Measured, like the prices, and labelled so.
func renderBoard(board model.Board) string {
	if len(board.Funds) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nThe market in its parts on the last US session, %s, from each fund's close against the session before, and a week and a month back. Measured on the exchange, not reported by any article:\n",
		board.Session.Format("Monday 2 January"))
	names, funds := board.Groups()
	for _, name := range names {
		b.WriteString(name + ":\n")
		for _, f := range funds[name] {
			if f.Yield {
				fmt.Fprintf(&b, "- %s: %s, %s percentage points on the day, %s over the week, %s over the month\n",
					f.Name, f.LevelText(), f.Move(f.Day), f.Move(f.Week), f.Move(f.Month))
				continue
			}
			fmt.Fprintf(&b, "- %s (%s): %s, %s on the day, %s over the week, %s over the month\n",
				f.Name, f.Symbol, f.LevelText(), f.Move(f.Day), f.Move(f.Week), f.Move(f.Month))
		}
	}
	if len(board.Gaps) == 0 {
		return b.String()
	}
	b.WriteString("\nThe gaps between them. Each is the first fund's move less the second's, in percentage points, beside its usual size over the past year. Each is read on the session or the month, whichever is the larger against its usual size. UNUSUAL marks a gap at least one and a half times its usual size: the reader is shown those in a line under the overview, and every gap on the page.\n")
	for _, g := range board.Gaps {
		fmt.Fprintf(&b, "- %s (%s less %s): %+.1f on the day, against a usual %.1f; %+.1f over the month, against a usual %.1f.",
			g.Name, g.A, g.B, g.Day, g.UsualDay, g.Month, g.UsualMonth)
		if g.Unusual {
			if g.Window == "month" {
				b.WriteString(" UNUSUAL over the month.")
			} else {
				b.WriteString(" UNUSUAL on the day.")
			}
		}
		b.WriteString(" Reads: " + g.Reading + "\n")
	}
	return b.String()
}

// offBoard is the quotes the board does not already show.
func offBoard(quotes []model.Quote, board model.Board) []model.Quote {
	if len(board.Funds) == 0 {
		return quotes
	}
	out := make([]model.Quote, 0, len(quotes))
	for _, q := range quotes {
		if !prices.OnBoard(q.Symbol) {
			out = append(out, q)
		}
	}
	return out
}

// renderMarketMoves lists the outsized moves across the market, with each
// company's name so an article about it can be found.
func renderMarketMoves(moves []model.MarketMove) string {
	if len(moves) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nAcross the market, the last session's biggest moves against each share's usual, among companies worth US$2bn or more, followed or not. The reader is shown this line under the overview:\n")
	for _, m := range moves {
		fmt.Fprintf(&b, "- %s (%s): %s, %.1f times its usual daily move, on %.1f times its usual trading\n",
			m.Symbol, m.Name, m.Move(), m.Times, m.Busy)
	}
	return b.String()
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
func renderPrices(quotes []model.Quote, trends map[string]model.Trading, display *time.Location) string {
	if len(quotes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nPrices, as measured on the exchange rather than reported by any article. Use them to say how the market answered the news, and do not attribute them to a source:\n")
	for _, q := range quotes {
		fmt.Fprintf(&b, "- %s: %.2f, %s on its last session", prices.LabelFor(q.Symbol), q.Price, q.Move())
		if q.Previous > 0 {
			fmt.Fprintf(&b, " (previous close %.2f)", q.Previous)
		}
		if !q.AsOf.IsZero() {
			fmt.Fprintf(&b, ", as at %s", q.AsOf.In(display).Format("15:04 on 2 Jan"))
		}
		if t, ok := trends[q.Symbol]; ok {
			if desc := trend(q, t); desc != "" {
				fmt.Fprintf(&b, ". Against its own history: %s", desc)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("A move of \"flat\" means the price barely changed; say so rather than inventing a direction. Prices are US listings only, so a company quoted elsewhere has none here -- say that instead of guessing.\n")
	return b.String()
}
