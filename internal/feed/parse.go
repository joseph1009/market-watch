// Package feed collects news from RSS and Atom sources and normalizes it into
// model.Article values ready for summarization.
package feed

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxSummaryRunes caps how much prose we keep per item. Feed descriptions run
// from a single sentence to a full article body; the summarizer only needs
// enough to tell stories apart, and the rest is prompt cost.
const maxSummaryRunes = 400

// Item is one entry from a feed, after RSS and Atom have been flattened into a
// single shape. It is deliberately not a model.Article: it carries no source
// identity and no dedupe key, which only the fetcher can supply.
type Item struct {
	Title     string
	URL       string
	Summary   string
	Published time.Time
}

// Parse reads an RSS 2.0 or Atom document. One decode handles both: the two
// vocabularies do not collide, so whichever element set the document uses
// populates its half of the struct and the other half stays empty.
func Parse(r io.Reader) ([]Item, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	// Real feeds carry undefined entities and bare ampersands. Strict mode
	// rejects the whole document over one of them, losing a source for the day.
	dec.Strict = false

	var doc rawFeed
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode feed: %w", err)
	}

	items := make([]Item, 0, len(doc.Items)+len(doc.Entries))
	for _, it := range doc.Items {
		items = append(items, Item{
			Title:     collapseSpace(it.Title),
			URL:       strings.TrimSpace(it.Link),
			Summary:   summarize(firstNonEmpty(it.Description, it.Encoded)),
			Published: parseTime(firstNonEmpty(it.PubDate, it.Date, it.Updated)),
		})
	}
	for _, e := range doc.Entries {
		items = append(items, Item{
			Title:     collapseSpace(e.Title),
			URL:       e.link(),
			Summary:   summarize(firstNonEmpty(e.Summary, e.Content)),
			Published: parseTime(firstNonEmpty(e.Published, e.Updated)),
		})
	}
	return items, nil
}

// rawFeed holds both vocabularies at once: RSS items hang off <channel>, Atom
// entries off the root. Go matches these tags on local name regardless of
// namespace, so the Atom namespace needs no special handling.
type rawFeed struct {
	Items   []rawItem  `xml:"channel>item"`
	Entries []rawEntry `xml:"entry"`
}

type rawItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Encoded     string `xml:"encoded"` // content:encoded
	PubDate     string `xml:"pubDate"`
	Date        string `xml:"date"` // dc:date
	Updated     string `xml:"updated"`
}

type rawEntry struct {
	Title     string    `xml:"title"`
	Links     []rawLink `xml:"link"`
	Summary   string    `xml:"summary"`
	Content   string    `xml:"content"`
	Published string    `xml:"published"`
	Updated   string    `xml:"updated"`
}

type rawLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

// link picks the human-readable page for an entry. Atom entries commonly carry
// several links -- "self", "enclosure", "related" -- and only the alternate one
// points at the story a reader should open.
func (e rawEntry) link() string {
	var fallback string
	for _, l := range e.Links {
		href := strings.TrimSpace(l.Href)
		if href == "" {
			continue
		}
		if l.Rel == "" || l.Rel == "alternate" {
			return href
		}
		if fallback == "" {
			fallback = href
		}
	}
	return fallback
}

// timeFormats covers what feeds actually emit. RSS specifies RFC 822 and Atom
// RFC 3339, but publishers drift from both, so unparseable dates fall back to
// the fetch time rather than dropping the item.
var timeFormats = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339Nano,
	time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range timeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// summarize turns a feed description into plain, bounded prose.
func summarize(s string) string {
	return truncate(stripHTML(s), maxSummaryRunes)
}

// stripHTML flattens markup to text. Descriptions arrive as HTML fragments and
// are frequently double-escaped, so the XML decode leaves real tags behind.
func stripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth == 0 {
				b.WriteRune(r) // a lone '>' outside a tag is literal text
				continue
			}
			depth--
			if depth == 0 {
				b.WriteByte(' ') // a tag boundary is a word boundary
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return collapseSpace(unescape(b.String()))
}

// namedEntities are the ones that survive to a second unescape pass in
// practice; anything rarer is left as written rather than guessed at.
var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'",
	"nbsp": " ", "ldquo": "“", "rdquo": "”",
	"lsquo": "‘", "rsquo": "’", "hellip": "…",
	"mdash": "—", "ndash": "–",
}

func unescape(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], ';')
		// An unterminated or absurdly long run is a literal ampersand.
		if end < 0 || end > 12 {
			b.WriteByte('&')
			i++
			continue
		}
		name := s[i+1 : i+end]
		if repl, ok := decodeEntity(name); ok {
			b.WriteString(repl)
			i += end + 1
			continue
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

func decodeEntity(name string) (string, bool) {
	if repl, ok := namedEntities[strings.ToLower(name)]; ok {
		return repl, true
	}
	if !strings.HasPrefix(name, "#") {
		return "", false
	}

	digits, base := name[1:], 10
	if len(digits) > 0 && (digits[0] == 'x' || digits[0] == 'X') {
		digits, base = digits[1:], 16
	}
	code, err := strconv.ParseUint(digits, base, 32)
	if err != nil || code == 0 || !utf8.ValidRune(rune(code)) {
		return "", false
	}
	return string(rune(code)), true
}

func collapseSpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// truncate cuts to n runes on a word boundary so the summary does not end
// mid-word in the prompt.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	cut := len(s)
	count := 0
	for i := range s {
		if count == n {
			cut = i
			break
		}
		count++
	}
	out := s[:cut]
	if sp := strings.LastIndexByte(out, ' '); sp > n/2 {
		out = out[:sp]
	}
	return strings.TrimRight(out, " ,;:-") + "…"
}

// charsetReader handles the non-UTF-8 feeds that actually turn up. Go's XML
// decoder refuses any encoding it does not recognize, which would silently cost
// us a whole source, so the single-byte Western encodings get a converter.
func charsetReader(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		// windows-1252 differs from latin-1 only in 0x80-0x9F (smart quotes and
		// dashes), which this maps to the C1 controls instead. Wrong glyphs in a
		// handful of punctuation marks beats losing the feed.
		return &latin1Reader{r: bufio.NewReader(input)}, nil
	}
	return nil, fmt.Errorf("unsupported charset %q", label)
}

// latin1Reader widens single-byte input to UTF-8 one rune at a time.
type latin1Reader struct {
	r   *bufio.Reader
	buf [utf8.UTFMax]byte
	n   int // bytes of buf holding an encoded rune
	i   int // read offset into buf
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		if l.i < l.n {
			p[written] = l.buf[l.i]
			l.i++
			written++
			continue
		}
		b, err := l.r.ReadByte()
		if err != nil {
			if written > 0 {
				return written, nil // surface the error on the next call
			}
			return 0, err
		}
		l.n = utf8.EncodeRune(l.buf[:], rune(b))
		l.i = 0
	}
	return written, nil
}
