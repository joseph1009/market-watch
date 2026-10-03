package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The brief on one screen: the day in a line, the model's short bullets
// without their citations, and the releases and results due in the next day,
// a followed company's first. Results already out are not "coming up". One
// emoji, on the title, and each time after what happens at it (the owner's
// asks, 2026-10-01).
func TestTheBriefsSummaryFitsOnOneScreen(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	sg, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 10, 1, 7, 30, 0, 0, ny)
	rep := model.Report{
		GeneratedAt: now,
		Summary:     "- 📉 Long yields hit **5.55%**, a 2002 high [1][53].\n- Micron reports after the close [87].",
		Overview:    "Yields hit 2002 highs; **PCE** and Micron test it [12].\n\n### 🏦 Bonds\n- The 30-year rose [1].",
		Calendar: model.Calendar{
			Events: []model.Release{
				{At: now.Add(time.Hour), Country: "USD", Title: "Core PCE Price Index m/m", Impact: "High", Forecast: "0.3%", Previous: "0.2%"},
				{At: now.Add(2 * time.Hour), Country: "USD", Title: "Pending Home Sales", Impact: "Medium"},
				{At: now.Add(30 * time.Hour), Country: "USD", Title: "Non-Farm Payrolls", Impact: "High"},
			},
			Earnings: []model.Results{
				{Day: time.Date(2026, 10, 1, 0, 0, 0, 0, ny), Symbol: "NKE", Name: "Nike, Inc.", When: "after the close"},
				{Day: time.Date(2026, 10, 1, 0, 0, 0, 0, ny), Symbol: "MU", Name: "Micron Technology, Inc.", When: "after the close", Forecast: "$31.41", Followed: true},
				{Day: time.Date(2026, 10, 2, 0, 0, 0, 0, ny), Symbol: "TOMORROW", Name: "Later Co"},
				{Day: time.Date(2026, 9, 30, 0, 0, 0, 0, ny), Symbol: "DONE", Name: "Reported Co", When: "after the close"},
			},
		},
	}
	s := BriefSummary(rep, Options{Display: sg})
	for _, want := range []string{
		"📊 <b>Market Watch</b> @ 19:30 Singapore time, Thu 1 Oct",
		"\n\n<b>Yields hit 2002 highs; PCE and Micron test it.</b>",
		"<b>IN SHORT</b>\n• Long yields hit <b>5.55%</b>, a 2002 high.\n\n• Micron reports after the close.",
		"<b>NEXT 24 HOURS</b>\n• Core PCE Price Index m/m @ <b>20:30</b> — forecast 0.3%, previous 0.2%",
		"• Pending Home Sales @ <b>21:30</b>",
		"• Micron Technology <code>MU</code> results @ <b>04:00</b>, after the US close — expected $31.41 a share",
	} {
		if !strings.Contains(s.Text, want) {
			t.Errorf("summary is missing %q:\n%s", want, s.Text)
		}
	}
	for _, unwanted := range []string{"[1]", "📉", "🔴", "🇺🇸", "🏦", "Non-Farm Payrolls", "TOMORROW", "DONE", "30-year"} {
		if strings.Contains(s.Text, unwanted) {
			t.Errorf("summary carries %q:\n%s", unwanted, s.Text)
		}
	}
	if strings.Index(s.Text, "MU") > strings.Index(s.Text, "NKE") {
		t.Errorf("a followed company's results should come first:\n%s", s.Text)
	}
	if s.Button == "" || !Fits(s.Text) {
		t.Errorf("button %q, %d runes", s.Button, runeLen(s.Text))
	}

	// A reply without the bullets still says what the brief covers.
	rep.Summary = ""
	if s := BriefSummary(rep, Options{Display: sg}); strings.Contains(s.Text, "IN SHORT") || !strings.Contains(s.Text, "<i>Overview:</i> Bonds") {
		t.Errorf("fallback summary:\n%s", s.Text)
	}
}

// The closer look as one line a company, under the note for its reader.
func TestThePicksSummaryIsALineACompany(t *testing.T) {
	p := Picks{Reactions: []model.Idea{
		{Name: "Warby Parker", Ticker: "WRBY", Exchange: "US", Verdict: model.Sell, Confidence: "low", Reaction: "overreacted — a rival's news"},
		{Name: "NESR", Ticker: "NESR", Exchange: "US", Verdict: model.Buy, Confidence: "medium"},
	}}
	owner := PicksSummary(p, IdeasOptions{})
	for _, want := range []string{"🔎 <b>Reacting to the news</b>", "<b>2 companies</b> · 🟢 1 BUY · 🔴 1 SELL", "🔴 <b>Warby Parker</b> 🇺🇸 <code>WRBY</code>\n<b>SELL</b> · low confidence · overreacted", "🟢 <b>NESR</b>"} {
		if !strings.Contains(owner.Text, want) {
			t.Errorf("summary is missing %q:\n%s", want, owner.Text)
		}
	}
	if strings.Contains(owner.Text, disclosure) {
		t.Error("the owner's summary carries the channel's note")
	}
	if channel := PicksSummary(p, IdeasOptions{ForChannel: true}); !strings.Contains(channel.Text, disclosure) {
		t.Errorf("the channel's summary is missing its note:\n%s", channel.Text)
	}
}

const groundedProse = `THE BUSINESS

### What it sells
- Rides and food delivery.

THE CASE FOR IT

### In the business
- Leads ride-hailing in six countries.
- Second point.

### In the numbers
- Margin is **1.9%** [3].

### What follows
- Scale lowers the cost of each ride.

THE CASE AGAINST IT

### In the business
- Rivals subsidise rides.

### In the numbers
- HBM
- Cash burn in lending.

WHAT WOULD SETTLE IT

### The question
- November's cash flow.`

// An analysis's own short summary is the summary: what the company does,
// who buys it, what is coming, a point for and against, then the verdict.
// The case for and against, and the verdict's reasons, are left to the page.
func TestTheAnalysisSummaryIsItsOwnShortSummary(t *testing.T) {
	v := AnalysisVerdict{Verdict: model.Hold, Confidence: "low", Body: "### Why\n- Thin margin.\n\n### What would change it\n- Lending losses."}
	short := "### What it does\n- Rides and food delivery in an app [2].\n\n### Who buys it\n- Commuters and diners across Southeast Asia.\n\n" +
		"### What is coming\n- Results on 12 November. Watch whether lending losses grow.\n\n" +
		"### For and against\n- For: it leads in **six countries**.\n- Against: rivals subsidise rides."
	s := AnalysisSummary("GRAB · Grab Holdings", v, short, groundedProse, IdeasOptions{})
	want := "🔬 <b>GRAB · Grab Holdings</b>\n\n" +
		"<b>WHAT IT DOES</b>\n• Rides and food delivery in an app.\n\n" +
		"<b>WHO BUYS IT</b>\n• Commuters and diners across Southeast Asia.\n\n" +
		"<b>WHAT IS COMING</b>\n• Results on 12 November. Watch whether lending losses grow.\n\n" +
		"<b>FOR AND AGAINST</b>\n• <b>For:</b> it leads in <b>six countries</b>.\n\n• <b>Against:</b> rivals subsidise rides.\n\n" +
		"<b>THE VERDICT</b>\n⚪ <b>HOLD</b> · low confidence\n"
	if !strings.HasPrefix(s.Text, want) {
		t.Errorf("summary:\n%s\nwant it to start:\n%s", s.Text, want)
	}
	for _, not := range []string{"CASE FOR", "Thin margin", "Leads ride-hailing"} {
		if strings.Contains(s.Text, not) {
			t.Errorf("summary carries %q:\n%s", not, s.Text)
		}
	}
}

// Without its own summary, an analysis's summary is the best point of each
// group of the case for and against, then the verdict in a line with its note.
func TestTheAnalysisSummaryIsTheCaseForAndAgainst(t *testing.T) {
	v := AnalysisVerdict{Verdict: model.Hold, Confidence: "low", Body: "### Why\n- Thin margin.\n\n### What would change it\n- Lending losses."}
	s := AnalysisSummary("GRAB — the case for and against", v, "", groundedProse, IdeasOptions{ForChannel: true})
	want := "🔬 <b>GRAB — the case for and against</b>\n\n" +
		"<b>THE CASE FOR IT</b>\n• Leads ride-hailing in six countries.\n\n• Margin is <b>1.9%</b>.\n\n• Scale lowers the cost of each ride.\n\n" +
		"<b>THE CASE AGAINST IT</b>\n• Rivals subsidise rides.\n\n• HBM\n\n" +
		"<b>THE VERDICT</b>\n⚪ <b>HOLD</b> · low confidence\n"
	if !strings.HasPrefix(s.Text, want) {
		t.Errorf("summary:\n%s\nwant it to start:\n%s", s.Text, want)
	}
	if !strings.Contains(s.Text, disclosure) {
		t.Errorf("the channel's summary lost its note:\n%s", s.Text)
	}
	for _, not := range []string{"Second point", "Cash burn", "November", "Thin margin", "Rides and food"} {
		if strings.Contains(s.Text, not) {
			t.Errorf("summary carries %q:\n%s", not, s.Text)
		}
	}
}

// Prose without the case sections falls back to the verdict's reasons.
func TestAnAnalysisSummaryWithoutTheCaseGivesTheVerdictsReasons(t *testing.T) {
	v := AnalysisVerdict{Verdict: model.Hold, Confidence: "low", Body: "### Why\n- Margin is **1.9%** [3].\n\n### What would change it\n- November's cash flow."}
	s := AnalysisSummary("GRAB — the case for and against", v, "", "THE BUSINESS\n- Rides.", IdeasOptions{})
	for _, want := range []string{"<b>THE VERDICT</b>", "⚪ <b>HOLD</b> · low confidence", "<b>Why</b>\n• Margin is <b>1.9%</b>."} {
		if !strings.Contains(s.Text, want) {
			t.Errorf("summary is missing %q:\n%s", want, s.Text)
		}
	}
	if strings.Contains(s.Text, "November") || strings.Contains(s.Text, "CASE") {
		t.Errorf("summary carries more than the reasons:\n%s", s.Text)
	}
}
