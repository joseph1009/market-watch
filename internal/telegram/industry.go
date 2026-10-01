package telegram

import (
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// IndustryCompany is one company /industry suggests looking into, under the
// part of the industry it belongs to.
type IndustryCompany struct {
	Part, Name, Symbol, Why string
}

// industryNote and industryChannelNote head an industry's explanation. The
// companies are suggestions to read about, not verdicts, and the channel's
// copy says a model wrote it, as every verdict there does.
const (
	industryNote        = "<i>How the industry fits together, with listed companies to look into in each part: companies to read about, not recommendations. Send /analyse with a ticker to read one's accounts.</i>"
	industryChannelNote = "<i>How the industry fits together, with listed companies to look into in each part: companies to read about, not recommendations. " + disclosure + "</i>"
)

// RenderIndustry lays out /industry: the heading and its note, the
// explanation in sub-headings over bullets with its jargon linked, and the
// companies grouped by part, each with a checked ticker.
func RenderIndustry(topic, prose string, companies []IndustryCompany, opts IdeasOptions, terms []model.Term) []string {
	note := industryNote
	if opts.ForChannel {
		note = industryChannelNote
	}
	segs := []segment{{blocks: []string{
		fmt.Sprintf("<b>🧭 %s — how the industry fits together</b>\n%s", escape(capitalise(topic)), note),
	}}}
	if prose != "" {
		segs = append(segs, segment{blocks: linkTerms(paragraphs(prose), terms)})
	}

	if len(companies) > 0 {
		var order []string
		byPart := map[string][]string{}
		for _, c := range companies {
			if _, seen := byPart[c.Part]; !seen {
				order = append(order, c.Part)
			}
			byPart[c.Part] = append(byPart[c.Part], fmt.Sprintf("• %s <code>%s</code> — %s", escape(c.Name), escape(c.Symbol), escape(c.Why)))
		}
		blocks := []string{divider + "\n<b>🏢 COMPANIES TO LOOK INTO</b>\n<i>Every ticker checked against its exchange.</i>"}
		for _, part := range order {
			blocks = append(blocks, "<b>"+escape(part)+"</b>\n"+strings.Join(byPart[part], "\n"))
		}
		segs = append(segs, segment{blocks: blocks})
	}
	return pack(segs)
}

// capitalise raises the first letter: "robotics" heads as "Robotics".
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
