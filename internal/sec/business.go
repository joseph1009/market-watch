package sec

import (
	"html"
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

	// A foreign filer's 20-F has no Item 1 business. Its description is part B
	// of Item 4, "Business Overview", which part C, the organizational
	// structure, follows. These match whole lines only: the 20-F refers to
	// "Item 4. Information on the Company -- B. Business Overview" dozens of
	// times in running text, and Alibaba's own Item 4 heading came through as
	// "INFORM ATION", split by the markup, so the part headings are the
	// dependable marks.
	overviewHeading  = regexp.MustCompile(`(?im)^(item\s*)?(4\.?\s*)?B\.?\s*business\s+overview\s*$`)
	structureHeading = regexp.MustCompile(`(?im)^(item\s*)?(4\.?\s*)?C\.?\s*organi[sz]ational\s+structure\s*$`)

	whitespace = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// businessText extracts the business description from an annual report: Item
// 1 of a 10-K, or failing that the business overview in Item 4 of a 20-F.
func businessText(document string, maxRunes int) string {
	text := plainText(document)

	// A contents entry yields a few characters; a real section yields
	// thousands. Anything in between is not worth showing as a description.
	const minUsefulRunes = 400
	best := longestSection(text, businessHeading, nextHeading)
	if len([]rune(best)) < minUsefulRunes {
		best = longestSection(text, overviewHeading, structureHeading)
	}
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

// longestSection returns the text between a start heading and the next end
// heading, from whichever start has the most text after it.
//
// The table of contents is the trap: it carries the same headings as the
// document, so the first match is usually a line of dots and a page number. The
// section is therefore taken from the heading with substantial text after it,
// not the first one found.
func longestSection(text string, start, end *regexp.Regexp) string {
	best := ""
	for _, span := range start.FindAllStringIndex(text, -1) {
		section := text[span[1]:]
		if stop := end.FindStringIndex(section); stop != nil {
			section = section[:stop[0]]
		}
		section = strings.TrimSpace(section)
		if len(section) > len(best) {
			best = section
		}
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
	text = html.UnescapeString(text)
	text = typography.Replace(text)
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

// typography flattens the punctuation filing software dresses prose in, once
// the entities are decoded, so the description reads like the rest of the
// prompt. Decoding itself is left to the full table: a short list of the
// entities these tools emit turned out to miss Micron's "&#174;" and its
// "1&#947;" process node, which reached the model as written.
var typography = strings.NewReplacer(
	"’", "'", "‘", "'", "“", `"`, "”", `"`,
	"–", "-", "—", "--", "•", "*",
)
