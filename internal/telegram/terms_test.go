package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

var testTerms = []model.Term{
	{URL: "https://example.com/pce", Words: []string{"PCE", "personal consumption expenditures"}},
	{URL: "https://example.com/curve", Words: []string{"yield curve"}},
	{URL: "https://example.com/eps", Words: []string{"EPS"}},
}

// The first mention of a term in a section is linked to its explanation,
// once; text inside links and bold is left alone, and a term in capitals
// matches only in capitals.
func TestJargonIsLinkedOnceASection(t *testing.T) {
	blocks := []string{
		"<b>📉 PCE</b>\n• The PCE index rose 0.3% <a href=\"https://news/1\">[1]</a>, and PCE matters.",
		"• The yield curve steepened; the Yield Curve is a gauge. Steps in the reps' report.",
	}
	got := linkTerms(blocks, testTerms)
	if strings.Contains(got[0], `<b>📉 <a`) {
		t.Errorf("a heading was linked: %s", got[0])
	}
	if strings.Count(got[0], "https://example.com/pce") != 1 || !strings.Contains(got[0], `The <a href="https://example.com/pce">PCE</a> index`) {
		t.Errorf("PCE not linked once at its first mention: %s", got[0])
	}
	if strings.Count(got[1], "https://example.com/curve") != 1 || !strings.Contains(got[1], `The <a href="https://example.com/curve">yield curve</a> steepened`) {
		t.Errorf("yield curve: %s", got[1])
	}
	if strings.Contains(got[1], "example.com/eps") {
		t.Errorf("EPS matched inside an ordinary word: %s", got[1])
	}
	if !strings.Contains(got[0], `<a href="https://news/1">[1]</a>`) {
		t.Errorf("a citation link was disturbed: %s", got[0])
	}
}

// The writer's **marks** become bold, a stray one disappears, and the
// section heading carries its sector's emoji.
func TestHighlightsAndSectorEmoji(t *testing.T) {
	if got := highlight("Brent topped **$100** a barrel, up **5%**"); got != "Brent topped <b>$100</b> a barrel, up <b>5%</b>" {
		t.Errorf("highlight = %q", got)
	}
	if got := highlight("an unclosed ** marker"); strings.Contains(got, "*") {
		t.Errorf("a stray marker reached the reader: %q", got)
	}
	rep := model.Report{
		GeneratedAt: time.Date(2026, 10, 1, 7, 30, 0, 0, time.UTC),
		Sections:    []model.Section{{GroupID: "energy", GroupName: "Energy", Emoji: "🛢️", Body: "### ⛽ Diesel\n- Diesel topped **$6** a gallon [1]."}},
	}
	text := strings.Join(RenderWith(rep, Options{Display: time.UTC, Terms: testTerms}), "\n")
	for _, want := range []string{"<b>🛢️ ENERGY</b>", "<b>⛽ Diesel</b>", "• Diesel topped <b>$6</b> a gallon"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}
