package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/pages"
)

func render(d pages.Doc) string { return pages.Fragment(pages.Page{Title: "t", Doc: &d}) }

// The brief's page is a document, not the messages: the day's line is its
// title, the markets are a table and a chart, what is due is a table a day,
// and each sector carries its moves and its own sources.
func TestTheBriefsPageIsLaidOutFromTheReport(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	sg, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 10, 1, 7, 30, 0, 0, ny)
	article := model.Article{ID: "a", Title: "Yields jump", URL: "https://example.com/y", SourceName: "Reuters"}
	rep := model.Report{
		GeneratedAt: now,
		Summary:     "- Yields hit **5.55%** [1].",
		Overview:    "Yields hit 2002 highs.\n\n### 🏦 Bonds\n- The 30-year rose [1].",
		Cited:       []model.Article{article},
		Sections: []model.Section{{GroupID: "macro", GroupName: "Macro & Rates", Emoji: "🏦", Body: "### Fed\n- Waller speaks.",
			Articles: []model.Article{article}, Movers: []model.Quote{{Symbol: "TLT", Percent: -1.2}}}},
		Calendar: model.Calendar{Events: []model.Release{
			{At: now.Add(time.Hour), Country: "USD", Title: "Core PCE", Impact: "High", Forecast: "0.3%", Previous: "0.2%"},
		}},
	}
	market := Market{
		Gauges: []Gauge{{Name: "10-year Treasury yield", Level: "5.26%", Day: "+0.02", Week: "+0.30", AsOf: "29 Sep"}},
		Funds:  []Fund{{Name: "S&P 500", Quote: model.Quote{Symbol: "SPY", Percent: -0.2}}, {Name: "Crude oil", Quote: model.Quote{Symbol: "USO", Percent: 1.6}}},
	}
	page := render(BriefDoc(rep, market, Options{Display: sg}))
	for _, want := range []string{
		"<h1>Yields hit 2002 highs.</h1>",
		"Thursday 1 October 2026 @ 19:30 Singapore time",
		`<aside class="box">`, "Yields hit <b>5.55%</b>.",
		`<td class="num"><b>5.26%</b></td>`, `class="chart bars"`, "Crude oil",
		`<section id="overview">`, "<h3>🏦 Bonds</h3>",
		"Today, Thursday 1 October", `<tr class="marked">`, "Core PCE",
		`<section id="macro">`, `<span class="chip down">TLT -1.2%</span>`,
		"Sources for Macro &amp; Rates", `href="https://example.com/y"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	if strings.Contains(page, "<li>Yields hit 2002 highs") || strings.Count(page, "Yields hit 2002 highs") != 1 {
		t.Error("the day's line should head the page, not open the overview again")
	}
}

// A company's card: its verdict as a badge, its year on a range, its moves
// as a chart, and its case a labelled part at a time.
func TestTheCloserLooksPageIsACardACompany(t *testing.T) {
	idea := model.Idea{
		Name: "NESR", Ticker: "NESR", Exchange: "US", Kind: model.IdeaReaction, Verdict: model.Buy, Confidence: "low",
		Changed: "Nothing at the company.", Reaction: "Overreacted, on oil.", Case: "Contracts, not oil.", Numbers: "revenue $520m; no debt",
		Checked: "Checked in [MarketBeat](https://www.marketbeat.com/x).", Accounts: true,
		Quote: &model.Quote{Symbol: "NESR", Price: 25.47, Percent: -3.85},
		Trading: &model.Trading{Last: 25.47, Low52: 9.8, High52: 34.9, MA50: 30, MA200: 22,
			Returns: []model.Return{{Over: "1 week", Percent: -22.4}, {Over: "12 months", Percent: 148}}},
	}
	page := render(PicksDoc(Picks{Reactions: []model.Idea{idea}}, nil, IdeasOptions{ForChannel: true}, time.Now(), time.UTC))
	for _, want := range []string{
		"<h1>Reacting to the news</h1>", disclosure,
		`<article class="card" id="nesr">`, `<span class="badge buy">BUY</span>`, "low confidence · overreacted",
		"<dt>Last price</dt><dd>25.47 USD</dd>", `class="chart range"`, "50-day", `class="chart bars"`,
		"<h4>What changed</h4>", "<h4>Numbers</h4><p>• revenue $520m<br>", `<a href="https://www.marketbeat.com/x" target="_blank"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	if strings.Contains(page, `class="term"`) {
		t.Error("a source is styled as an explained word")
	}
}

// An analysis's figures are a table a column a period, oldest first, with
// the margins worked out, and the revenue as a chart.
func TestTheAnalysisPageTabulatesTheAccounts(t *testing.T) {
	acc := Accounts{Currency: "USD", Years: []Period{
		{Label: "FY to 31 Dec 2025", Figures: map[string]float64{"revenue": 3.37e9, "operatingIncome": 65e6}},
		{Label: "FY to 31 Dec 2024", Figures: map[string]float64{"revenue": 2.8e9, "operatingIncome": -105e6}},
	}}
	v := AnalysisVerdict{Verdict: model.Hold, Confidence: "low", Body: "### Why\n- Thin margin."}
	page := render(AnalysisDoc("GRAB", "Grab Holdings", v, "THE BUSINESS\n\n### What it sells\n- Rides.", acc, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		"<h1>Grab Holdings</h1>", `<span class="badge hold">HOLD</span>`, "Thin margin.",
		"<th class=\"num\">FY Dec 2024</th><th class=\"num\">FY Dec 2025</th>",
		"<td class=\"num\">$3.37bn</td>", "<td class=\"num\">-3.8%</td>", "<td class=\"num\">1.9%</td>",
		`class="chart columns"`, "<h2>THE BUSINESS</h2>", "<h3>What it sells</h3>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	if v, b := strings.Index(page, "Thin margin."), strings.Index(page, "<h2>THE BUSINESS</h2>"); v < b {
		t.Error("the verdict comes before the analysis")
	}
}

// A quarter's column is headed by its months, and the rows' names stay in
// view while the figures scroll on a phone.
func TestTheAnalysisPageNamesQuartersByTheirMonths(t *testing.T) {
	acc := Accounts{Currency: "USD", Quarters: []Period{
		{Label: "3 months to 31 Jan 2026", Figures: map[string]float64{"revenue": 9e8}},
		{Label: "3m to Oct 2025", Figures: map[string]float64{"revenue": 8e8}},
	}, TTM: &Period{Label: "12 months to 31 Jan 2026", Figures: map[string]float64{"revenue": 3.4e9}}}
	page := render(AnalysisDoc("GRAB", "Grab Holdings", AnalysisVerdict{}, "", acc, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		`<table class="labelled">`,
		"<th class=\"num\">Aug–Oct 2025</th><th class=\"num\">Nov–Jan 2026</th><th class=\"num\">Last 12 months</th>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	if strings.Contains(page, "3m to") || strings.Contains(page, "3 months to") {
		t.Error("a quarter is still headed by its label")
	}
}

// A year of weeks ends on a weekday, sometimes in a month's first days:
// Micron's quarter of June to August ended on 3 September 2026.
func TestAPeriodEndingInAMonthsFirstWeekIsNamedForTheMonthBefore(t *testing.T) {
	for label, want := range map[string]string{
		"3 months to 3 Sep 2026":  "Jun–Aug 2026",
		"3 months to 28 May 2026": "Mar–May 2026",
		"FY to 1 Sep 2022":        "FY Aug 2022",
		"FY to 28 Aug 2025":       "FY Aug 2025",
		"FY to 31 Dec 2025":       "FY Dec 2025",
		"3 months to 4 Jan 2026":  "Oct–Dec 2025",
		"Last 12 months":          "Last 12 months",
	} {
		if got := shortPeriod(label); got != want {
			t.Errorf("shortPeriod(%q) = %q, want %q", label, got, want)
		}
	}
}

// An industry's parts are drawn as a chain, in the order the explanation
// gives them, each with its companies.
func TestTheIndustryPageDrawsTheChain(t *testing.T) {
	companies := []IndustryCompany{
		{Part: "Motion parts", Name: "Harmonic Drive", Symbol: "6324.JP", Why: "gears", Market: "🇯🇵 Japan"},
		{Part: "Robot makers", Name: "Fanuc", Symbol: "6954.JP", Why: "the largest"},
		{Part: "Motion parts", Name: "Nabtesco", Symbol: "6268.JP", Why: "reducers"},
	}
	page := render(IndustryDoc("robotics", "### 🧭 The big picture\n- Robots move.", companies, IdeasOptions{}, nil))
	for _, want := range []string{
		`<figure class="chain">`, `<span class="step-name">Motion parts</span><span class="step-items"><code>6324.JP</code><code>6268.JP</code>`,
		`<span class="step-name">Robot makers</span>`, "<figcaption>Motion parts</figcaption>", "Robots move.",
		"<b>Harmonic Drive</b> <i>🇯🇵 Japan</i>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	if strings.Index(page, "step-name\">Motion parts") > strings.Index(page, "step-name\">Robot makers") {
		t.Error("the parts are out of the explanation's order")
	}
}

// An analysis set beside its industry gets a table of where it stands.
func TestTheAnalysisPageSetsTheCompanyBesideItsIndustry(t *testing.T) {
	acc := Accounts{Peers: &Peers{About: "<i>Nasdaq's Semiconductors group.</i>", Rows: []PeerRow{
		{Label: "Operating margin", Company: "26.1%", Median: "2.4%", Range: "-10.2% to 16.1%", Above: "56 of 65"},
	}}}
	page := render(AnalysisDoc("MU", "Micron", AnalysisVerdict{}, "", acc, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{"Beside its industry", "Operating margin", "<b>26.1%</b>", "-10.2% to 16.1%", "56 of 65", "Nasdaq's Semiconductors group."} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
