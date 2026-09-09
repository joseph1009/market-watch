package feed

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>CNBC Markets</title>
    <item>
      <title>Chipmakers rally on AI demand</title>
      <link>https://www.example.com/story-1?utm_source=rss</link>
      <description>&lt;p&gt;Shares of &lt;b&gt;NVDA&lt;/b&gt; climbed 4%.&lt;/p&gt;</description>
      <pubDate>Tue, 09 Sep 2026 14:30:00 -0400</pubDate>
    </item>
    <item>
      <title>Fed holds rates steady</title>
      <link>https://www.example.com/story-2</link>
      <description>Policymakers left the target range unchanged.</description>
      <pubDate>Tue, 09 Sep 2026 18:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

const atomSample = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>SEC EDGAR 8-K</title>
  <entry>
    <title>ACME CORP (0000123456) 8-K</title>
    <link rel="self" href="https://www.sec.gov/self/feed"/>
    <link rel="alternate" type="text/html" href="https://www.sec.gov/filing/123"/>
    <summary type="html">Item 2.02 Results of Operations</summary>
    <updated>2026-09-09T18:12:04-04:00</updated>
  </entry>
</feed>`

func TestParseReadsRSS(t *testing.T) {
	items, err := Parse(strings.NewReader(rssSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.Title != "Chipmakers rally on AI demand" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://www.example.com/story-1?utm_source=rss" {
		t.Errorf("URL = %q", first.URL)
	}
	// The description arrives double-escaped, so the decode leaves real tags.
	if want := "Shares of NVDA climbed 4%."; first.Summary != want {
		t.Errorf("Summary = %q, want %q", first.Summary, want)
	}
	if want := time.Date(2026, 9, 9, 18, 30, 0, 0, time.UTC); !first.Published.Equal(want) {
		t.Errorf("Published = %s, want %s", first.Published, want)
	}
}

func TestParseReadsAtomAndPrefersTheAlternateLink(t *testing.T) {
	items, err := Parse(strings.NewReader(atomSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	// "self" points back at the feed; only "alternate" is the story.
	if want := "https://www.sec.gov/filing/123"; items[0].URL != want {
		t.Errorf("URL = %q, want %q", items[0].URL, want)
	}
	if want := time.Date(2026, 9, 9, 22, 12, 4, 0, time.UTC); !items[0].Published.Equal(want) {
		t.Errorf("Published = %s, want %s", items[0].Published, want)
	}
}

// A feed that trips the strict XML rules must still yield its items: losing the
// document costs a source for the whole day.
func TestParseToleratesBareAmpersandsAndUnknownEntities(t *testing.T) {
	const messy = `<rss><channel>
	  <item>
	    <title>Procter &amp; Gamble &nbsp; beats &mdash; guidance raised</title>
	    <link>https://example.com/pg</link>
	    <description>Sales rose 3% &amp;amp; margins widened&hellip;</description>
	  </item>
	</channel></rss>`

	items, err := Parse(strings.NewReader(messy))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.HasPrefix(items[0].Title, "Procter & Gamble") {
		t.Errorf("Title = %q", items[0].Title)
	}
	if !strings.Contains(items[0].Summary, "3% & margins") {
		t.Errorf("Summary = %q, want the double-escaped ampersand resolved", items[0].Summary)
	}
}

// A "--" inside a comment is illegal XML and Go enforces it even with strict
// mode off. Intel's newsroom feed carries one, and it cost the whole source.
func TestParseSurvivesAMalformedComment(t *testing.T) {
	const feed = `<rss><channel>
	  <!-- build id 4821 -- generated nightly -->
	  <item><title>Intel ships a new process</title><link>https://example.com/i</link></item>
	</channel></rss>`

	items, err := Parse(strings.NewReader(feed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Intel ships a new process" {
		t.Errorf("got %+v, want the item recovered", items)
	}
}

// Comment markers inside CDATA are prose, not markup, and must survive the
// retry that strips real comments.
func TestParseKeepsCommentMarkersInsideCDATA(t *testing.T) {
	const feed = `<rss><channel>
	  <!-- bad -- comment forces the retry path -->
	  <item>
	    <title>Markup explained</title>
	    <link>https://example.com/c</link>
	    <description><![CDATA[Use <!-- like this --> to comment]]></description>
	  </item>
	</channel></rss>`

	items, err := Parse(strings.NewReader(feed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.Contains(items[0].Summary, "to comment") {
		t.Errorf("Summary = %q, want the CDATA text preserved", items[0].Summary)
	}
}

// newsroom.intel.com/feed/ serves a web page. The XML error it produced named
// a stray "--" in the page's own markup, which reads as a parser bug and sent a
// debugging session in the wrong direction.
func TestParseNamesAnHTMLPageForWhatItIs(t *testing.T) {
	const page = `<!doctype html>
<html lang="en"><head><title>Newsroom</title></head>
<body><!-- nav -- section --><div class="wrap">Not a feed</div></body></html>`

	_, err := Parse(strings.NewReader(page))
	if !errors.Is(err, ErrNotAFeed) {
		t.Errorf("err = %v, want ErrNotAFeed", err)
	}
}

// A real feed served as HTML-ish markup must not be mistaken for a web page.
func TestParseDoesNotMistakeAFeedForAPage(t *testing.T) {
	const feed = `<?xml version="1.0"?><rss version="2.0"><channel>
	  <item><title>Real</title><link>https://example.com/r</link></item>
	</channel></rss>`

	items, err := Parse(strings.NewReader(feed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items, want 1", len(items))
	}
}

func TestStripCommentsReportsWhetherItChangedAnything(t *testing.T) {
	if _, changed := stripComments([]byte("<rss><channel></channel></rss>")); changed {
		t.Error("reported a change on a document with no comments")
	}
	got, changed := stripComments([]byte("<a><!-- x --><b>keep</b></a>"))
	if !changed {
		t.Error("did not report stripping a comment")
	}
	if want := "<a><b>keep</b></a>"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseMissingDateLeavesPublishedZero(t *testing.T) {
	// The fetcher substitutes its own clock; the parser must not invent one.
	const noDate = `<rss><channel><item>
	  <title>Undated</title><link>https://example.com/x</link>
	</item></channel></rss>`

	items, err := Parse(strings.NewReader(noDate))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !items[0].Published.IsZero() {
		t.Errorf("Published = %s, want the zero time", items[0].Published)
	}
}

func TestParseDecodesLatin1(t *testing.T) {
	// 0xE9 is 'é' in ISO-8859-1. Without a charset reader the decoder rejects
	// the document outright.
	raw := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><item>" +
		"<title>Soci\xe9t\xe9 G\xe9n\xe9rale</title><link>https://example.com/sg</link>" +
		"</item></channel></rss>"

	items, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := "Société Générale"; items[0].Title != want {
		t.Errorf("Title = %q, want %q", items[0].Title, want)
	}
}

func TestStripHTMLKeepsWordBoundaries(t *testing.T) {
	tests := map[string]string{
		"<p>One</p><p>Two</p>":       "One Two",
		"a<br/>b":                    "a b",
		"plain text":                 "plain text",
		"3 > 2 is true":              "3 > 2 is true",
		"<a href='#'>link</a> after": "link after",
		"  spaced   \n  out  ":       "spaced out",
	}
	for in, want := range tests {
		if got := stripHTML(in); got != want {
			t.Errorf("stripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateCutsOnWordBoundary(t *testing.T) {
	got := truncate("the quick brown fox jumps over the lazy dog", 20)
	if strings.HasSuffix(got, "…") == false {
		t.Fatalf("truncate did not mark the cut: %q", got)
	}
	if strings.Contains(got, "jump") {
		t.Errorf("truncate(%q) kept a partial word: %q", "…jumps…", got)
	}
	if unchanged := truncate("short", 20); unchanged != "short" {
		t.Errorf("truncate shortened a string under the limit: %q", unchanged)
	}
}
