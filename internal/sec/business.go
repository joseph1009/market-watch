package sec

import (
	"regexp"
	"strings"
)

// Annual reports are HTML written by filing software, not by anyone expecting
// it to be read as markup: tables of financial statements, inline styles, and
// the business description somewhere among them. What follows is deliberately
// crude -- strip the tags, find the heading, take what is between it and the
// next one. A parser that understood the document would be no more accurate,
// because the documents do not agree with each other on structure.

var (
	// script and style hold no prose and plenty of braces that survive tag
	// stripping as gibberish.
	scriptOrStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)

	tag = regexp.MustCompile(`(?s)<[^>]*>`)

	// Filers write "Item 1." or "ITEM 1 -" or "Item 1: Business", with the
	// number sometimes followed by a period and sometimes not.
	businessHeading = regexp.MustCompile(`(?i)item\s*1\s*[.:\-–—]?\s*business`)

	// Item 1A is risk factors, which is where the business description ends.
	// Item 2 is properties, for the filers that carry no risk factors.
	nextHeading = regexp.MustCompile(`(?i)item\s*(1A\s*[.:\-–—]?\s*risk|2\s*[.:\-–—]?\s*propert)`)

	whitespace = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// businessText extracts the business description from an annual report.
//
// The table of contents is the trap: it carries the same headings as the
// document, so the first match is usually a line of dots and a page number. The
// section is therefore taken from the last heading that has substantial text
// after it, not the first one found.
func businessText(document string, maxRunes int) string {
	text := plainText(document)

	spans := businessHeading.FindAllStringIndex(text, -1)
	if len(spans) == 0 {
		return ""
	}

	best := ""
	for _, span := range spans {
		section := text[span[1]:]
		if end := nextHeading.FindStringIndex(section); end != nil {
			section = section[:end[0]]
		}
		section = strings.TrimSpace(section)
		if len(section) > len(best) {
			best = section
		}
	}

	// A contents entry yields a few characters; a real section yields
	// thousands. Anything in between is not worth showing as a description.
	const minUsefulRunes = 400
	if len([]rune(best)) < minUsefulRunes {
		return ""
	}

	r := []rune(best)
	if maxRunes > 0 && len(r) > maxRunes {
		// Cut at a sentence end where one is near, so the extract does not stop
		// mid-word.
		cut := string(r[:maxRunes])
		if stop := strings.LastIndex(cut, ". "); stop > maxRunes/2 {
			cut = cut[:stop+1]
		}
		return cut + "\n\n[Business description truncated here.]"
	}
	return best
}

// plainText turns filing HTML into readable prose.
func plainText(document string) string {
	text := scriptOrStyle.ReplaceAllString(document, " ")

	// Block elements become line breaks, so sentences do not run together.
	text = regexp.MustCompile(`(?i)</(p|div|tr|h[1-6]|li|table)>`).ReplaceAllString(text, "\n")
	text = regexp.MustCompile(`(?i)<br[^>]*>`).ReplaceAllString(text, "\n")

	text = tag.ReplaceAllString(text, " ")
	text = entities.Replace(text)
	text = whitespace.ReplaceAllString(text, " ")
	text = blankLines.ReplaceAllString(text, "\n\n")

	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// entities covers what filing software actually emits. A full table would add
// nothing: these documents are written by a handful of tools.
var entities = strings.NewReplacer(
	"&nbsp;", " ", "&#160;", " ", "&amp;", "&", "&#38;", "&",
	"&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&apos;", "'",
	"&#8217;", "'", "&#8216;", "'", "&#8220;", `"`, "&#8221;", `"`,
	"&#8211;", "-", "&#8212;", "--", "&mdash;", "--", "&ndash;", "-",
	"&#149;", "*", "&bull;", "*", "&#8226;", "*",
)
