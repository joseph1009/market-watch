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
)

// Render turns a report into the messages to send, in order. Long reports are
// split across several messages because Telegram rejects anything longer.
func Render(rep model.Report, display *time.Location) []string {
	if display == nil {
		display = time.UTC
	}

	blocks := []string{
		fmt.Sprintf("<b>📊 Market Watch</b>\n%s", escape(rep.GeneratedAt.In(display).Format("Monday, 2 January 2006 · 15:04 MST"))),
	}

	if rep.Overview != "" {
		blocks = append(blocks, paragraphs(rep.Overview)...)
	}

	for _, s := range rep.Sections {
		blocks = append(blocks, fmt.Sprintf("<b>%s</b>", escape(s.GroupName)))
		if s.Body != "" {
			blocks = append(blocks, paragraphs(s.Body)...)
		}
		if links := renderLinks(s.Articles); links != "" {
			blocks = append(blocks, links)
		}
	}

	blocks = append(blocks, fmt.Sprintf("<i>%d articles from %d sources</i>", rep.ArticleCount, rep.SourceCount))

	return pack(blocks)
}

// renderLinks lists the stories behind a section so the reader can follow up.
func renderLinks(articles []model.Article) string {
	if len(articles) == 0 {
		return ""
	}

	shown := articles
	if len(shown) > maxLinksPerSection {
		shown = shown[:maxLinksPerSection]
	}

	var lines []string
	for _, a := range shown {
		lines = append(lines, fmt.Sprintf("• <a href=\"%s\">%s</a> — %s",
			escape(a.URL), escape(a.Title), escape(a.SourceName)))
	}
	if extra := len(articles) - len(shown); extra > 0 {
		lines = append(lines, fmt.Sprintf("<i>+%d more</i>", extra))
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
			out = append(out, escape(p))
		}
	}
	return out
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

// escape covers the characters Telegram's HTML parse mode treats as markup.
// Quotes are included because the same helper fills href attributes.
var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escape(s string) string { return escaper.Replace(s) }

func runeLen(s string) int { return len([]rune(s)) }
