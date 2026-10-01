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

	// Terms are the glossary's jargon, each linked to its explanation the
	// first time it appears in a section.
	Terms []model.Term
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
			[]string{divider + "\n<b>OVERVIEW</b>"}, linkTerms(cite(paragraphs(rep.Overview), rep.Cited), opts.Terms)...)})
	}

	// What is due comes straight after the overview: the reader has just
	// read what happened, and this is what to watch next.
	if blocks := renderCalendar(rep.Calendar, rep.GeneratedAt, display); len(blocks) > 0 {
		segs = append(segs, segment{blocks: blocks})
	}

	// Every category's prose runs uninterrupted, and the links follow at the
	// end. Interleaving a source list after each section broke the reading into
	// prose, links, prose, links -- the reader had to skip past a list of
	// headlines to reach the next piece of analysis.
	for _, s := range rep.Sections {
		// In capitals, to stand above the bold sub-headings inside it.
		title := strings.ToUpper(s.GroupName)
		if s.Emoji != "" {
			title = s.Emoji + " " + title
		}
		heading := fmt.Sprintf("%s\n<b>%s</b>", divider, escape(title))
		// The biggest moves sit under the heading, in the same block so a
		// message break never parts them: what the exchange did, before what
		// was written about it.
		if len(s.Movers) > 0 {
			heading += "\n<i>Biggest moves: " + escape(model.Moves(s.Movers)) + "</i>"
		}
		blocks := []string{heading}
		if s.Body != "" {
			blocks = append(blocks, linkTerms(cite(paragraphs(s.Body), rep.Cited), opts.Terms)...)
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
	return fmt.Sprintf("<b>QUIET TODAY</b>\n%s — nothing that warranted a section.",
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

// renderFooter reports the run's size: how many articles, and how much text went
// to and from the models. Tokens rather than dollars, since the calls are
// answered through a subscription that is not billed by the token.
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
	return fmt.Sprintf("<i>%s%s in · %s out</i>", label, thousands(u.InputTokens), thousands(u.OutputTokens))
}

// paragraphs splits model prose on blank lines and escapes each part. The model
// is told to write plain prose, but anything it does emit is escaped rather
// than trusted as markup.
func paragraphs(body string) []string {
	var out []string
	pending := "" // a sub-heading written with a blank line under it
	for _, p := range strings.Split(body, "\n\n") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		// A sub-heading belongs to the bullets under it, so a message break
		// can never fall between them.
		if strings.HasPrefix(p, subheadingMarker) && !strings.Contains(p, "\n") {
			pending += p + "\n"
			continue
		}
		out = append(out, emphasizeLabel(bullets(escape(pending+p))))
		pending = ""
	}
	if pending != "" {
		out = append(out, bullets(escape(strings.TrimSpace(pending))))
	}
	return out
}

// subheadingMarker opens a sub-heading in the brief and the analysis: a topic
// of a few words over the bullets that belong to it.
const subheadingMarker = "### "

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
	if strings.HasPrefix(first, "• ") || strings.HasPrefix(first, "<b>") {
		return block // list items and sub-headings carry their own structure
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
		switch group := strings.TrimSpace(line); {
		case isBullet:
			out = append(out, "• "+emphasizeBulletLabel(highlight(strings.TrimSpace(trimmed[2:]))))
		case strings.HasPrefix(group, subheadingMarker):
			if previousWasBullet {
				out = append(out, "")
			}
			out = append(out, "<b>"+strings.ReplaceAll(strings.TrimSpace(group[len(subheadingMarker):]), "**", "")+"</b>")
		case groupLabels[group]:
			if previousWasBullet {
				out = append(out, "")
			}
			out = append(out, "<b>"+group+"</b>")
		default:
			out = append(out, line)
		}
		previousWasBullet = isBullet
	}
	return strings.Join(out, "\n")
}

// highlights are the figure or short phrase the writer marks as the one that
// matters most in a bullet, as **5.55%**.
var highlights = regexp.MustCompile(`\*\*([^*\n]{1,80}?)\*\*`)

// highlight bolds what the writer marked, and drops a stray marker that
// closes nothing, so no asterisks reach the reader.
func highlight(line string) string {
	return strings.ReplaceAll(highlights.ReplaceAllString(line, "<b>$1</b>"), "**", "")
}

// groupLabels divide the analysis's case for and case against into the
// business and the figures, as the analysis wrote them before it had
// sub-headings; a reply written that way still reads. They are bolded by name
// rather than by shape: a short line ending in a colon is also how the brief
// introduces a list, and that line is not a heading.
var groupLabels = map[string]bool{
	"In the business:": true,
	"In the numbers:":  true,
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
	// a rule opening a message -- the divider, or the closer look's shorter one
	// between companies -- would be drawn under nothing.
	opening := func(piece string) string {
		first, rest, ok := strings.Cut(piece, "\n")
		if ok && first != "" && strings.Trim(first, "─") == "" {
			return rest
		}
		return piece
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

// Escape is the same, for the places outside this package that build a message
// of their own. /stats now quotes headlines straight from the feeds, and a
// headline with an ampersand in it would otherwise be rejected by Telegram as
// broken markup rather than shown.
func Escape(s string) string { return escape(s) }

func runeLen(s string) int { return len([]rune(s)) }

// RenderPlain lays out a heading and a block of model prose as messages.
//
// It exists for replies that are not the daily brief -- a company analysis, say
// -- which still need the same escaping, paragraph handling and message
// splitting, and would otherwise be truncated by Telegram at 4096 characters.
func RenderPlain(heading, body string) []string {
	segs := headingSegments(heading)
	segs = append(segs, plainSegments(body)...)
	if len(segs) == 0 {
		return nil
	}
	return pack(segs)
}

func headingSegments(heading string) []segment {
	if heading == "" {
		return nil
	}
	return []segment{{blocks: []string{"<b>" + escape(heading) + "</b>"}}}
}

// plainSegments are the prose's sections, each with the paragraphs under it.
func plainSegments(body string) []segment {
	var segs []segment

	// A section's heading and the bullets under it are one unit. Treating every
	// paragraph as its own segment let a message end on "THE CASE AGAINST IT"
	// with the case itself opening the next one, which reads as though the
	// analysis had been cut off.
	var current *segment
	for _, p := range paragraphs(body) {
		// A heading written straight onto its first sub-heading or bullet,
		// with no blank line between, is still a heading.
		heading, rest, _ := strings.Cut(p, "\n")
		if isSectionHeading(heading) {
			if current != nil {
				segs = append(segs, *current)
			}
			// Ruled off like the brief's sections, so each stands apart from
			// the bold sub-headings inside the one before.
			current = &segment{blocks: []string{divider + "\n<b>" + heading + "</b>"}}
			if rest = strings.TrimSpace(rest); rest != "" {
				current.blocks = append(current.blocks, rest)
			}
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
	return segs
}

// AnalysisVerdict is the view on the stock an analysis ends with: BUY, HOLD
// or SELL, a confidence, and the bullets saying why.
type AnalysisVerdict struct {
	Verdict, Confidence, Body string
}

// The notes under an analysis's verdict. As with the closer look, the
// channel's is not decoration: a verdict posted to other people must say that
// a model wrote it, that nobody checked it, and that it is not advice to act
// on. /share is the only way an analysis reaches the channel, and it posts
// the channel's copy. Since 2026-10-01 both are the closer look's one line,
// without its first clause: an analysis is of a company the owner chose.
const (
	analysisOwnerNote = "<i>" + verdictMeaning + "</i>"

	analysisChannelNote = "<i>" + verdictMeaning + " " + disclosure + "</i>"
)

// RenderAnalysis lays out /analyse: the heading, then the verdict, then the
// accounts that support it. The verdict is written last and shown first:
// last, so the reading of the accounts is not bent to fit a conclusion
// reached before it; first, because it is what a reader looks for.
func RenderAnalysis(heading string, v AnalysisVerdict, prose string, opts IdeasOptions) []string {
	segs := headingSegments(heading)
	if v.Verdict != "" {
		note := analysisOwnerNote
		if opts.ForChannel {
			note = analysisChannelNote
		}
		line := "<b>" + escape(v.Verdict) + "</b>"
		if mark := verdictMarks[v.Verdict]; mark != "" {
			line = mark + " " + line
		}
		if v.Confidence != "" {
			line += " · " + escape(v.Confidence) + " confidence"
		}
		blocks := []string{divider + "\n<b>" + VerdictHeading + "</b>\n" + line + "\n" + note}
		blocks = append(blocks, paragraphs(v.Body)...)
		segs = append(segs, segment{blocks: blocks})
	}
	segs = append(segs, plainSegments(prose)...)
	if len(segs) == 0 {
		return nil
	}
	return pack(segs)
}

// VerdictHeading is what the analysis's verdict is shown under.
const VerdictHeading = "THE VERDICT"

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

	blocks := []string{divider + "\n<b>NEW NAMES IN THE NEWS</b>\n" +
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
		// The move, not the level. A percentage needs no currency beside it,
		// which a Hong Kong price does, and the move is the part the news
		// explains. A name with no price simply goes without one.
		if c.Quote != nil {
			b.WriteString(" · " + escape(c.Quote.Move()) + " last session")
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
