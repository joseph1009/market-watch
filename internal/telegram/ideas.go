package telegram

import (
	"fmt"
	"strings"
	"time"

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
	ownerNote = "<i>Claude's verdicts on companies found from the market's numbers and the news. " +
		"BUY and SELL mean at least 5 points better or worse than the S&amp;P 500 over twelve months, in US dollars. /scorecard shows how past verdicts have done.</i>"

	channelNote = "<i>Companies found from market data and the news, researched and judged by Claude — an AI model, writing from public filings and share prices with nobody checking its work. " +
		"BUY and SELL mean it expects the share to beat or trail the S&amp;P 500 by at least 5 percentage points over twelve months. " +
		"This is not financial advice and not a recommendation to buy or sell anything. It is often wrong. " +
		"Do your own research, and talk to someone licensed before you act on any of it.</i>"
)

// ThemeView is one of the week's themes as the message shows it.
type ThemeView struct {
	// Kind is "popular", a theme the market has been paying for, or
	// "early", one whose business is growing before its shares have.
	Kind string
	Name string

	// Figures is its numbers in a line; Driving, PricedIn and Value what the
	// research found: what drives it, what the market has paid for, and
	// where the value is.
	Figures, Driving, PricedIn, Value string

	Ideas []model.Idea
}

// EarlierPick is a pick from the last weeks, and how it has done since.
type EarlierPick struct {
	Name, Symbol, Verdict, Theme string
	At                           time.Time

	// Ahead is how far it has gone the way its verdict said, against the
	// index, in percentage points, where Priced.
	Ahead  float64
	Priced bool
}

// Picks is one closer look: the week's themes on the day they run, the
// day's reactions, and on the themes' day the earlier picks.
type Picks struct {
	Themes    []ThemeView
	Reactions []model.Idea
	Earlier   []EarlierPick
}

// Empty reports whether there is nothing to show.
func (p Picks) Empty() bool {
	n := len(p.Reactions)
	for _, t := range p.Themes {
		n += len(t.Ideas)
	}
	return n == 0
}

// RenderPicks lays out the closer look: the week's themes, each with what
// the research found and the companies picked under it, then the day's
// reactions to news, then how the earlier picks have done.
//
// It is its own message, sent after the brief, for two reasons. The research
// takes minutes and must not hold the brief back. And it is a different kind
// of thing from the brief -- a judgment rather than a report -- so keeping
// them in separate messages means each can be headed for what it is.
//
// Citations use the brief's numbers, so [12] here is the [12] above.
func RenderPicks(p Picks, cited []model.Article, opts IdeasOptions, where *time.Location) []string {
	if p.Empty() {
		return nil
	}
	note := ownerNote
	if opts.ForChannel {
		note = channelNote
	}
	weekly := false
	for _, t := range p.Themes {
		weekly = weekly || len(t.Ideas) > 0
	}
	const reactionsLine = "<i>Shares that moved several times their usual on the last session, where the move and the news do not fit.</i>"
	head := "<b>🔎 This week's picks</b>\n" + note
	if !weekly {
		// A day of reactions alone is headed once, for what it is.
		head = "<b>🔎 Reacting to the news</b>\n" + note + "\n\n" + reactionsLine
	}
	segs := []segment{{blocks: []string{head}}}

	for _, t := range p.Themes {
		if len(t.Ideas) == 0 {
			continue
		}
		label := "Popular"
		if t.Kind == "early" {
			label = "Early"
		}
		head := []string{divider + "\n<b>" + escape(t.Name) + "</b> · <i>" + label + "</i>"}
		for _, part := range []struct{ label, text string }{
			{"The numbers:", t.Figures},
			{"What's driving it:", t.Driving},
			{"Priced in:", t.PricedIn},
			{"Where the value is:", t.Value},
		} {
			if strings.TrimSpace(part.text) != "" {
				head = append(head, "<i>"+part.label+"</i> "+escape(part.text))
			}
		}
		blocks := []string{strings.Join(head, "\n\n")}
		for _, idea := range t.Ideas {
			blocks = append(blocks, companyRule+"\n"+renderIdea(idea, cited))
		}
		segs = append(segs, segment{blocks: blocks})
	}

	if len(p.Reactions) > 0 {
		var blocks []string
		if weekly {
			blocks = append(blocks, divider+"\n<b>Reacting to the news</b>\n"+reactionsLine)
		}
		for i, idea := range p.Reactions {
			block := renderIdea(idea, cited)
			if i > 0 {
				block = companyRule + "\n" + block
			}
			blocks = append(blocks, block)
		}
		segs = append(segs, segment{blocks: blocks})
	}

	if len(p.Earlier) > 0 {
		lines := []string{divider + "\n<b>Earlier picks</b>"}
		for _, e := range p.Earlier {
			line := fmt.Sprintf("• %s <code>%s</code> · %s on %s", escape(e.Name), escape(e.Symbol), escape(e.Verdict), e.At.In(where).Format("2 Jan"))
			if e.Priced {
				line += fmt.Sprintf(" · %+.1f points the way called", e.Ahead)
			} else {
				line += " · not yet traded since"
			}
			lines = append(lines, line)
		}
		lines = append(lines, "<i>Against the S&amp;P 500 since the first open after each verdict. A pick is not written up again for eight weeks unless its verdict changes.</i>")
		segs = append(segs, segment{blocks: []string{strings.Join(lines, "\n")}})
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
	// The last session's move, unless the verdict gives it below with the
	// longer ones. Not "today": the look is read before the US open, when a
	// US share's last session was yesterday's.
	if idea.Quote != nil && idea.Moved == "" {
		head.WriteString(" · " + escape(idea.Quote.Move()) + " last session")
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
	if idea.Before != "" {
		head.WriteString(" · <i>was " + escape(idea.Before) + "</i>")
	}
	parts := []string{head.String()}

	// What changed says why a reaction is here, in the verdict's own numbers;
	// a pick says where it sits in its theme. The research's reason is shown
	// only where there is no verdict to say it, rather than a second time in
	// other words. The stories behind it follow either way.
	label, why := "What changed:", idea.Changed
	if idea.Kind == model.IdeaTheme {
		label, why = "Where it fits:", idea.Link
	}
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

	if idea.Value != "" {
		parts = append(parts, "<i>The price:</i> "+escape(idea.Value))
	}
	if len(idea.Flags) > 0 {
		parts = append(parts, "<i>Warning signs:</i> "+escape(strings.Join(idea.Flags, "; ")))
	}
	if idea.Case != "" {
		parts = append(parts, "<i>The case:</i> "+linkCitations(escape(idea.Case), cited))
	}

	// What could prove the case soon, and how much a change is worth: two
	// short lines that read as one paragraph.
	var ahead []string
	if idea.Catalyst != "" {
		ahead = append(ahead, "<i>Catalyst:</i> "+linkCitations(escape(idea.Catalyst), cited))
	}
	if idea.Sensitivity != "" {
		ahead = append(ahead, "<i>Sensitivity:</i> "+escape(idea.Sensitivity))
	}
	if len(ahead) > 0 {
		parts = append(parts, strings.Join(ahead, "\n"))
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
// verdict line: "underreacted", "overreacted", "matched", or "not yet traded"
// for news that came after the last price, or nothing when the field opens
// some other way.
func reactionWord(reaction string) string {
	words := strings.Fields(reaction)
	if len(words) == 0 {
		return ""
	}
	if len(words) >= 3 && strings.EqualFold(words[0], "not") && strings.EqualFold(words[1], "yet") &&
		strings.EqualFold(strings.Trim(words[2], ".,:;-–—"), "traded") {
		return "not yet traded"
	}
	first := strings.ToLower(strings.Trim(words[0], ".,:;-–—"))
	switch first {
	case "overreacted", "underreacted", "matched":
		return first
	}
	return ""
}
