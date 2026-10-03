package fundamentals

import (
	"reflect"
	"testing"
)

// The links come out of the text, wherever the model put them, and are
// listed once each at the end.
func TestTheLinksBecomeFootnotes(t *testing.T) {
	text := "WHAT THE NEWS SAYS\n\n" +
		"### Results\n" +
		"- CNBC [reported](https://www.cnbc.com/b) a beat on 30 September.\n" +
		"- Per the release (https://www.sec.gov/a), revenue rose.\n\n" +
		"### Sources checked\n" +
		"- [SEC 8-K release](https://www.sec.gov/a)\n" +
		"- https://www.fool.com/e\n\n" +
		"WHAT THE COMPANY EARNS\n- Revenue rose.\n\n" +
		"SOURCES\n" +
		"CNBC, 30 Sep 2026, results report|https://www.cnbc.com/c\n" +
		"- Reuters: https://www.reuters.com/d\n" +
		"- [CNBC](https://www.cnbc.com/b)\n"
	prose, sources := SplitSources(text)
	want := "WHAT THE NEWS SAYS\n\n" +
		"### Results\n" +
		"- CNBC reported a beat on 30 September.\n" +
		"- Per the release, revenue rose.\n\n" +
		"WHAT THE COMPANY EARNS\n- Revenue rose."
	if prose != want {
		t.Errorf("prose:\n%s\nwant:\n%s", prose, want)
	}
	wantSources := []Source{
		{"CNBC, 30 Sep 2026, results report", "https://www.cnbc.com/c"},
		{"Reuters", "https://www.reuters.com/d"},
		{"CNBC", "https://www.cnbc.com/b"},
		{"sec.gov", "https://www.sec.gov/a"},
		{"fool.com", "https://www.fool.com/e"},
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Errorf("sources = %+v\nwant %+v", sources, wantSources)
	}
}

// The text's citations follow the footnotes: renumbered from 1 in the
// list's order, merged where two numbers name one page, and dropped where no
// source stands behind them.
func TestTheCitationsAreRenumbered(t *testing.T) {
	text := "WHAT THE NEWS SAYS\n" +
		"- Sales rose [4].\n" +
		"- Prices fell [2, 7].\n" +
		"- Rivals are building [9].\n\n" +
		"SOURCES\n" +
		"4. CNBC, 30 Sep 2026, results report|https://www.cnbc.com/c\n" +
		"[7] Reuters, 1 Oct 2026, prices|https://www.reuters.com/d\n" +
		"2) Reuters again|https://www.reuters.com/d\n"
	prose, sources := SplitSources(text)
	want := "WHAT THE NEWS SAYS\n- Sales rose [1].\n- Prices fell [2].\n- Rivals are building."
	if prose != want {
		t.Errorf("prose:\n%s\nwant:\n%s", prose, want)
	}
	if len(sources) != 2 || sources[0].Title != "CNBC, 30 Sep 2026, results report" || sources[1].URL != "https://www.reuters.com/d" {
		t.Errorf("sources = %+v", sources)
	}

	// A list without numbers is read in its order.
	prose, _ = SplitSources("THE BUSINESS\n- Memory [1]. Storage [3].\n\nSOURCES\nCNBC|https://www.cnbc.com/c")
	if prose != "THE BUSINESS\n- Memory [1]. Storage." {
		t.Errorf("unnumbered: %q", prose)
	}
}

// Prose with no links comes back as it was.
func TestProseWithoutLinksIsLeftAlone(t *testing.T) {
	text := "THE BUSINESS\n\n### What it makes\n- Memory chips."
	if prose, sources := SplitSources(text); prose != text || sources != nil {
		t.Errorf("SplitSources = %q, %+v", prose, sources)
	}
}
