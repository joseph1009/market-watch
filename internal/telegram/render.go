package telegram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

const (
	// Telegram caps a message at 4096 characters. The margin absorbs the
	// difference between the runes counted here and the UTF-16 units Telegram
	// counts, where an emoji costs two.
	maxMessageRunes = 3900

	// maxLinksPerSection keeps a heavy news day readable. The brief is the
	// product; the links are there to follow up on it, not to be a full index.
	maxLinksPerSection = 8

	// maxSourceBlockRunes bounds one run of links. It sits below the message
	// limit because the first run is joined to the Sources heading when a list
	// has to be split, and that join must never push it over and force a cut
	// through the middle of a link.
	maxSourceBlockRunes = maxMessageRunes - 200

	// divider separates sections. Telegram's HTML mode has no horizontal rule,
	// so the break has to be drawn.
	divider = "──────────"

	// generalCategory labels the articles no section claimed. Listed only in
	// full mode, where the point is to check the overview against its evidence.
	generalCategory = "General market news (informed the overview)"
)

// SourceMode says whether the brief carries its source links, and how many.
//
// The zero value is the short list: a caller that says nothing about links
// gets the usual brief rather than one stripped of its references.
type SourceMode int

const (
	// SourcesShort lists the first few links behind each section.
	SourcesShort SourceMode = iota

	// SourcesFull lists every article behind each section and adds the general
	// news the overview drew on. For checking the brief against what it was
	// written from, where the short list hides most of the evidence.
	SourcesFull

	// SourcesOff omits the source list altogether, for a reader who wants the
	// analysis and nothing else.
	SourcesOff
)

// Options controls how a report is laid out.
type Options struct {
	Display *time.Location
	Sources SourceMode
}

// segment is a run of blocks that reads as one unit -- a section's heading and
// its prose -- and so shares a message wherever it fits.
//
// Packing used to work block by block, filling each message to the limit and
// breaking wherever the limit happened to fall. That left a section heading at
// the foot of one message with its prose opening the next, and split sections
// at arbitrary paragraphs. Packing by segment moves the breaks to the places a
// reader expects them: between sections.
type segment struct {
	blocks []string

	// newMessage starts the segment on a fresh message even when there is room
	// left in the current one.
	newMessage bool
}

// Render lays out a report with the short source list.
func Render(rep model.Report, display *time.Location) []string {
	return RenderWith(rep, Options{Display: display})
}

// RenderWith turns a report into the messages to send, in order. Long reports
// are split across several messages because Telegram rejects anything longer.
func RenderWith(rep model.Report, opts Options) []string {
	display := opts.Display
	if display == nil {
		display = time.UTC
	}

	segs := []segment{{blocks: []string{
		fmt.Sprintf("<b>📊 Market Watch</b>\n<i>%s</i>",
			escape(rep.GeneratedAt.In(display).Format("Monday, 2 January 2006 · 15:04 MST"))),
	}}}

	if rep.Overview != "" {
		segs = append(segs, segment{blocks: append(
			[]string{divider + "\n<b>Overview</b>"}, cite(paragraphs(rep.Overview), rep.Cited)...)})
	}

	// Every category's prose runs uninterrupted, and the links follow at the
	// end. Interleaving a source list after each section broke the reading into
	// prose, links, prose, links -- the reader had to skip past a list of
	// headlines to reach the next piece of analysis.
	for _, s := range rep.Sections {
		blocks := []string{fmt.Sprintf("%s\n<b>%s</b>", divider, escape(s.GroupName))}
		if s.Body != "" {
			blocks = append(blocks, cite(paragraphs(s.Body), rep.Cited)...)
		}
		segs = append(segs, segment{blocks: blocks})
	}

	if blocks := renderCandidates(rep.Candidates, rep.Cited); len(blocks) > 0 {
		segs = append(segs, segment{blocks: blocks})
	}

	// The quiet watchlists close the prose, so the analysis ends by accounting
	// for what was absent before the reference material begins.
	if quiet := renderQuiet(rep.QuietGroups); quiet != "" {
		segs = append(segs, segment{blocks: []string{divider + "\n" + quiet}})
	}

	// Sources are reference material rather than reading, so they open a message
	// of their own: no message ever holds both analysis and links. Off, there is
	// no such message at all -- the brief ends with the analysis and the footer.
	var sources []string
	if opts.Sources != SourcesOff {
		limit := maxLinksPerSection
		if opts.Sources == SourcesFull {
			limit = 0
		}
		for _, s := range rep.Sections {
			sources = append(sources, renderSources(s.GroupName, s.Articles, limit)...)
		}
		if opts.Sources == SourcesFull {
			sources = append(sources, renderSources(generalCategory, rep.General, 0)...)
		}
	}
	if len(sources) > 0 {
		segs = append(segs, segment{
			blocks:     append([]string{"<b>Sources</b>"}, sources...),
			newMessage: true,
		})
	}

	segs = append(segs, segment{blocks: []string{divider + "\n" + renderFooter(rep)}})

	return pack(segs)
}

// renderQuiet names the watchlists that had too little news for a section.
//
// It is part of the brief rather than a statistic about it: "these sectors were
// checked and had nothing" answers the question a reader asks when a sector
// they track is missing, and it reads as neither finding nor number when it
// sits beside the token count.
func renderQuiet(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	return fmt.Sprintf("<b>Quiet today</b>\n%s — nothing that warranted a section.",
		escape(strings.Join(groups, ", ")))
}

// renderSources lists the stories behind a category as one or more blocks.
//
// A limit of zero lists everything. A full list can outgrow a message, so it is
// broken into runs of whole links -- each its own block, which the packer only
// ever splits between -- and every run after the first says the category
// continues, since it may open a new message with nothing above it.
func renderSources(category string, articles []model.Article, limit int) []string {
	if len(articles) == 0 {
		return nil
	}

	shown := articles
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}

	var lines []string
	for _, a := range shown {
		lines = append(lines, fmt.Sprintf("• <a href=\"%s\">%s</a> — %s",
			escape(a.URL), escape(a.Title), escape(a.SourceName)))
	}
	if extra := len(articles) - len(shown); extra > 0 {
		lines = append(lines, fmt.Sprintf("<i>+%d more article(s) not listed</i>", extra))
	}

	// Named by category, since the links do not sit under the prose they
	// support and the reader needs to know which analysis each list backs.
	var blocks []string
	current := "<i>" + escape(category) + "</i>"
	for _, line := range lines {
		if runeLen(current)+1+runeLen(line) > maxSourceBlockRunes {
			blocks = append(blocks, current)
			current = "<i>" + escape(category) + " (continued)</i>"
		}
		current += "\n" + line
	}
	return append(blocks, current)
}

// renderFooter reports the run's size and what it cost. The cost is an
// estimate priced from a local table, never a balance -- the API reports what a
// request spent, not what the account has left.
func renderFooter(rep model.Report) string {
	lines := []string{fmt.Sprintf("<i>%d articles from %d sources</i>", rep.ArticleCount, rep.SourceCount)}

	if u := rep.Usage; u.Total() > 0 {
		lines = append(lines, usageLine("", u))
	}
	if u := rep.Triage; u.Total() > 0 {
		lines = append(lines, usageLine("triage ", u))
	}
	return strings.Join(lines, "\n")
}

func usageLine(label string, u model.Usage) string {
	if u.EstimatedUSD > 0 {
		return fmt.Sprintf("<i>%s%s in · %s out · ~$%.3f</i>",
			label, thousands(u.InputTokens), thousands(u.OutputTokens), u.EstimatedUSD)
	}
	return fmt.Sprintf("<i>%s%s in · %s out</i>", label, thousands(u.InputTokens), thousands(u.OutputTokens))
}

// paragraphs splits model prose on blank lines and escapes each part. The model
// is told to write plain prose, but anything it does emit is escaped rather
// than trusted as markup.
func paragraphs(body string) []string {
	var out []string
	for _, p := range strings.Split(body, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, emphasizeLabel(bullets(escape(p))))
		}
	}
	return out
}

// labelSeparators are what the model may put between a block's topic label and
// its first sentence. It is asked for " - "; a hyphen is routinely typeset as a
// dash on the way out, so both are accepted.
var labelSeparators = []string{" - ", " — ", " – "}

// maxLabelRunes bounds what will be treated as a label. Past this, a match is
// far more likely to be a dash inside an ordinary sentence.
const maxLabelRunes = 40

// emphasizeLabel bolds a block's leading topic label so the reader can skim
// down the labels alone and stop where it matters. A block that does not
// clearly carry one is left exactly as written -- guessing wrong would bold a
// fragment of a sentence, which reads worse than no label at all.
func emphasizeLabel(block string) string {
	first, rest, _ := strings.Cut(block, "\n")
	if rest != "" {
		rest = "\n" + rest
	}
	if strings.HasPrefix(first, "• ") {
		return block // list items carry their own structure
	}

	for _, sep := range labelSeparators {
		label, tail, found := strings.Cut(first, sep)
		if !found || !isLabel(label) {
			continue
		}
		return "<b>" + label + "</b>" + sep + tail + rest
	}
	return block
}

// maxLabelWords bounds a label. Four covers "Treasury and the yen"; five was
// enough to swallow "The Dow fell 350 points".
const maxLabelWords = 4

// determiners open a clause, not a topic. A label names its subject directly.
var determiners = map[string]bool{
	"the": true, "a": true, "an": true, "this": true, "that": true,
	"these": true, "those": true, "it": true, "its": true, "they": true,
	"their": true, "we": true, "he": true, "his": true, "she": true,
	"her": true, "there": true,
}

// isLabel reports whether a fragment reads as a topic label rather than the
// opening clause of a sentence. It errs towards saying no: a missed label just
// goes unbolded, while a false positive bolds half a sentence.
func isLabel(s string) bool {
	if s == "" || runeLen(s) > maxLabelRunes {
		return false
	}
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > maxLabelWords {
		return false
	}
	// Sentence punctuation means the dash was joining clauses, not labelling.
	if strings.ContainsAny(s, ".!?:;,") {
		return false
	}
	// Digits belong to figures, and a figure means a sentence is under way.
	if strings.ContainsAny(s, "0123456789") {
		return false
	}
	return !determiners[strings.ToLower(fields[0])]
}

// bullets rewrites the model's "- " list markers as real bullets. Telegram has
// no list rendering, so the character is the only thing distinguishing a list
// from a run-on paragraph.
func bullets(paragraph string) string {
	lines := strings.Split(paragraph, "\n")

	var out []string
	previousWasBullet := false
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		isBullet := strings.HasPrefix(trimmed, "- ")

		// A blank line between bullets. Stacked flush against each other they
		// read as a paragraph with odd punctuation; the gap is what makes a list
		// scannable on a phone, and the model cannot supply it because a blank
		// line is how it separates one block from the next.
		if isBullet && previousWasBullet {
			out = append(out, "")
		}
		if isBullet {
			out = append(out, "• "+emphasizeBulletLabel(strings.TrimSpace(trimmed[2:])))
		} else {
			out = append(out, line)
		}
		previousWasBullet = isBullet
	}
	return strings.Join(out, "\n")
}

// pack lays segments out across messages.
//
// A segment that fits in the space left goes there whole. One that does not
// starts a fresh message rather than being cut at whatever paragraph reaches
// the limit, so breaks fall between sections. Only a segment too large for any
// single message is split, and then between blocks, with its heading kept
// attached to the first of them so a heading never ends a message alone.
func pack(segs []segment) []string {
	var (
		messages []string
		current  strings.Builder
	)
	flush := func() {
		if current.Len() > 0 {
			messages = append(messages, current.String())
			current.Reset()
		}
	}
	// A message boundary already separates what is above from what is below, so
	// a divider opening a message would be a rule drawn under nothing.
	opening := func(piece string) string {
		return strings.TrimPrefix(piece, divider+"\n")
	}
	fits := func(piece string) bool {
		if current.Len() == 0 {
			return runeLen(opening(piece)) <= maxMessageRunes
		}
		// +2 for the blank line joining blocks within a message.
		return runeLen(current.String())+2+runeLen(piece) <= maxMessageRunes
	}
	add := func(piece string) {
		if current.Len() == 0 {
			current.WriteString(opening(piece))
			return
		}
		current.WriteString("\n\n")
		current.WriteString(piece)
	}

	for _, seg := range segs {
		if len(seg.blocks) == 0 {
			continue
		}
		if seg.newMessage {
			flush()
		}

		whole := strings.Join(seg.blocks, "\n\n")
		if fits(whole) {
			add(whole)
			continue
		}
		flush()
		if fits(whole) {
			add(whole)
			continue
		}

		// Too large for one message. The heading travels with the first block
		// after it; everything else breaks between blocks.
		units := seg.blocks
		if len(units) >= 2 {
			units = append([]string{units[0] + "\n\n" + units[1]}, units[2:]...)
		}
		for _, unit := range units {
			for _, piece := range splitOversized(unit) {
				if !fits(piece) {
					flush()
				}
				add(piece)
			}
		}
	}
	flush()

	if len(messages) == 0 {
		return nil
	}
	return messages
}

// splitOversized breaks a single block that cannot fit in one message. It
// splits on spaces, which never fall inside an HTML entity or a URL, so the
// escaped text stays intact.
func splitOversized(block string) []string {
	if runeLen(block) <= maxMessageRunes {
		return []string{block}
	}

	var (
		out     []string
		current strings.Builder
	)
	for _, word := range strings.Fields(block) {
		if current.Len() > 0 && runeLen(current.String())+runeLen(word)+1 > maxMessageRunes {
			out = append(out, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(word)
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// thousands groups a token count so 128000 reads as 128,000 at a glance.
func thousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// escape covers the characters Telegram's HTML parse mode treats as markup.
// Quotes are included because the same helper fills href attributes.
var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escape(s string) string { return escaper.Replace(s) }

func runeLen(s string) int { return len([]rune(s)) }

// RenderPlain lays out a heading and a block of model prose as messages.
//
// It exists for replies that are not the daily brief -- a company analysis, say
// -- which still need the same escaping, paragraph handling and message
// splitting, and would otherwise be truncated by Telegram at 4096 characters.
func RenderPlain(heading, body string) []string {
	segs := []segment{}
	if heading != "" {
		segs = append(segs, segment{blocks: []string{"<b>" + escape(heading) + "</b>"}})
	}

	// A section's heading and the bullets under it are one unit. Treating every
	// paragraph as its own segment let a message end on "THE CASE AGAINST IT"
	// with the case itself opening the next one, which reads as though the
	// analysis had been cut off.
	var current *segment
	for _, p := range paragraphs(body) {
		if isSectionHeading(p) {
			if current != nil {
				segs = append(segs, *current)
			}
			current = &segment{blocks: []string{"<b>" + p + "</b>"}}
			continue
		}
		if current == nil {
			current = &segment{}
		}
		current.blocks = append(current.blocks, p)
	}
	if current != nil {
		segs = append(segs, *current)
	}

	if len(segs) == 0 {
		return nil
	}
	return pack(segs)
}

// isSectionHeading recognizes the capitalised lines the analysis divides itself
// with -- "THE CASE AGAINST IT", "WHAT IT OWNS AND OWES".
//
// Capitals alone are the test, because that is the only thing that separates a
// heading from a line of prose here: no markdown is allowed through, and a
// bullet or a sentence carries lower-case letters within a few words.
func isSectionHeading(block string) bool {
	const maxHeadingRunes = 60
	if strings.Contains(block, "\n") || runeLen(block) > maxHeadingRunes {
		return false
	}

	letters := 0
	for _, r := range block {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= 'A' && r <= 'Z':
			letters++
		}
	}
	return letters >= 3
}

// citation matches the "[12]" and "[12][15]" the brief cites its sources with.
var citation = regexp.MustCompile(`\[(\d{1,4})\]`)

// linkCitations turns each citation into a link to the article it points at.
//
// A source list at the end answers "where did this come from" only for someone
// willing to hunt; a number beside the claim answers it in one tap. A number
// with no article behind it is left as written rather than linked to something
// else, so a miscount cannot silently attribute a claim to the wrong outlet.
func linkCitations(block string, cited []model.Article) string {
	if len(cited) == 0 {
		return block
	}
	return citation.ReplaceAllStringFunc(block, func(match string) string {
		n, err := strconv.Atoi(strings.Trim(match, "[]"))
		if err != nil || n < 1 || n > len(cited) {
			return match
		}
		a := cited[n-1]
		if a.URL == "" {
			return match
		}
		return fmt.Sprintf("<a href=\"%s\">[%d]</a>", escape(a.URL), n)
	})
}

// cite links the citations in every block of a section.
func cite(blocks []string, cited []model.Article) []string {
	for i, b := range blocks {
		blocks[i] = linkCitations(b, cited)
	}
	return blocks
}

// renderCandidates lists the companies the news kept mentioning that no
// watchlist tracks.
//
// Every line carries the evidence with it -- what happened, which outlets, how
// many days it has been running -- because the reader has to be able to dismiss
// a name as quickly as they can act on one. There is no ranking and no verdict:
// a heading that said "opportunities" would be claiming something the brief
// cannot know.
func renderCandidates(candidates []model.Candidate, cited []model.Article) []string {
	if len(candidates) == 0 {
		return nil
	}

	blocks := []string{divider + "\n<b>New names in the news</b>\n" +
		"<i>Companies today's stories were about that none of your watchlists track. Not recommendations.</i>"}

	for _, c := range candidates {
		var b strings.Builder
		b.WriteString("• <b>" + escape(c.Name) + "</b>")
		switch {
		case c.Symbol() != "":
			b.WriteString(" <code>" + escape(c.Symbol()) + "</code>")
		case c.Private:
			b.WriteString(" <i>private</i>")
		default:
			b.WriteString(" <i>ticker unverified</i>")
		}
		b.WriteString("\n" + escape(c.Why))

		var notes []string
		if c.Days > 1 {
			notes = append(notes, fmt.Sprintf("%d days running", c.Days))
		}
		if c.Sources > 1 {
			notes = append(notes, fmt.Sprintf("%d outlets", c.Sources))
		}
		if len(notes) > 0 {
			b.WriteString(" <i>(" + escape(strings.Join(notes, ", ")) + ")</i>")
		}

		// The articles behind it, as citations, so the claim can be checked in
		// one tap exactly as the rest of the brief can.
		for _, a := range c.Articles {
			if n := citationNumber(a, cited); n > 0 {
				b.WriteString(fmt.Sprintf(" <a href=\"%s\">[%d]</a>", escape(a.URL), n))
			}
		}
		blocks = append(blocks, b.String())
	}
	return blocks
}

// citationNumber finds an article's number in the brief's numbering, so a
// candidate cites the same article the prose does.
func citationNumber(a model.Article, cited []model.Article) int {
	for i, c := range cited {
		if c.ID == a.ID {
			return i + 1
		}
	}
	return 0
}

// emphasizeBulletLabel bolds the few words a bullet opens with, where it opens
// with a label at all.
//
// A screen of bullets that all begin with prose reads as one block; the same
// bullets with "Gross margin —" in bold can be skimmed for the one that
// matters. The same caution applies as for a paragraph label: a fragment of a
// sentence in bold reads worse than none, so anything that is not plainly a
// label is left exactly as written.
func emphasizeBulletLabel(bullet string) string {
	for _, sep := range []string{" - ", " — ", " – ", ": "} {
		label, rest, found := strings.Cut(bullet, sep)
		if !found || !isLabel(label) {
			continue
		}
		return "<b>" + label + "</b>" + sep + rest
	}
	return bullet
}
