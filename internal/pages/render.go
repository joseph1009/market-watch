package pages

import (
	"html"
	"regexp"
	"strings"
)

// The messages are the source of a page: the service writes them once, for
// Telegram, and the page is drawn from them. A page can never say something
// the chat copy does not, and every renderer the chat already has -- the
// brief, the closer look, the analysis, the industry -- has a page for free.
//
// Telegram's HTML is a handful of tags and line breaks, and the renderers use
// them in a few fixed ways, which is what lets a page recover the structure:
//
//	──────────             a divider: a section starts
//	<b>OVERVIEW</b>        a line in bold capitals: a section's heading
//	<b>Bonds</b>           a block's first line in bold: a sub-heading
//	• ...                  a bullet
//	<b>Sources</b>         the source list, folded away on a page

var (
	tag         = regexp.MustCompile(`<[^>]*>`)
	dividerLine = regexp.MustCompile(`^─+$`)
	boldLine    = regexp.MustCompile(`^<b>([^<]*)</b>$`)
	pickLine    = regexp.MustCompile(`^\S+ <b>[^<]+</b> <code>[^<]+</code>$`)
	anchorTag   = regexp.MustCompile(`^<a href="(https?://[^"<>\s]+)">$`)
	citation    = regexp.MustCompile(`^\[\d+\]$`)
	lowercase   = regexp.MustCompile(`\p{Ll}`)
	entity      = regexp.MustCompile(`&[a-z#0-9]+;`)
)

// Render lays a page out as a whole HTML document.
func Render(p Page) []byte {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, viewport-fit=cover\">\n")
	b.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">\n")
	b.WriteString("<meta property=\"og:title\" content=\"" + html.EscapeString(p.Title) + "\">\n")
	if p.Description != "" {
		d := html.EscapeString(clip(p.Description, 200))
		b.WriteString("<meta name=\"description\" content=\"" + d + "\">\n")
		b.WriteString("<meta property=\"og:description\" content=\"" + d + "\">\n")
	}
	b.WriteString("</head>\n<body>\n")
	b.WriteString(Fragment(p))
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String())
}

// Fragment is the page without the document around it: its title, its
// styles and its content.
func Fragment(p Page) string {
	var w writer
	w.WriteString("<title>" + html.EscapeString(p.Title) + "</title>\n<style>" + style + "</style>\n<main class=\"page\">\n")
	if p.Doc != nil {
		p.Doc.write(&w)
		w.WriteString("</main>\n")
		return w.String()
	}
	for i, m := range p.Messages {
		w.message(m, i == 0)
	}
	w.closeAll()
	if p.Note != "" {
		w.WriteString("<footer>" + safe(p.Note, false) + "</footer>\n")
	}
	w.WriteString("</main>\n")
	return w.String()
}

type writer struct {
	strings.Builder
	para, list, sources bool
}

func (w *writer) message(m string, first bool) {
	for i, block := range strings.Split(strings.TrimSpace(m), "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if first && i == 0 && boldLine.MatchString(lines[0]) {
			w.header(lines)
			continue
		}
		w.block(lines)
	}
}

// header is the page's opening: the first message's title, and the lines
// under it.
func (w *writer) header(lines []string) {
	w.WriteString("<header>\n<h1>" + safe(boldLine.FindStringSubmatch(lines[0])[1], false) + "</h1>\n")
	for _, l := range lines[1:] {
		w.WriteString("<p class=\"dek\">" + safe(l, false) + "</p>\n")
	}
	w.WriteString("</header>\n")
}

func (w *writer) block(lines []string) {
	headed := false
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case dividerLine.MatchString(line):
			// Before a heading it only marks the section, which the heading
			// draws itself; between picks it is a rule.
			if i+1 >= len(lines) || !boldLine.MatchString(strings.TrimSpace(lines[i+1])) {
				w.close()
				w.WriteString("<hr>\n")
			}
			continue // the line after it may still be the block's heading
		case !headed && line == "<b>Sources</b>":
			w.closeAll()
			w.WriteString("<details class=\"sources\">\n<summary>Sources</summary>\n")
			w.sources = true
		case !headed && boldLine.MatchString(line):
			w.close()
			text := boldLine.FindStringSubmatch(line)[1]
			if lowercase.MatchString(stripEntities(text)) {
				w.WriteString("<h3>" + safe(text, false) + "</h3>\n")
			} else {
				w.WriteString("<h2>" + safe(text, false) + "</h2>\n")
			}
		case !headed && pickLine.MatchString(line):
			w.close()
			w.WriteString("<h3 class=\"pick\">" + safe(line, false) + "</h3>\n")
		case strings.HasPrefix(line, "# "):
			w.close()
			w.WriteString("<h2>" + safe(line[2:], false) + "</h2>\n")
		case strings.HasPrefix(line, "• "):
			if w.para {
				w.WriteString("</p>\n")
				w.para = false
			}
			if !w.list {
				w.WriteString("<ul>\n")
				w.list = true
			}
			w.WriteString("<li>" + safe(line[len("• "):], w.sources) + "</li>\n")
		default:
			if w.list {
				w.WriteString("</ul>\n")
				w.list = false
			}
			if w.para {
				w.WriteString("<br>\n")
			} else {
				w.WriteString("<p>")
				w.para = true
			}
			w.WriteString(safe(line, w.sources))
		}
		headed = true
	}
	// A list runs on into the next block: the chat puts a blank line between
	// bullets to make them readable, and they are still one list.
	if w.para {
		w.WriteString("</p>\n")
		w.para = false
	}
}

func (w *writer) close() {
	if w.para {
		w.WriteString("</p>\n")
		w.para = false
	}
	if w.list {
		w.WriteString("</ul>\n")
		w.list = false
	}
}

func (w *writer) closeAll() {
	w.close()
	if w.sources {
		w.WriteString("</details>\n")
		w.sources = false
	}
}

var allowed = map[string]bool{
	"<b>": true, "</b>": true, "<i>": true, "</i>": true, "<u>": true, "</u>": true,
	"<s>": true, "</s>": true, "<code>": true, "</code>": true, "</a>": true,
}

// safe passes Telegram's tags through and escapes anything else, so a page
// can carry no markup the chat could not. Links open in a new tab and say
// nothing about where they came from. A link's text says what it is: "[12]"
// is a source, and a word in the prose links to an explanation of it.
func safe(line string, inSources bool) string {
	var b strings.Builder
	open := false
	rest := line
	for {
		loc := tag.FindStringIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:loc[0]])
		t := rest[loc[0]:loc[1]]
		rest = rest[loc[1]:]
		switch m := anchorTag.FindStringSubmatch(t); {
		case m != nil && !open:
			text := rest
			if end := strings.Index(rest, "</a>"); end >= 0 {
				text = rest[:end]
			}
			class := ""
			switch {
			case citation.MatchString(text):
				class = ` class="cite"`
			case !inSources && explains(m[1]):
				class = ` class="term"`
			}
			b.WriteString(`<a href="` + m[1] + `"` + class + ` target="_blank" rel="noopener noreferrer">`)
			open = true
		case t == "</a>" && open:
			b.WriteString(t)
			open = false
		case allowed[t] && t != "</a>":
			b.WriteString(t)
		default:
			b.WriteString(html.EscapeString(t))
		}
	}
	if open {
		b.WriteString("</a>")
	}
	return b.String()
}

// explains reports whether a link is to an explanation of a word, as the
// glossary's are (config/glossary.yaml), rather than to a source.
func explains(url string) bool {
	for _, host := range []string{"https://www.investopedia.com/", "https://en.wikipedia.org/"} {
		if strings.HasPrefix(url, host) {
			return true
		}
	}
	return false
}

func stripEntities(s string) string {
	return entity.ReplaceAllString(s, "")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// style is the page's look: one reading column, the sections set off by a
// rule and a heading in spaced capitals, links blue, explained words in amber
// with a dotted line, sources numbered small; figures in tables and charts in
// a card, rises green and falls red. System fonts, so a page opens
// at once on a phone and asks nothing of any other server.
const style = `
:root {
  --ground: #f3f5f7; --card: #ffffff; --panel: #e8ecf0; --ink: #15212c; --muted: #5a6977;
  --rule: #d5dce3; --link: #1f6fb2; --term: #8f5200; --cite-bg: #dfe8f1; --code-bg: #e6ebf0;
  --up: #17803f; --up-bg: #e1f2e7; --down: #c23b2e; --down-bg: #fae5e2;
  --hold: #5d6b78; --hold-bg: #e7ebef; --high: #c23b2e; --medium: #c27a0e;
  --track: #e3e8ed; --mark: #8a98a6; --box: #eaf1f8; --box-rule: #1f6fb2;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    color-scheme: dark;
    --ground: #0e151c; --card: #151f29; --panel: #1a2631; --ink: #e3eaf0; --muted: #91a1b0;
    --rule: #26333f; --link: #6cb4f0; --term: #eeb05c; --cite-bg: #1d2d3c; --code-bg: #1e2a35;
    --up: #4cc47a; --up-bg: #15301f; --down: #f2756a; --down-bg: #3a1c1a;
    --hold: #a3b0bc; --hold-bg: #24313d; --high: #f2756a; --medium: #e9a948;
    --track: #24313d; --mark: #7d8c9a; --box: #142536; --box-rule: #6cb4f0;
  }
}
:root[data-theme="dark"] {
  color-scheme: dark;
  --ground: #0e151c; --card: #151f29; --panel: #1a2631; --ink: #e3eaf0; --muted: #91a1b0;
  --rule: #26333f; --link: #6cb4f0; --term: #eeb05c; --cite-bg: #1d2d3c; --code-bg: #1e2a35;
  --up: #4cc47a; --up-bg: #15301f; --down: #f2756a; --down-bg: #3a1c1a;
  --hold: #a3b0bc; --hold-bg: #24313d; --high: #f2756a; --medium: #e9a948;
  --track: #24313d; --mark: #7d8c9a; --box: #142536; --box-rule: #6cb4f0;
}
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }
body {
  margin: 0; background: var(--ground); color: var(--ink);
  font: 17px/1.6 system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
}
.page {
  max-width: 720px; margin: 0 auto;
  padding-inline: max(16px, env(safe-area-inset-left, 0px));
  padding-block: calc(env(safe-area-inset-top, 0px) + 28px) calc(env(safe-area-inset-bottom, 0px) + 56px);
  overflow-wrap: anywhere;
}
header { display: grid; gap: 6px; margin-bottom: 8px; }
.kicker { margin: 0; font-size: 13px; font-weight: 700; letter-spacing: 0.09em; text-transform: uppercase; color: var(--link); }
h1 { font-size: 28px; line-height: 1.22; font-weight: 750; letter-spacing: -0.015em; margin: 0; text-wrap: balance; }
.dek { margin: 0; color: var(--muted); font-size: 15px; }
.note { margin: 14px 0 0; padding: 10px 14px; background: var(--panel); border-radius: 8px; font-size: 14px; color: var(--muted); }
.note i { color: var(--muted); }
section { margin-top: 40px; }
h2 {
  font-size: 13px; line-height: 1.3; font-weight: 700; letter-spacing: 0.09em; text-transform: uppercase;
  color: var(--muted); margin: 40px 0 6px; padding-top: 14px; border-top: 1px solid var(--rule); text-wrap: balance;
}
section > h2 { margin-top: 0; }
.lead { margin: 4px 0 12px; color: var(--muted); font-size: 15px; }
h3 { font-size: 18px; line-height: 1.35; font-weight: 700; margin: 24px 0 6px; text-wrap: balance; }
h3.pick { font-size: 20px; margin-top: 8px; }
h4 { font-size: 13px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--muted); margin: 0 0 2px; }
p { margin: 10px 0; }
ul, ol { margin: 6px 0 12px; padding-left: 1.2em; display: grid; gap: 8px; }
li::marker { color: var(--muted); }
hr { border: 0; border-top: 1px solid var(--rule); margin: 32px 0 24px; }
i { color: var(--muted); }
b i, i b { color: var(--ink); }
a { color: var(--link); text-decoration: none; }
a:hover { text-decoration: underline; }
a:focus-visible, summary:focus-visible { outline: 2px solid var(--link); outline-offset: 2px; border-radius: 3px; }
a.term { color: var(--term); border-bottom: 1px dotted var(--term); }
a.cite {
  font-size: 11px; font-weight: 600; vertical-align: 2px; font-variant-numeric: tabular-nums;
  background: var(--cite-bg); border-radius: 4px; padding: 0 4px; margin-left: 2px;
}
code {
  font: 0.86em ui-monospace, "SF Mono", Menlo, Consolas, monospace; font-variant-numeric: tabular-nums;
  background: var(--code-bg); border-radius: 4px; padding: 1px 5px;
}
.small { font-size: 13px; color: var(--muted); margin: 6px 0 0; }
.small i { color: var(--muted); }

.box { margin: 24px 0 8px; padding: 14px 18px 6px; background: var(--box); border-left: 4px solid var(--box-rule); border-radius: 0 10px 10px 0; }
.box-title { margin: 0 0 4px; font-size: 13px; font-weight: 700; letter-spacing: 0.09em; text-transform: uppercase; color: var(--link); }
.box ul { gap: 10px; }

figure { margin: 18px 0; }
figcaption { font-size: 14px; font-weight: 700; margin-bottom: 8px; }
.scroll { overflow-x: auto; -webkit-overflow-scrolling: touch; border: 1px solid var(--rule); border-radius: 10px; background: var(--card); }
table { border-collapse: collapse; width: 100%; font-size: 15px; line-height: 1.4; font-variant-numeric: tabular-nums; overflow-wrap: normal; }
th, td { padding: 8px 12px; text-align: left; vertical-align: top; }
th { font-size: 12px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; color: var(--muted); border-bottom: 1px solid var(--rule); white-space: nowrap; }
td { border-bottom: 1px solid var(--rule); }
tbody tr:last-child td { border-bottom: 0; }
td.num, th.num { text-align: right; white-space: nowrap; }
tr.marked td { background: var(--box); }
td i { color: var(--muted); }
table.labelled th:first-child, table.labelled td:first-child {
  position: sticky; left: 0; z-index: 1; min-width: 8.5em; background: var(--card); box-shadow: inset -1px 0 0 var(--rule);
}
table.labelled tr.marked td:first-child { background: var(--box); }

.up { color: var(--up); } .down { color: var(--down); }
.chart { background: var(--card); border: 1px solid var(--rule); border-radius: 10px; padding: 14px 16px; }
.bar-row { display: grid; grid-template-columns: minmax(96px, 34%) 1fr 64px; align-items: center; gap: 10px; font-size: 14px; line-height: 1.3; padding: 3px 0; }
.bar-label { color: var(--ink); }
.bar-track { position: relative; height: 14px; }
.bar-zero { position: absolute; top: -3px; bottom: -3px; width: 1px; background: var(--mark); }
.bar { position: absolute; top: 0; bottom: 0; border-radius: 3px; }
.bar.up { background: var(--up); } .bar.down { background: var(--down); }
.bar-value { text-align: right; font-weight: 600; font-variant-numeric: tabular-nums; }

.col-plot { display: grid; grid-template-columns: repeat(var(--cols), 1fr); gap: 10px; height: 150px; }
.col { position: relative; }
.col-zero { position: absolute; left: -5px; right: -5px; height: 1px; background: var(--mark); }
.col-bar { position: absolute; left: 18%; right: 18%; border-radius: 3px; }
.col-bar.up { background: var(--link); } .col-bar.down { background: var(--down); }
.col-labels { display: grid; grid-template-columns: repeat(var(--cols), 1fr); gap: 10px; margin-top: 8px; font-size: 12px; line-height: 1.3; color: var(--muted); text-align: center; }
.col-labels b { display: block; color: var(--ink); font-size: 13px; font-variant-numeric: tabular-nums; }

.range-track { position: relative; height: 10px; margin: 30px 6px 30px; background: var(--track); border-radius: 5px; }
.range-mark { position: absolute; top: -6px; width: 2px; height: 22px; background: var(--mark); }
.range-mark:nth-child(even) span { bottom: auto; top: 24px; }
.range-mark span { position: absolute; bottom: 24px; left: 50%; transform: translateX(-50%); font-size: 11px; color: var(--muted); white-space: nowrap; }
.range-last { position: absolute; top: 50%; width: 16px; height: 16px; margin: -8px 0 0 -8px; border-radius: 50%; background: var(--link); border: 3px solid var(--card); box-shadow: 0 0 0 1px var(--link); }
.range-ends { display: flex; justify-content: space-between; gap: 8px; font-size: 13px; color: var(--muted); font-variant-numeric: tabular-nums; }
.range-now { color: var(--ink); font-weight: 700; }

.facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); margin: 14px 0; background: var(--card); border: 1px solid var(--rule); border-radius: 10px; overflow: hidden; }
.facts div { padding: 8px 12px; box-shadow: 1px 1px 0 var(--rule); }
.facts dt { font-size: 12px; color: var(--muted); }
.facts dd { margin: 0; font-weight: 650; font-variant-numeric: tabular-nums; }

.field { margin: 16px 0; }
.field p { margin: 0; }

.chips { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin: 6px 0 14px; }
.chips-label { font-size: 13px; color: var(--muted); margin-right: 2px; }
.chip { font-size: 13px; font-weight: 600; padding: 2px 8px; border-radius: 999px; font-variant-numeric: tabular-nums; background: var(--hold-bg); color: var(--hold); }
.chip.up { background: var(--up-bg); color: var(--up); } .chip.down { background: var(--down-bg); color: var(--down); }

.card { background: var(--card); border: 1px solid var(--rule); border-radius: 12px; padding: 18px 18px 8px; margin: 18px 0; scroll-margin-top: 16px; }
.card-head { display: grid; gap: 4px; }
.card-head h3 { margin: 0; font-size: 21px; }
.meta { margin: 0; color: var(--muted); font-size: 14px; }
.badge { justify-self: start; font-size: 12px; font-weight: 800; letter-spacing: 0.08em; padding: 2px 10px; border-radius: 999px; background: var(--hold-bg); color: var(--hold); }
.badge.buy { background: var(--up-bg); color: var(--up); } .badge.sell { background: var(--down-bg); color: var(--down); }
.verdict { font-weight: 800; letter-spacing: 0.04em; }
.verdict.buy { color: var(--up); } .verdict.sell { color: var(--down); } .verdict.hold { color: var(--hold); }
.dot { display: inline-block; width: 9px; height: 9px; border-radius: 50%; margin-right: 6px; vertical-align: 1px; }
.dot.high { background: var(--high); } .dot.medium { background: var(--medium); }

.chain ol { list-style: none; padding: 0; margin: 0; display: flex; flex-wrap: wrap; gap: 26px; counter-reset: none; }
.chain li { position: relative; flex: 1 1 150px; display: grid; gap: 6px; align-content: start; background: var(--card); border: 1px solid var(--rule); border-top: 3px solid var(--link); border-radius: 10px; padding: 10px 12px; }
.chain li + li::before { content: "→"; position: absolute; left: -21px; top: 50%; transform: translateY(-50%); color: var(--muted); font-weight: 700; }
.step-n { font-size: 12px; font-weight: 700; color: var(--link); }
.step-name { font-weight: 700; line-height: 1.3; }
.step-items { display: flex; flex-wrap: wrap; gap: 4px; }
@media (max-width: 520px) {
  .chain ol { flex-direction: column; gap: 22px; }
  .chain li { flex-basis: auto; }
  .chain li + li::before { content: "↓"; left: 50%; top: -20px; transform: translateX(-50%); }
  .bar-row { grid-template-columns: 38% 1fr 56px; gap: 8px; }
}

details.sources { margin-top: 14px; background: var(--panel); border-radius: 10px; padding: 10px 16px; font-size: 14px; }
details.sources summary { cursor: pointer; font-weight: 700; }
details.sources .count { font-weight: 600; color: var(--muted); font-size: 12px; margin-left: 4px; }
details.sources ol { list-style: none; padding: 0; gap: 6px; margin-top: 10px; }
details.sources .n { display: inline-block; min-width: 2.2em; font-size: 12px; font-weight: 600; color: var(--muted); font-variant-numeric: tabular-nums; }
details.sources .src { color: var(--muted); font-size: 13px; }
footer { margin-top: 40px; padding-top: 14px; border-top: 1px solid var(--rule); color: var(--muted); font-size: 14px; }
footer p { margin: 4px 0; }
`
