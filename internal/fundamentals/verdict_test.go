package fundamentals

import (
	"strings"
	"testing"
)

const analysedWithVerdict = `THE BUSINESS
- Makes memory.

WHAT WOULD SETTLE IT
- The next quarter's pricing.

THE VERDICT
VERDICT: BUY
CONFIDENCE: Medium

### Why
- 8.7 times next year's expected earnings, with revenue up 175%.

### What would change it
- Memory prices falling in the October quarter.`

func TestTheVerdictIsTakenOutOfTheProse(t *testing.T) {
	prose, v := SplitVerdict(analysedWithVerdict)
	if v.Verdict != "BUY" || v.Confidence != "medium" {
		t.Fatalf("verdict = %+v", v)
	}
	if !strings.HasPrefix(v.Body, "### Why") || !strings.Contains(v.Body, "Memory prices falling") {
		t.Errorf("body = %q", v.Body)
	}
	if strings.Contains(prose, "THE VERDICT") || strings.Contains(prose, "8.7 times") {
		t.Errorf("the verdict was left in the prose:\n%s", prose)
	}
	if !strings.HasSuffix(prose, "- The next quarter's pricing.") {
		t.Errorf("the prose lost its ending:\n%s", prose)
	}
}

// A section written in some other place, or with the heading dressed up,
// still comes out, and the prose around it stays in order.
func TestAVerdictBeforeAnotherSectionIsTakenOutAlone(t *testing.T) {
	text := "THE BUSINESS\n- Makes memory.\n\n**THE VERDICT**\n**VERDICT:** Sell.\nCONFIDENCE: low\n- Priced for perfection.\n\nCASH\n- Plenty."
	prose, v := SplitVerdict(text)
	if v.Verdict != "SELL" || v.Confidence != "low" || v.Body != "- Priced for perfection." {
		t.Fatalf("verdict = %+v", v)
	}
	if prose != "THE BUSINESS\n- Makes memory.\n\nCASH\n- Plenty." {
		t.Errorf("prose = %q", prose)
	}
}

// Without a verdict that reads as one, nothing is taken: a section headed
// THE VERDICT that argues without saying BUY, HOLD or SELL stays where the
// reader can see it.
func TestProseWithoutAVerdictIsLeftWhole(t *testing.T) {
	for _, text := range []string{
		"THE BUSINESS\n- Makes memory.",
		"THE VERDICT\nVERDICT: accumulate\n- Nice.",
	} {
		prose, v := SplitVerdict(text)
		if prose != text || v.Verdict != "" {
			t.Errorf("SplitVerdict(%q) = %q, %+v", text, prose, v)
		}
	}
}

// The terms are cut out, once each, even where a term in capitals looks like
// a heading, and the sources after them stay.
func TestTheTermsAreCutOut(t *testing.T) {
	text := "THE BUSINESS\n- HBM chips.\n\nIN SHORT\n### What it does\n- Memory.\n\nTERMS\n- forward P/E\nHBM\n- CoWoS | chip packaging\n- hbm\n\nSOURCES\n1. CNBC|https://www.cnbc.com/a"
	prose, terms := SplitTerms(text)
	if want := []string{"forward P/E", "HBM", "CoWoS"}; strings.Join(terms, ",") != strings.Join(want, ",") {
		t.Errorf("terms = %q", terms)
	}
	if prose != "THE BUSINESS\n- HBM chips.\n\nIN SHORT\n### What it does\n- Memory.\n\nSOURCES\n1. CNBC|https://www.cnbc.com/a" {
		t.Errorf("prose = %q", prose)
	}
	if prose, terms := SplitTerms("THE BUSINESS\n- Memory."); terms != nil || prose != "THE BUSINESS\n- Memory." {
		t.Errorf("without terms: %q, %q", prose, terms)
	}
}

// The model's word on its own work, before the first section, is dropped.
func TestThePreambleIsDropped(t *testing.T) {
	text := "Searches are done. I checked the results and am now writing the report.\n\nTHE BUSINESS\n- Memory chips."
	if got := TrimPreamble(text); got != "THE BUSINESS\n- Memory chips." {
		t.Errorf("TrimPreamble = %q", got)
	}
	if got := TrimPreamble("- No headings at all."); got != "- No headings at all." {
		t.Errorf("without a heading: %q", got)
	}
}

// The summary is cut out of the prose, wherever it falls, and the sections
// either side of it stay.
func TestTheShortSummaryIsCutOut(t *testing.T) {
	text := "THE BUSINESS\n- Memory chips.\n\nTHE VERDICT\nVERDICT: BUY\n\nIN SHORT\n### What it does\n- Memory.\n\nCOMPANIES TO READ\nx"
	prose, short := SplitShort(text)
	if short != "### What it does\n- Memory." {
		t.Errorf("short = %q", short)
	}
	if prose != "THE BUSINESS\n- Memory chips.\n\nTHE VERDICT\nVERDICT: BUY\n\nCOMPANIES TO READ\nx" {
		t.Errorf("prose = %q", prose)
	}
	if prose, short := SplitShort("THE BUSINESS\n- Memory chips."); short != "" || prose != "THE BUSINESS\n- Memory chips." {
		t.Errorf("without a summary: %q, %q", prose, short)
	}
}
