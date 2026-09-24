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

// A 20-F has no Item 1 business: the description is part B of Item 4. The
// filing refers to that part by name all through its risk factors, and those
// references, like the contents entry, must not be taken for the section.
func TestBusinessSectionReadsATwentyFsBusinessOverview(t *testing.T) {
	doc := `<html><body>
	<p>ITEM 4.</p><p>INFORMATION ON THE COMPANY</p>
	<p>B. Business Overview</p><p>C. Organizational Structure</p>
	<p>ITEM 3. KEY INFORMATION</p>
	<p>For more, see "Item 4. Information on the Company -- B. Business Overview -- Regulation". ` +
		strings.Repeat("Regulation may change and hurt our results. ", 40) + `</p>
	<p>ITEM 4. INFORM ATION ON THE COMPANY</p>
	<p>A. History and Development of the Company</p>
	<p>We were incorporated in the Cayman Islands in 1999.</p>
	<p>B. Business Overview</p>
	<p>We run China's largest online marketplaces and a cloud business. ` +
		strings.Repeat("Merchants reach consumers through our platforms and pay us for marketing. ", 12) +
		`</p>
	<p>C. Organizational Structure</p>
	<p>The following diagram shows our subsidiaries.</p>
	</body></html>`

	got := businessText(doc, 0)
	if !strings.HasPrefix(got, "We run China's largest online marketplaces") {
		t.Fatalf("the business overview was not found:\n%s", got)
	}
	if strings.Contains(got, "Regulation may change") || strings.Contains(got, "Cayman Islands") {
		t.Errorf("took text from outside part B:\n%s", got)
	}
	if strings.Contains(got, "subsidiaries") {
		t.Errorf("the extract ran past the end of part B:\n%s", got)
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

// Taken from Micron's 10-K, where a hand-kept list of entities let these
// through as written.
func TestPlainTextDecodesEveryEntity(t *testing.T) {
	got := plainText(`<p>Micron&#174; and Crucial&#174; brands; the 1&#947; node rose 12&#37; &#8212; &#8220;HBM&#8221; &#149; DDR5&nbsp;memory</p>`)
	want := `Micron® and Crucial® brands; the 1γ node rose 12% -- "HBM" * DDR5 memory`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
