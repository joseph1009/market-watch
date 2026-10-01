package telegram

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/joseph1009/market-watch/internal/model"
)

// Summary is the chat's copy of something read in full as a web page: what
// fits on one screen, and the label on the button that opens the rest.
//
// The page carries everything the messages would have, so the summary only
// has to say whether opening it is worth it, and what to know if it is not
// opened at all.
type Summary struct {
	Text   string
	Button string
}

const (
	// maxSummaryReleases and maxSummaryResults bound the brief's "next 24
	// hours": the releases and results that matter most, not the calendar.
	maxSummaryReleases = 4
	maxSummaryResults  = 3
)

// A summary is labelled blocks: a heading in bold capitals over each, a
// blank line between blocks, and a blank line between bullets, which is what
// keeps a list readable on a phone. One emoji, on the title: the owner found
// one on every heading, bullet and line too many (2026-10-01). A verdict's
// coloured dot stays, being a reading aid rather than decoration.

// block is one labelled part of a summary.
func block(label, body string) string {
	return "<b>" + label + "</b>\n" + body
}

// spaced joins bullets with a blank line between them.
func spaced(bullets []string) string {
	return strings.Join(bullets, "\n\n")
}

// at is a moment as the summaries write it, after what happens then:
// "Market Watch @ 09:46 Singapore time, Thu 1 Oct".
func at(t time.Time, display *time.Location) string {
	return t.In(display).Format("15:04") + " " + placeName(display) + " time, " + t.In(display).Format("Mon 2 Jan")
}

// BriefSummary is the brief on one screen: the day in a line, the writer's
// few bullets, what lands in the next day, and what the page holds.
func BriefSummary(rep model.Report, opts Options) Summary {
	display := opts.Display
	if display == nil {
		display = time.UTC
	}
	parts := []string{"📊 <b>Market Watch</b> @ " + escape(at(rep.GeneratedAt, display))}

	if line := headline(rep.Overview); line != "" {
		parts = append(parts, "<b>"+line+"</b>")
	}
	if points := summaryBullets(rep.Summary); len(points) > 0 {
		parts = append(parts, block("IN SHORT", spaced(points)))
	}
	if next := nextDay(rep.Calendar, rep.GeneratedAt, display); len(next) > 0 {
		parts = append(parts, block("NEXT 24 HOURS", strings.Join(next, "\n")))
	}
	if inside := contents(rep); inside != "" {
		parts = append(parts, block("IN THE FULL BRIEF", inside))
	}
	return Summary{Text: strings.Join(parts, "\n\n"), Button: "📖 Read the full brief"}
}

// contents names what the page holds: the overview's topics, then the
// sectors.
func contents(rep model.Report) string {
	var lines []string
	if topics := subheadings(rep.Overview); len(topics) > 0 {
		lines = append(lines, "<i>Overview:</i> "+escape(strings.Join(topics, " · ")))
	}
	var sectors []string
	for _, s := range rep.Sections {
		sectors = append(sectors, escape(s.GroupName))
	}
	if len(sectors) > 0 {
		lines = append(lines, "<i>Sectors:</i> "+strings.Join(sectors, " · "))
	}
	return strings.Join(lines, "\n")
}

// PicksSummary is the closer look on one screen: how many companies and
// which way, then each company with its verdict, so the reader can see what
// there is before opening the cases.
func PicksSummary(p Picks, opts IdeasOptions) Summary {
	title := "Reacting to the news"
	var all []model.Idea
	var parts []string
	for _, t := range p.Themes {
		if len(t.Ideas) == 0 {
			continue
		}
		title = "This week's picks"
		kind := "POPULAR"
		if t.Kind == "early" {
			kind = "EARLY"
		}
		var picks []string
		for _, idea := range t.Ideas {
			picks = append(picks, pickLines(idea))
		}
		parts = append(parts, block(escape(strings.ToUpper(t.Name))+" · "+kind, spaced(picks)))
		all = append(all, t.Ideas...)
	}
	if len(p.Reactions) > 0 {
		var picks []string
		for _, idea := range p.Reactions {
			picks = append(picks, pickLines(idea))
		}
		body := spaced(picks)
		if len(parts) > 0 {
			body = block("REACTING TO THE NEWS", body)
		}
		parts = append(parts, body)
		all = append(all, p.Reactions...)
	}
	head := "🔎 <b>" + title + "</b>\n" + picksNote(opts) + "\n\n" + tally(all)
	return Summary{Text: head + "\n\n" + strings.Join(parts, "\n\n"), Button: "📖 Read the cases"}
}

// tally counts the companies and their verdicts: "2 companies · 🟢 1 BUY
// · 🔴 1 SELL".
func tally(ideas []model.Idea) string {
	n := map[string]int{}
	for _, idea := range ideas {
		n[idea.Verdict]++
	}
	line := fmt.Sprintf("<b>%d companies</b>", len(ideas))
	if len(ideas) == 1 {
		line = "<b>1 company</b>"
	}
	for _, v := range []string{model.Buy, model.Hold, model.Sell} {
		if n[v] > 0 {
			line += fmt.Sprintf(" · %s %d %s", verdictMarks[v], n[v], v)
		}
	}
	return line
}

// pickLines is one company: its name and ticker, then its verdict.
func pickLines(idea model.Idea) string {
	head := "• "
	if mark := verdictMarks[idea.Verdict]; mark != "" {
		head = mark + " "
	}
	head += "<b>" + escape(idea.Name) + "</b>"
	if s := idea.Symbol(); s != "" {
		head += " <code>" + escape(s) + "</code>"
	}
	verdict := "<b>" + escape(idea.Verdict) + "</b>"
	if idea.Confidence != "" {
		verdict += " · " + escape(idea.Confidence) + " confidence"
	}
	if word := reactionWord(idea.Reaction); word != "" {
		verdict += " · " + word
	}
	return head + "\n" + verdict
}

// AnalysisSummary is the analysis's verdict and the reasons for it, which
// is what a reader looks for first; the accounts are on the page.
func AnalysisSummary(heading string, v AnalysisVerdict, opts IdeasOptions) Summary {
	text := "🔬 <b>" + escape(heading) + "</b>"
	if v.Verdict != "" {
		text += "\n\n" + block("THE VERDICT", verdictLine(v)+"\n"+analysisNote(opts))
		if why := firstGroup(v.Body); why != "" {
			text += "\n\n" + why
		}
	}
	return Summary{Text: text, Button: "📖 Read the full analysis"}
}

// verdictLine is "⚪ HOLD · low confidence".
func verdictLine(v AnalysisVerdict) string {
	line := "<b>" + escape(v.Verdict) + "</b>"
	if mark := verdictMarks[v.Verdict]; mark != "" {
		line = mark + " " + line
	}
	if v.Confidence != "" {
		line += " · " + escape(v.Confidence) + " confidence"
	}
	return line
}

// IndustrySummary is the industry's big picture and the parts it is made
// of, with how many companies the page suggests in each.
func IndustrySummary(topic, prose string, companies []IndustryCompany, opts IdeasOptions) Summary {
	text := fmt.Sprintf("🧭 <b>%s — how the industry fits together</b>\n%s", escape(capitalise(topic)), industryNoteFor(opts))
	if first := firstGroup(prose); first != "" {
		text += "\n\n" + first
	}
	if len(companies) > 0 {
		var parts []string
		for _, p := range partsOf(companies) {
			parts = append(parts, fmt.Sprintf("• %s — %d", escape(p.Name), len(p.Companies)))
		}
		total := fmt.Sprintf("%d COMPANIES", len(companies))
		if len(companies) == 1 {
			total = "1 COMPANY"
		}
		text += "\n\n" + block(total+" TO LOOK INTO", strings.Join(parts, "\n"))
	}
	return Summary{Text: text, Button: "📖 Read the explanation"}
}

// citations finds the brief's "[12]" and "[3][7]", which a summary leaves
// out: its links are on the page.
var citations = regexp.MustCompile(`\s*(\[\d{1,4}\])+`)

// inline is one line of model prose for a summary: escaped, its highlight in
// bold, its citations gone.
func inline(s string) string {
	return highlight(escape(strings.TrimSpace(citations.ReplaceAllString(s, ""))))
}

// headline is the overview's opening line, when it opens with one rather
// than with a sub-heading.
func headline(overview string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(overview), "\n")
	first = strings.TrimSpace(first)
	if first == "" || strings.HasPrefix(first, "#") || strings.HasPrefix(first, "- ") {
		return ""
	}
	// Shown in bold whole, so a highlight inside it is dropped.
	return strings.NewReplacer("<b>", "", "</b>", "").Replace(inline(first))
}

// summaryBullets are the IN SHORT block's bullets, without an emoji a
// model opened one with.
func summaryBullets(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "-•*"))
		line = withoutEmoji(line)
		if line == "" {
			continue
		}
		out = append(out, "• "+inline(line))
	}
	return out
}

// withoutEmoji drops the emoji a line opens with, "📉 Yields rose" being
// "Yields rose".
func withoutEmoji(s string) string {
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if r < 0x2190 && r != 0xFE0F && r != 0x200D {
			break
		}
		s = s[size:]
	}
	return strings.TrimSpace(s)
}

// subheadings are the topics the overview is written under, without their
// emoji.
func subheadings(overview string) []string {
	var out []string
	for _, line := range strings.Split(overview, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, subheadingMarker) {
			out = append(out, withoutEmoji(strings.ReplaceAll(strings.TrimSpace(line[len(subheadingMarker):]), "**", "")))
		}
	}
	return out
}

// firstGroup is the first sub-heading and its bullets, or the first bullets
// of prose that has none, spaced as the brief's are.
func firstGroup(prose string) string {
	var heading string
	var bullets []string
	for _, line := range strings.Split(prose, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, subheadingMarker):
			if heading != "" || len(bullets) > 0 {
				return group(heading, bullets)
			}
			heading = withoutEmoji(strings.ReplaceAll(strings.TrimSpace(line[len(subheadingMarker):]), "**", ""))
		case strings.HasPrefix(line, "- "):
			bullets = append(bullets, "• "+inline(line[2:]))
		}
	}
	return group(heading, bullets)
}

func group(heading string, bullets []string) string {
	if heading == "" {
		return spaced(bullets)
	}
	return block(escape(heading), spaced(bullets))
}

// nextDay is what the calendar has in the next twenty-four hours, a line
// each, the time after what happens: the releases that move markets most,
// high impact first and then the US's medium ones, and the results still to
// come, a followed company's first.
func nextDay(cal model.Calendar, now time.Time, display *time.Location) []string {
	var lines []string
	for _, e := range dueReleases(cal, now, maxSummaryReleases) {
		line := fmt.Sprintf("• %s @ <b>%s</b>", escape(e.Title), e.At.In(display).Format("15:04"))
		if e.Forecast != "" {
			line += " — forecast " + escape(e.Forecast)
			if e.Previous != "" {
				line += ", previous " + escape(e.Previous)
			}
		}
		lines = append(lines, line)
	}
	for _, r := range dueResults(cal, now, maxSummaryResults) {
		line := fmt.Sprintf("• %s <code>%s</code> results @ %s", escape(shortName(r.Name)), escape(r.Symbol), resultsTime(r, display))
		if r.Forecast != "" {
			line += " — expected " + escape(r.Forecast) + " a share"
		}
		lines = append(lines, line)
	}
	return lines
}

// dueReleases are the releases in the next day worth a reader's attention,
// at most n, in time order.
func dueReleases(cal model.Calendar, now time.Time, n int) []model.Release {
	until := now.Add(24 * time.Hour)
	var events []model.Release
	for _, e := range cal.Events {
		if e.At.Before(now) || !e.At.Before(until) {
			continue
		}
		if e.Impact == "High" || (e.Impact == "Medium" && e.Country == "USD") {
			events = append(events, e)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if hi, hj := events[i].Impact == "High", events[j].Impact == "High"; hi != hj {
			return hi
		}
		return events[i].At.Before(events[j].At)
	})
	events = events[:min(len(events), n)]
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	return events
}

// dueResults are the results not yet out that will be within the day, a
// followed company's first, at most n.
func dueResults(cal model.Calendar, now time.Time, n int) []model.Results {
	until := now.Add(24 * time.Hour)
	var results []model.Results
	for _, r := range cal.Earnings {
		if at := reportedBy(r); at.After(now) && at.Before(until) {
			results = append(results, r)
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Followed && !results[j].Followed })
	return results[:min(len(results), n)]
}

// resultsTime is when results are out, in the reader's time: "<b>04:00</b>,
// after the US close", or "<b>by 21:30</b>, before the US open".
func resultsTime(r model.Results, display *time.Location) string {
	clock := reportedBy(r).In(display).Format("15:04")
	switch when := strings.ToLower(r.When); {
	case strings.Contains(when, "before"):
		return "<b>by " + clock + "</b>, before the US open"
	case strings.Contains(when, "after"):
		return "<b>" + clock + "</b>, after the US close"
	}
	return "<b>" + clock + "</b> or earlier"
}

// reportedBy is when a company's results are out, from the day Nasdaq lists
// them on: by New York's open when due before it, otherwise by its close.
// Results already out are news, not something coming up: a brief asked for
// in New York's evening must not list that afternoon's as still to come.
func reportedBy(r model.Results) time.Time {
	hour, minute := 16, 0
	if strings.Contains(strings.ToLower(r.When), "before") {
		hour, minute = 9, 30
	}
	return time.Date(r.Day.Year(), r.Day.Month(), r.Day.Day(), hour, minute, 0, 0, r.Day.Location())
}

// placeName is a timezone as a reader names it: "Asia/Singapore" is
// Singapore.
func placeName(loc *time.Location) string {
	name := loc.String()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ReplaceAll(name, "_", " ")
}

// Fits reports whether a summary fits in one message.
func Fits(text string) bool { return runeLen(text) <= maxMessageRunes }
