package pages

import (
	"fmt"
	"html"
	"math"
	"strings"
)

// Since 2026-10-01, at the owner's request, a page is laid out from what the
// messages were written from -- the report, the verdicts, the accounts --
// rather than from the messages themselves. A chat can show bold, italics and
// links; a page can show a table of the day's markets, a company's figures
// year by year, a bar for every sector's move and an industry as a chain of
// parts. The prose is the same prose, in Telegram's few tags, so everything
// a page prints passes the same filter (safe) the messages' pages do.
//
// The charts are drawn in HTML and CSS, not scripts or images: they scale
// with the text on a phone, read in either theme, and keep the pages' rule
// that nothing runs and nothing loads from anywhere else.

// Doc is a page built from parts.
type Doc struct {
	Kicker string   // plain text over the title: "Market Watch"
	Title  string   // plain text
	Dek    []string // lines under the title, Telegram HTML
	Note   string   // the note a verdict is read under, set apart; Telegram HTML
	Parts  []Part
	Footer []string // Telegram HTML
}

// Part is one piece of a page.
type Part interface{ write(w *writer) }

// Prose is model prose already laid out for Telegram, a block a paragraph:
// a bold first line is a sub-heading, "• " lines a list.
type Prose []string

// Para is one paragraph, Telegram HTML.
type Para string

// Small is a line of small print: where figures came from, what a table
// leaves out. Telegram HTML.
type Small string

// Section is a titled part of a page, a rule above it.
type Section struct {
	ID    string // an anchor to link to, letters, digits and hyphens
	Title string // plain text
	Lead  string // a line under the title, Telegram HTML
	Parts []Part
}

// Box is a set-apart list: the brief's few bullets for the reader in a hurry.
type Box struct {
	Title string   // plain text
	Items []string // Telegram HTML
}

// Table is a table of figures. Align gives each column's alignment, "l" or
// "r"; figures read best right-aligned.
type Table struct {
	Caption string     // plain text
	Head    []string   // plain text
	Align   string     // "lrr": a letter a column
	Rows    [][]string // Telegram HTML cells
	Marked  []bool     // rows to stand out, where set
	Note    string     // Telegram HTML, under the table
}

// Bar is one value in a chart.
type Bar struct {
	Label string  // plain text
	Value float64 // its size and sign
	Text  string  // the value as printed: "+1.2%", "$3.4bn"
}

// Bars is a horizontal bar chart, its bars running left of a zero line for
// a fall and right for a rise.
type Bars struct {
	Title string // plain text
	Items []Bar
	Note  string // Telegram HTML
}

// Columns is a vertical bar chart: a figure over time, oldest first.
type Columns struct {
	Title string // plain text
	Items []Bar
	Note  string // Telegram HTML
}

// Mark is a point placed on a range.
type Mark struct {
	Label string // plain text
	Value float64
}

// Range places a price between its low and its high, with its averages
// marked on the way.
type Range struct {
	Title           string // plain text
	Low, High, Last float64
	LowText         string // plain text
	HighText        string
	LastText        string
	Marks           []Mark
	Note            string // Telegram HTML
}

// Facts are labelled values in a grid: price, moves, valuation.
type Facts [][2]string // plain label, Telegram HTML value

// Field is a labelled paragraph of a case: "What changed", "The case".
type Field struct {
	Label string // plain text
	Body  string // Telegram HTML; a line break is kept
}

// Chips are short tagged values in a row: the day's biggest moves.
type Chips struct {
	Label string // plain text
	Items []Chip
}

// Chip is one: its text, and "up" or "down" for its colour.
type Chip struct {
	Text string // plain text
	Tone string
}

// Card is one company: its verdict as a badge, then what was found.
type Card struct {
	ID    string
	Tone  string   // "buy", "sell" or "hold": the badge's colour
	Badge string   // plain text: "BUY"
	Title string   // plain text
	Code  string   // the ticker, plain text
	Meta  []string // plain text: "low confidence", "overreacted"
	Parts []Part
}

// Chain is an industry drawn as its parts in order, from what goes in to
// who buys what comes out, with arrows between them.
type Chain struct {
	Title string // plain text
	Steps []Step
	Note  string // Telegram HTML
}

// Step is one part of a chain and the companies in it.
type Step struct {
	Name  string   // plain text
	Items []string // plain text: tickers
}

// Sources are the links behind a part of the page, folded away.
type Sources struct {
	Title string // plain text
	Links []Link
}

// Link is one source.
type Link struct {
	N      int // its citation number, where it has one
	Title  string
	URL    string
	Source string
}

// Group is parts written one after another, for a builder that returns
// several.
type Group []Part

func (d Doc) write(w *writer) {
	w.WriteString("<header>\n")
	if d.Kicker != "" {
		w.WriteString("<p class=\"kicker\">" + text(d.Kicker) + "</p>\n")
	}
	w.WriteString("<h1>" + text(d.Title) + "</h1>\n")
	for _, l := range d.Dek {
		w.WriteString("<p class=\"dek\">" + safe(l, false) + "</p>\n")
	}
	w.WriteString("</header>\n")
	if d.Note != "" {
		w.WriteString("<p class=\"note\">" + safe(d.Note, false) + "</p>\n")
	}
	for _, p := range d.Parts {
		p.write(w)
	}
	if len(d.Footer) > 0 {
		w.WriteString("<footer>\n")
		for _, l := range d.Footer {
			w.WriteString("<p>" + safe(l, false) + "</p>\n")
		}
		w.WriteString("</footer>\n")
	}
}

func (p Prose) write(w *writer) {
	for _, b := range p {
		if lines := strings.Split(strings.TrimSpace(b), "\n"); len(lines) > 0 && lines[0] != "" {
			w.block(lines)
		}
	}
	w.closeAll()
}

func (p Para) write(w *writer) {
	if p != "" {
		w.WriteString("<p>" + lines(string(p)) + "</p>\n")
	}
}

func (s Small) write(w *writer) {
	if s != "" {
		w.WriteString("<p class=\"small\">" + lines(string(s)) + "</p>\n")
	}
}

func (g Group) write(w *writer) {
	for _, p := range g {
		p.write(w)
	}
}

func (s Section) write(w *writer) {
	w.WriteString("<section" + anchor(s.ID) + ">\n<h2>" + text(s.Title) + "</h2>\n")
	if s.Lead != "" {
		w.WriteString("<p class=\"lead\">" + safe(s.Lead, false) + "</p>\n")
	}
	for _, p := range s.Parts {
		p.write(w)
	}
	w.WriteString("</section>\n")
}

func (b Box) write(w *writer) {
	if len(b.Items) == 0 {
		return
	}
	w.WriteString("<aside class=\"box\">\n<p class=\"box-title\">" + text(b.Title) + "</p>\n<ul>\n")
	for _, item := range b.Items {
		w.WriteString("<li>" + safe(item, false) + "</li>\n")
	}
	w.WriteString("</ul>\n</aside>\n")
}

func (t Table) write(w *writer) {
	if len(t.Rows) == 0 {
		return
	}
	w.WriteString("<figure class=\"table\">\n")
	if t.Caption != "" {
		w.WriteString("<figcaption>" + text(t.Caption) + "</figcaption>\n")
	}
	w.WriteString("<div class=\"scroll\"><table>\n")
	if len(t.Head) > 0 {
		w.WriteString("<thead><tr>")
		for i, h := range t.Head {
			w.WriteString("<th" + align(t.Align, i) + ">" + text(h) + "</th>")
		}
		w.WriteString("</tr></thead>\n")
	}
	w.WriteString("<tbody>\n")
	for r, row := range t.Rows {
		if r < len(t.Marked) && t.Marked[r] {
			w.WriteString("<tr class=\"marked\">")
		} else {
			w.WriteString("<tr>")
		}
		for i, cell := range row {
			w.WriteString("<td" + align(t.Align, i) + ">" + safe(cell, false) + "</td>")
		}
		w.WriteString("</tr>\n")
	}
	w.WriteString("</tbody>\n</table></div>\n")
	if t.Note != "" {
		w.WriteString("<p class=\"small\">" + safe(t.Note, false) + "</p>\n")
	}
	w.WriteString("</figure>\n")
}

func (b Bars) write(w *writer) {
	if len(b.Items) == 0 {
		return
	}
	// The zero line sits where the largest fall and the largest rise leave
	// it, so the bars on either side are drawn to the same scale.
	var neg, pos float64
	for _, it := range b.Items {
		neg = math.Max(neg, -it.Value)
		pos = math.Max(pos, it.Value)
	}
	span := neg + pos
	if span <= 0 {
		span = 1
	}
	zero := 100 * neg / span
	w.WriteString("<figure class=\"chart bars\">\n")
	if b.Title != "" {
		w.WriteString("<figcaption>" + text(b.Title) + "</figcaption>\n")
	}
	for _, it := range b.Items {
		width := 100 * math.Abs(it.Value) / span
		left, tone := zero, "up"
		if it.Value < 0 {
			left, tone = zero-width, "down"
		}
		w.WriteString(fmt.Sprintf("<div class=\"bar-row\"><span class=\"bar-label\">%s</span>"+
			"<span class=\"bar-track\"><span class=\"bar-zero\" style=\"left:%.2f%%\"></span>"+
			"<span class=\"bar %s\" style=\"left:%.2f%%;width:%.2f%%\"></span></span>"+
			"<span class=\"bar-value %s\">%s</span></div>\n",
			text(it.Label), zero, tone, left, math.Max(width, 0.6), tone, text(it.Text)))
	}
	if b.Note != "" {
		w.WriteString("<p class=\"small\">" + safe(b.Note, false) + "</p>\n")
	}
	w.WriteString("</figure>\n")
}

func (c Columns) write(w *writer) {
	if len(c.Items) == 0 {
		return
	}
	var top, bottom float64
	for _, it := range c.Items {
		top = math.Max(top, it.Value)
		bottom = math.Min(bottom, it.Value)
	}
	span := top - bottom
	if span <= 0 {
		span = 1
	}
	zero := 100 * top / span // from the top of the plot
	w.WriteString("<figure class=\"chart columns\">\n")
	if c.Title != "" {
		w.WriteString("<figcaption>" + text(c.Title) + "</figcaption>\n")
	}
	w.WriteString(fmt.Sprintf("<div class=\"col-plot\" style=\"--cols:%d\">\n", len(c.Items)))
	for _, it := range c.Items {
		height := 100 * math.Abs(it.Value) / span
		top, tone := zero-height, "up"
		if it.Value < 0 {
			top, tone = zero, "down"
		}
		w.WriteString(fmt.Sprintf("<div class=\"col\"><span class=\"col-zero\" style=\"top:%.2f%%\"></span>"+
			"<span class=\"col-bar %s\" style=\"top:%.2f%%;height:%.2f%%\"></span></div>\n",
			zero, tone, top, math.Max(height, 0.8)))
	}
	w.WriteString("</div>\n<div class=\"col-labels\" style=\"--cols:" + fmt.Sprint(len(c.Items)) + "\">")
	for _, it := range c.Items {
		w.WriteString("<span><b>" + text(it.Text) + "</b>" + text(it.Label) + "</span>")
	}
	w.WriteString("</div>\n")
	if c.Note != "" {
		w.WriteString("<p class=\"small\">" + safe(c.Note, false) + "</p>\n")
	}
	w.WriteString("</figure>\n")
}

func (r Range) write(w *writer) {
	if r.High <= r.Low {
		return
	}
	at := func(v float64) float64 {
		return math.Min(100, math.Max(0, 100*(v-r.Low)/(r.High-r.Low)))
	}
	w.WriteString("<figure class=\"chart range\">\n")
	if r.Title != "" {
		w.WriteString("<figcaption>" + text(r.Title) + "</figcaption>\n")
	}
	w.WriteString("<div class=\"range-track\">")
	for _, m := range r.Marks {
		if m.Value > 0 {
			w.WriteString(fmt.Sprintf("<span class=\"range-mark\" style=\"left:%.2f%%\" title=\"%s\"><span>%s</span></span>",
				at(m.Value), text(m.Label), text(m.Label)))
		}
	}
	w.WriteString(fmt.Sprintf("<span class=\"range-last\" style=\"left:%.2f%%\"></span></div>\n", at(r.Last)))
	w.WriteString("<div class=\"range-ends\"><span>Low " + text(r.LowText) + "</span><span class=\"range-now\">Now " +
		text(r.LastText) + "</span><span>High " + text(r.HighText) + "</span></div>\n")
	if r.Note != "" {
		w.WriteString("<p class=\"small\">" + safe(r.Note, false) + "</p>\n")
	}
	w.WriteString("</figure>\n")
}

func (f Facts) write(w *writer) {
	if len(f) == 0 {
		return
	}
	w.WriteString("<dl class=\"facts\">\n")
	for _, kv := range f {
		w.WriteString("<div><dt>" + text(kv[0]) + "</dt><dd>" + safe(kv[1], false) + "</dd></div>\n")
	}
	w.WriteString("</dl>\n")
}

func (f Field) write(w *writer) {
	if strings.TrimSpace(f.Body) == "" {
		return
	}
	w.WriteString("<div class=\"field\"><h4>" + text(f.Label) + "</h4><p>" + lines(f.Body) + "</p></div>\n")
}

func (c Chips) write(w *writer) {
	if len(c.Items) == 0 {
		return
	}
	w.WriteString("<p class=\"chips\">")
	if c.Label != "" {
		w.WriteString("<span class=\"chips-label\">" + text(c.Label) + "</span>")
	}
	for _, ch := range c.Items {
		w.WriteString("<span class=\"chip " + tone(ch.Tone) + "\">" + text(ch.Text) + "</span>")
	}
	w.WriteString("</p>\n")
}

func (c Card) write(w *writer) {
	w.WriteString("<article class=\"card\"" + anchor(c.ID) + ">\n<div class=\"card-head\">")
	if c.Badge != "" {
		w.WriteString("<span class=\"badge " + tone(c.Tone) + "\">" + text(c.Badge) + "</span>")
	}
	w.WriteString("<h3>" + text(c.Title))
	if c.Code != "" {
		w.WriteString(" <code>" + text(c.Code) + "</code>")
	}
	w.WriteString("</h3>")
	if len(c.Meta) > 0 {
		w.WriteString("<p class=\"meta\">" + text(strings.Join(c.Meta, " · ")) + "</p>")
	}
	w.WriteString("</div>\n")
	for _, p := range c.Parts {
		p.write(w)
	}
	w.WriteString("</article>\n")
}

func (c Chain) write(w *writer) {
	if len(c.Steps) == 0 {
		return
	}
	w.WriteString("<figure class=\"chain\">\n")
	if c.Title != "" {
		w.WriteString("<figcaption>" + text(c.Title) + "</figcaption>\n")
	}
	w.WriteString("<ol>\n")
	for i, s := range c.Steps {
		w.WriteString(fmt.Sprintf("<li><span class=\"step-n\">%d</span><span class=\"step-name\">%s</span>", i+1, text(s.Name)))
		if len(s.Items) > 0 {
			w.WriteString("<span class=\"step-items\">")
			for _, it := range s.Items {
				w.WriteString("<code>" + text(it) + "</code>")
			}
			w.WriteString("</span>")
		}
		w.WriteString("</li>\n")
	}
	w.WriteString("</ol>\n")
	if c.Note != "" {
		w.WriteString("<p class=\"small\">" + safe(c.Note, false) + "</p>\n")
	}
	w.WriteString("</figure>\n")
}

func (s Sources) write(w *writer) {
	if len(s.Links) == 0 {
		return
	}
	title := s.Title
	if title == "" {
		title = "Sources"
	}
	w.WriteString(fmt.Sprintf("<details class=\"sources\">\n<summary>%s <span class=\"count\">%d</span></summary>\n<ol>\n", text(title), len(s.Links)))
	for _, l := range s.Links {
		url := html.EscapeString(l.URL)
		if !strings.HasPrefix(l.URL, "https://") && !strings.HasPrefix(l.URL, "http://") {
			continue
		}
		n := ""
		if l.N > 0 {
			n = fmt.Sprintf("<span class=\"n\">%d</span>", l.N)
		}
		w.WriteString("<li>" + n + "<a href=\"" + url + "\" target=\"_blank\" rel=\"noopener noreferrer\">" + text(l.Title) + "</a>")
		if l.Source != "" {
			w.WriteString(" <span class=\"src\">" + text(l.Source) + "</span>")
		}
		w.WriteString("</li>\n")
	}
	w.WriteString("</ol>\n</details>\n")
}

// text escapes plain text for a page.
func text(s string) string { return html.EscapeString(s) }

// lines is Telegram HTML with its line breaks kept.
func lines(s string) string {
	parts := strings.Split(strings.TrimSpace(s), "\n")
	for i, p := range parts {
		parts[i] = safe(p, false)
	}
	return strings.Join(parts, "<br>\n")
}

func align(spec string, i int) string {
	if i < len(spec) && spec[i] == 'r' {
		return " class=\"num\""
	}
	return ""
}

func anchor(id string) string {
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return ""
		}
	}
	if id == "" {
		return ""
	}
	return " id=\"" + id + "\""
}

// tone is a colour's class, from the few a page has.
func tone(t string) string {
	switch t {
	case "up", "down", "buy", "sell", "hold", "high", "medium":
		return t
	}
	return "plain"
}
