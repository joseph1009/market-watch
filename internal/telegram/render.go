package telegram

import (
	"fmt"
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
	maxLinksPerSection = 5

	// divider separates sections. Telegram's HTML mode has no horizontal rule,
	// so the break has to be drawn.
	divider = "──────────"
)

// Render turns a report into the messages to send, in order. Long reports are
// split across several messages because Telegram rejects anything longer.
func Render(rep model.Report, display *time.Location) []string {
	if display == nil {
		display = time.UTC
	}

	blocks := []string{
		fmt.Sprintf("<b>📊 Market Watch</b>\n<i>%s</i>",
			escape(rep.GeneratedAt.In(display).Format("Monday, 2 January 2006 · 15:04 MST"))),
	}

	if rep.Overview != "" {
		blocks = append(blocks, divider+"\n<b>Overview</b>")
		blocks = append(blocks, paragraphs(rep.Overview)...)
	}

	for _, s := range rep.Sections {
		blocks = append(blocks, fmt.Sprintf("%s\n<b>%s</b>", divider, escape(s.GroupName)))
		if s.Body != "" {
			blocks = append(blocks, paragraphs(s.Body)...)
		}
		if links := renderSources(s.Articles); links != "" {
			blocks = append(blocks, links)
		}
	}

	blocks = append(blocks, divider+"\n"+renderFooter(rep))

	return pack(blocks)
}

// renderSources lists the stories behind a section. The heading matters: an
// unlabelled list of links and a bare "+3 more" reads as stray text.
func renderSources(articles []model.Article) string {
	if len(articles) == 0 {
		return ""
	}

	shown := articles
	if len(shown) > maxLinksPerSection {
		shown = shown[:maxLinksPerSection]
	}

	lines := []string{"<i>Sources</i>"}
	for _, a := range shown {
		lines = append(lines, fmt.Sprintf("• <a href=\"%s\">%s</a> — %s",
			escape(a.URL), escape(a.Title), escape(a.SourceName)))
	}
	if extra := len(articles) - len(shown); extra > 0 {
		lines = append(lines, fmt.Sprintf("<i>+%d more article(s) not listed</i>", extra))
	}
	return strings.Join(lines, "\n")
}

// renderFooter reports the run's size and what it cost. The cost is an
// estimate priced from a local table, never a balance -- the API reports what a
// request spent, not what the account has left.
func renderFooter(rep model.Report) string {
	lines := []string{fmt.Sprintf("<i>%d articles from %d sources</i>", rep.ArticleCount, rep.SourceCount)}

	if u := rep.Usage; u.Total() > 0 {
		line := fmt.Sprintf("<i>%s in · %s out</i>", thousands(u.InputTokens), thousands(u.OutputTokens))
		if u.EstimatedUSD > 0 {
			line = fmt.Sprintf("<i>%s in · %s out · ~$%.3f</i>",
				thousands(u.InputTokens), thousands(u.OutputTokens), u.EstimatedUSD)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "- ") {
			lines[i] = "• " + strings.TrimSpace(trimmed[2:])
		}
	}
	return strings.Join(lines, "\n")
}

// pack fits blocks into as few messages as possible without splitting a block
// across a message boundary, which would tear an anchor tag in half.
func pack(blocks []string) []string {
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

	for _, block := range blocks {
		for _, piece := range splitOversized(block) {
			// +2 for the blank line joining blocks within a message.
			if current.Len() > 0 && runeLen(current.String())+runeLen(piece)+2 > maxMessageRunes {
				flush()
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(piece)
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
