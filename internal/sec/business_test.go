package sec

import (
	"strings"
	"testing"
)

// The table of contents carries the same headings as the document, so the first
// match is usually a line of dots and a page number.
func TestBusinessSectionSkipsTheTableOfContents(t *testing.T) {
	doc := `<html><body>
	<table><tr><td>Item 1. Business</td><td>....</td><td>4</td></tr>
	<tr><td>Item 1A. Risk Factors</td><td>....</td><td>12</td></tr></table>
	<p>Item 1. Business</p>
	<p>We design and manufacture memory and storage products. ` +
		strings.Repeat("Our products are sold to data centre, mobile and automotive customers worldwide. ", 12) +
		`</p>
	<p>Item 1A. Risk Factors</p>
	<p>Our business is cyclical and subject to severe price swings.</p>
	</body></html>`

	got := businessText(doc, 0)
	if !strings.Contains(got, "design and manufacture memory") {
		t.Fatalf("the real section was not found:\n%s", got)
	}
	if strings.Contains(got, "Risk Factors") || strings.Contains(got, "cyclical") {
		t.Errorf("the extract ran past the end of Item 1:\n%s", got)
	}
	if strings.Contains(got, "....") {
		t.Errorf("the contents entry was taken instead of the section:\n%s", got)
	}
}

// A filer with no recognisable heading gets no description rather than a
// mangled one: the analysis says it is unavailable instead of inventing it.
func TestBusinessSectionReturnsNothingWhenItCannotFindIt(t *testing.T) {
	if got := businessText(`<html><body><p>Annual report of some company.</p></body></html>`, 0); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

// A contents entry alone is a few characters; a real section is thousands.
func TestBusinessSectionRejectsATooShortMatch(t *testing.T) {
	doc := `<p>Item 1. Business</p><p>See page 4.</p><p>Item 1A. Risk Factors</p>`
	if got := businessText(doc, 0); got != "" {
		t.Errorf("got %q, want nothing for a stub", got)
	}
}

func TestBusinessSectionTruncatesAtASentence(t *testing.T) {
	body := strings.Repeat("We sell memory chips to many customers. ", 200)
	doc := `<p>Item 1. Business</p><p>` + body + `</p><p>Item 1A. Risk Factors</p>`

	got := businessText(doc, 500)
	if !strings.Contains(got, "truncated") {
		t.Errorf("a truncated extract does not say so:\n%s", got)
	}
	if strings.Contains(got, "We sell memory chips to many custo\n") {
		t.Error("the extract stopped mid-word")
	}
}

func TestPlainTextStripsMarkupAndEntities(t *testing.T) {
	got := plainText(`<div style="x"><script>var a = 1;</script><p>Research &amp; development<br>rose 12&#37;</p></div>`)
	if strings.Contains(got, "<") || strings.Contains(got, "var a") {
		t.Errorf("markup survived: %q", got)
	}
	if !strings.Contains(got, "Research & development") {
		t.Errorf("entities were not decoded: %q", got)
	}
}
