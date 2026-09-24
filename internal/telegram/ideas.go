package telegram

import (
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// IdeasOptions says who is reading. The two audiences get the same companies
// and the same verdicts, and differ only in the note above them: the owner
// knows what this section is and has /scorecard to check it against, while a
// channel reader may be seeing it for the first time, or forwarded, and needs
// telling that a model wrote it and that it is not advice to act on.
type IdeasOptions struct {
	ForChannel bool
}

// ownerNote and channelNote head the closer look for each audience.
//
// The channel note is not decoration. Verdicts published to other people are a
// different thing from verdicts kept for yourself, and the two things the
// policy on AI-written advice asks for are that readers are told a model wrote
// it and that nobody is told to act. Both are said here, because this is the
// only place a channel reader will see them. See docs/RUNBOOK.md.
const (
	ownerNote = "<i>Claude's verdicts on companies today's news bears on. " +
		"BUY and SELL mean better or worse than the S&amp;P 500 over twelve months. /scorecard shows how past verdicts have done.</i>"

	channelNote = "<i>Companies today's news bears on, researched and judged by Claude — an AI model, writing from public filings and share prices with nobody checking its work. " +
		"BUY and SELL mean it expects better or worse than the S&amp;P 500 over twelve months. " +
		"This is not financial advice and not a recommendation to buy or sell anything. It is often wrong. " +
		"Do your own research, and talk to someone licensed before you act on any of it.</i>"
)

// RenderIdeas lays out "worth a closer look": the companies the research found,
// each with its verdict and what it rests on.
//
// It is its own message, sent after the brief, for two reasons. The research
// takes minutes and must not hold the brief back. And it is a different kind of
// thing from the brief -- a judgment rather than a report -- so keeping them in
// separate messages means each can be headed for what it is.
//
// Citations use the brief's numbers, so [12] here is the [12] above.
func RenderIdeas(ideas []model.Idea, cited []model.Article, opts IdeasOptions) []string {
	if len(ideas) == 0 {
		return nil
	}

	note := ownerNote
	if opts.ForChannel {
		note = channelNote
	}
	segs := []segment{{blocks: []string{"<b>🔎 Worth a closer look</b>\n" + note}}}

	// The companies the reader follows come first: they are the ones a
	// verdict is most likely to be acted on, and the screen chose them only
	// where the move and the news did not fit.
	for _, group := range []struct {
		heading string
		in      func(model.Idea) bool
	}{
		{"Companies you follow", func(i model.Idea) bool { return i.Followed }},
		{"In the news", func(i model.Idea) bool { return !i.Followed && !i.Connected }},
		{"Connected to today's news", func(i model.Idea) bool { return !i.Followed && i.Connected }},
	} {
		var blocks []string
		for _, idea := range ideas {
			if !group.in(idea) {
				continue
			}
			// With a blank line between each part of a verdict, a blank line
			// alone no longer says where one company ends and the next begins.
			block := renderIdea(idea, cited)
			if len(blocks) > 0 {
				block = companyRule + "\n" + block
			}
			blocks = append(blocks, block)
		}
		if len(blocks) == 0 {
			continue
		}
		segs = append(segs, segment{blocks: append([]string{divider + "\n<b>" + group.heading + "</b>"}, blocks...)})
	}
	return pack(segs)
}

// companyRule separates one company from the next: shorter than the divider
// between groups, so the two breaks can be told apart.
const companyRule = "─────"

// verdictMarks lead each company's first line, so a reader scrolling twenty
// of them can find the BUYs and SELLs without reading.
var verdictMarks = map[string]string{model.Buy: "🟢", model.Sell: "🔴", model.Hold: "⚪"}

// renderIdea lays out one company, each part of its verdict a paragraph of
// its own. Run together as one block, twenty of them read as a wall of text,
// which is how the first closer look on the new verdicts arrived.
func renderIdea(idea model.Idea, cited []model.Article) string {
	var head strings.Builder
	if mark := verdictMarks[idea.Verdict]; mark != "" {
		head.WriteString(mark + " ")
	}
	head.WriteString("<b>" + escape(idea.Name) + "</b>")
	if s := idea.Symbol(); s != "" {
		head.WriteString(" <code>" + escape(s) + "</code>")
	}
	// Today's move, unless the verdict gives it below with the longer ones.
	if idea.Quote != nil && idea.Moved == "" {
		head.WriteString(" · " + escape(idea.Quote.Move()) + " today")
	}

	head.WriteString("\n<b>" + escape(idea.Verdict) + "</b>")
	if idea.Confidence != "" {
		head.WriteString(" · " + escape(idea.Confidence) + " confidence")
	}
	if word := reactionWord(idea.Reaction); word != "" {
		head.WriteString(" · " + word)
	}
	if !idea.Accounts {
		head.WriteString(" · <i>no SEC accounts behind it</i>")
	}
	parts := []string{head.String()}

	// What changed says why the company is here, in the verdict's own
	// numbers; the research's reason is shown only where there is no verdict
	// to say it, rather than a second time in other words. The stories behind
	// it follow either way.
	label, why := "What changed:", idea.Changed
	if why == "" {
		label, why = "Why it is here:", idea.Link
	}
	line := "<i>" + label + "</i> " + linkCitations(escape(why), cited)
	for _, a := range idea.Articles {
		if n := citationNumber(a, cited); n > 0 && !strings.Contains(why, fmt.Sprintf("[%d]", n)) {
			line += fmt.Sprintf(" <a href=\"%s\">[%d]</a>", escape(a.URL), n)
		}
	}
	parts = append(parts, line)

	// The move and whether it was justified are one thought, and stay
	// together.
	var move []string
	if idea.Moved != "" {
		move = append(move, "<i>The move:</i> "+escape(idea.Moved))
	}
	if idea.Reaction != "" {
		move = append(move, "<i>Justified?</i> "+linkCitations(escape(idea.Reaction), cited))
	}
	if len(move) > 0 {
		parts = append(parts, strings.Join(move, "\n"))
	}

	if idea.Case != "" {
		parts = append(parts, "<i>The case:</i> "+linkCitations(escape(idea.Case), cited))
	}
	if n := renderNumbers(idea.Numbers); n != "" {
		parts = append(parts, n)
	}
	if idea.Risk != "" {
		parts = append(parts, "<i>Risk:</i> "+linkCitations(escape(idea.Risk), cited))
	}
	return strings.Join(parts, "\n\n")
}

// renderNumbers lists the figures a verdict rests on one to a line, as the
// prompt asks for them separated by semicolons. A reply that ran them into a
// sentence stays on one line.
func renderNumbers(numbers string) string {
	var items []string
	for _, item := range strings.Split(numbers, ";") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, "• "+escape(item))
		}
	}
	switch len(items) {
	case 0:
		return ""
	case 1:
		return "<i>Numbers:</i> " + strings.TrimPrefix(items[0], "• ")
	}
	return "<i>Numbers</i>\n" + strings.Join(items, "\n")
}

// reactionWord is the judgment at the head of the REACTION field, for the
// verdict line: "underreacted", "overreacted" or "matched", or nothing when
// the field opens some other way.
func reactionWord(reaction string) string {
	words := strings.Fields(reaction)
	if len(words) == 0 {
		return ""
	}
	first := strings.ToLower(strings.Trim(words[0], ".,:;-–—"))
	switch first {
	case "overreacted", "underreacted", "matched":
		return first
	}
	return ""
}
