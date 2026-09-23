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
// only place a channel reader will see them. See RUNBOOK.md.
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

	for _, group := range []struct {
		heading   string
		connected bool
	}{
		{"In the news", false},
		{"Connected to today's news", true},
	} {
		var blocks []string
		for _, idea := range ideas {
			if idea.Connected == group.connected {
				blocks = append(blocks, renderIdea(idea, cited))
			}
		}
		if len(blocks) == 0 {
			continue
		}
		segs = append(segs, segment{blocks: append([]string{divider + "\n<b>" + group.heading + "</b>"}, blocks...)})
	}
	return pack(segs)
}

func renderIdea(idea model.Idea, cited []model.Article) string {
	var b strings.Builder

	b.WriteString("• <b>" + escape(idea.Name) + "</b>")
	if s := idea.Symbol(); s != "" {
		b.WriteString(" <code>" + escape(s) + "</code>")
	}
	if idea.Quote != nil {
		b.WriteString(" · " + escape(idea.Quote.Move()) + " today")
	}

	b.WriteString("\n<b>" + escape(idea.Verdict) + "</b>")
	if idea.Confidence != "" {
		b.WriteString(" · " + escape(idea.Confidence) + " confidence")
	}
	if !idea.Accounts {
		b.WriteString(" · <i>no SEC accounts behind it</i>")
	}

	b.WriteString("\n<i>Why it is here:</i> " + linkCitations(escape(idea.Link), cited))
	for _, a := range idea.Articles {
		if n := citationNumber(a, cited); n > 0 && !strings.Contains(idea.Link, fmt.Sprintf("[%d]", n)) {
			b.WriteString(fmt.Sprintf(" <a href=\"%s\">[%d]</a>", escape(a.URL), n))
		}
	}

	if idea.Case != "" {
		b.WriteString("\n" + linkCitations(escape(idea.Case), cited))
	}
	if idea.Numbers != "" {
		b.WriteString("\n<i>Numbers:</i> " + escape(idea.Numbers))
	}
	if idea.Risk != "" {
		b.WriteString("\n<i>Risk:</i> " + escape(idea.Risk))
	}
	return b.String()
}
