package pages

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// A page is read at its own random address and nowhere else: the server
// lists nothing, takes nothing, and runs no script on the page.
func TestAPageIsServedOnlyAtItsAddress(t *testing.T) {
	s := &Store{Dir: t.TempDir(), BaseURL: "https://pages.example"}
	url, err := s.Publish(Page{Title: "Market Watch · Thu 1 Oct", Messages: []string{"<b>📊 Market Watch</b>\n<i>Thursday</i>"}})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(url, "https://pages.example")
	if !pagePath.MatchString(path) {
		t.Fatalf("address %q", url)
	}

	h := s.Handler()
	rec := get(t, h, http.MethodGet, path)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<h1>📊 Market Watch</h1>") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script") {
		t.Errorf("CSP = %q", csp)
	}
	if rec.Header().Get("X-Robots-Tag") == "" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers = %v", rec.Header())
	}

	for _, p := range []string{"/", "/r/", "/r/AAAAAAAAAAAAAAAAAAAAAA", "/r/../prefs.yaml", path + ".html"} {
		if rec := get(t, h, http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d", p, rec.Code)
		}
	}
	if rec := get(t, h, http.MethodPost, path); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	if rec := get(t, h, http.MethodGet, "/robots.txt"); !strings.Contains(rec.Body.String(), "Disallow: /") {
		t.Errorf("robots.txt = %q", rec.Body)
	}
}

// Pages are kept for a month, and the next page written clears the older.
func TestPagesOlderThanAMonthAreDeleted(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s := &Store{Dir: dir, BaseURL: "https://pages.example", Now: func() time.Time { return now }}
	if _, err := s.Publish(Page{Title: "old"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	old := filepath.Join(dir, entries[0].Name())
	if err := os.Chtimes(old, now.Add(-31*24*time.Hour), now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(Page{Title: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a page from 31 days ago is still there")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d pages left, want 1", len(entries))
	}
}

// The messages' structure becomes the page's: section headings, sub-headings,
// bullets, sources folded away, and links that say what they are.
func TestAPageIsLaidOutFromTheMessages(t *testing.T) {
	messages := []string{
		"<b>📊 Market Watch</b>\n<i>Thursday, 1 October 2026</i>\n\n──────────\n<b>OVERVIEW</b>\n\nYields hit a high.\n\n<b>🛢️ Oil</b>\n• Brent rose to <b>$103</b> <a href=\"https://news.example/1\">[1]</a>.\n\n• The <a href=\"https://www.investopedia.com/brent\">Brent</a> spread widened.",
		"──────────\n<b>ENERGY &amp; OIL</b>\n\n🟢 <b>Some Co</b> <code>SOME</code>\n<b>BUY</b> · medium confidence\n\n─────\n🔴 <b>Other Co</b> <code>OTH</code>",
		"<b>Sources</b>\n<i>Energy</i>\n• <a href=\"https://news.example/1\">Oil rises</a> — Reuters",
	}
	got := Fragment(Page{Title: "Market Watch · Thu 1 Oct", Messages: messages, Note: "<i>AI-written, unchecked, not advice.</i>"})
	for _, want := range []string{
		"<title>Market Watch · Thu 1 Oct</title>",
		"<h1>📊 Market Watch</h1>",
		`<p class="dek"><i>Thursday, 1 October 2026</i></p>`,
		"<h2>OVERVIEW</h2>",
		"<h2>ENERGY &amp; OIL</h2>",
		"<h3>🛢️ Oil</h3>",
		`<li>Brent rose to <b>$103</b> <a href="https://news.example/1" class="cite" target="_blank" rel="noopener noreferrer">[1]</a>.</li>`,
		`<a href="https://www.investopedia.com/brent" class="term"`,
		`<h3 class="pick">🟢 <b>Some Co</b> <code>SOME</code></h3>`,
		"<hr>",
		`<details class="sources">`,
		`<a href="https://news.example/1" target="_blank" rel="noopener noreferrer">Oil rises</a>`,
		"<footer><i>AI-written, unchecked, not advice.</i></footer>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page is missing %s", want)
		}
	}
	if strings.Count(got, "<hr>") != 1 {
		t.Errorf("a divider before a heading drew a rule:\n%s", got)
	}
}

// A page carries no markup the chat could not: anything but Telegram's tags,
// and any link that is not to a web address, is shown as text.
func TestAPageEscapesWhatTelegramWouldNotShow(t *testing.T) {
	got := Fragment(Page{Title: "<script>", Messages: []string{
		"<b>Title</b>\n\n<script>alert(1)</script> <a href=\"javascript:alert(1)\">x</a> <img src=x onerror=alert(1)> <b>kept</b>",
	}})
	for _, bad := range []string{"<script", "<img", `href="javascript`} {
		if strings.Contains(got, bad) {
			t.Errorf("page carries %s:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "<b>kept</b>") || !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("page = %s", got)
	}
}

// Bullets the chat spaces apart with blank lines are still one list.
func TestSpacedBulletsAreOneList(t *testing.T) {
	got := Fragment(Page{Messages: []string{"<b>Title</b>\n\n<b>Why</b>\n• one\n\n• two\n\n• three\n\nAfter."}})
	if strings.Count(got, "<ul>") != 1 || !strings.Contains(got, "<li>three</li>\n</ul>\n<p>After.</p>") {
		t.Errorf("page = %s", got)
	}
}
