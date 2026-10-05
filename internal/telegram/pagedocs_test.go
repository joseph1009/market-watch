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
			Returns: []model.Return{{Over: "1 week", Percent: -22.4}, {Over: "12 months", Percent: 148}},
			Market:  []model.Return{{Over: "12 months", Percent: 17.2}}},
	}
	page := render(PicksDoc(Picks{Reactions: []model.Idea{idea}}, nil, IdeasOptions{ForChannel: true}, time.Now(), time.UTC))
	for _, want := range []string{
		"<h1>Reacting to the news</h1>", disclosure,
		`<article class="card" id="nesr">`, `<span class="badge buy">BUY</span>`, "low confidence · overreacted",
		`<figure class="chart price">`, `<span class="price-now">$25.47</span>`, `<span class="chip down">-3.9% on the last session</span>`,
		`<span class="chip down">1 week -22.4%</span>`, `<span class="chip up">12 months +148.0% (S&amp;P 500 +17.2%)</span>`,
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
	page := render(AnalysisDoc("GRAB", "Grab Holdings", v, "THE BUSINESS\n\n### What it sells\n- Rides.", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		"<h1>Grab Holdings</h1>", `<span class="badge hold">HOLD</span>`, "Thin margin.",
		"<th class=\"num\">2024</th><th class=\"num\">2025</th>",
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

// The page reads the business first, then the tables, then what the figures
// say, as the owner asked on 4 October 2026.
func TestTheAnalysisPageGivesTheBusinessBeforeTheNumbers(t *testing.T) {
	acc := Accounts{Currency: "USD", Years: []Period{
		{Label: "FY to 31 Dec 2025", Figures: map[string]float64{"revenue": 3.37e9}},
		{Label: "FY to 31 Dec 2024", Figures: map[string]float64{"revenue": 2.8e9}},
	}}
	prose := "THE BUSINESS\n\n### What it sells\n- Rides.\n\nWHERE IT IS HEADING\n\n### Plans\n- Banking.\n\nKEY NUMBERS\n\n### Sales\n- Revenue grew."
	page := render(AnalysisDoc("GRAB", "Grab Holdings", AnalysisVerdict{}, prose, acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	business, plans, table, numbers := strings.Index(page, "<h2>THE BUSINESS</h2>"), strings.Index(page, "Banking."),
		strings.Index(page, "Year by year"), strings.Index(page, "<h2>KEY NUMBERS</h2>")
	if business < 0 || plans < 0 || table < 0 || numbers < 0 {
		t.Fatalf("a part is missing:\n%s", page)
	}
	if !(business < plans && plans < table && table < numbers) {
		t.Errorf("the order is business %d, plans %d, table %d, numbers %d", business, plans, table, numbers)
	}

	// With nothing to split at, the analysis follows the tables whole.
	page = render(AnalysisDoc("GRAB", "Grab Holdings", AnalysisVerdict{}, "THE BUSINESS\n\n- Rides.", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	if strings.Index(page, "<h2>THE BUSINESS</h2>") < strings.Index(page, "Year by year") {
		t.Error("an analysis with no figures heading came before the tables")
	}
}

// The box of first figures, the cash and debt, and each period's growth on a
// year earlier, as the owner listed them on 4 October 2026. Each tile says
// what its figure is worked out on, in a few words.
func TestTheAnalysisPageGivesTheFiguresAReaderLooksForFirst(t *testing.T) {
	asOf, older := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), time.Date(2025, 11, 27, 0, 0, 0, 0, time.UTC)
	acc := Accounts{Currency: "USD",
		Glance: &Glance{MarketCap: 1.21e12, PE: 14.5, PS: 9.1, On: "Sep 2025–Aug 2026", ForwardPE: 6.7, ForwardFor: "Sep 2026–Aug 2027",
			Price: 1074.89, Shares: 1.13e9, EPS: 74.21, Sales: 133.19e9, ExpectedEPS: 160.39,
			Cash: 43.4e9, Debt: 8.84e9, WithInvestments: true, HasCash: true, HasDebt: true, CashAt: asOf, DebtAt: older},
		Balance: &BalanceSheet{AsOf: asOf, Release: true, Figures: map[string]float64{"cash": 38.4e9, "longTermDebt": 8.84e9},
			Older: map[string]time.Time{"longTermDebt": older}},
		Quarters: []Period{
			{Label: "3 months to 3 Sep 2026", End: asOf, Figures: map[string]float64{"revenue": 54.2e9}},
			{Label: "3 months to 28 May 2026", End: time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC), Figures: map[string]float64{"revenue": 41.5e9}},
			{Label: "3 months to 28 Aug 2025", End: time.Date(2025, 8, 28, 0, 0, 0, 0, time.UTC), Figures: map[string]float64{"revenue": 11.3e9}},
		}}
	page := render(AnalysisDoc("MU", "Micron", AnalysisVerdict{}, "", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		"<h2>At a glance</h2>",
		`<dt>Market value</dt><dd class="tile-value">$1.21tn</dd><dd class="tile-note">1.13bn shares at $1,074.89</dd>`,
		`<dd class="tile-value">14.5×</dd><dd class="tile-note">On $74.21 a share earned in Sep 2025–Aug 2026</dd>`,
		"On $160.39 a share expected in Sep 2026–Aug 2027",
		"On $133.19bn of sales in Sep 2025–Aug 2026",
		`<dt>Cash</dt><dd class="tile-value">$43.40bn</dd><dd class="tile-note">With short-term investments, at 3 Sep 2026</dd>`,
		"At 27 Nov 2025, the latest filed",
		"Cash and debt", "<th class=\"num\">3 Sep 2026*</th>", "(at 27 Nov 2025)",
		"<td>Revenue growth</td>", "+379.6%<i>from $11.30bn</i><i>in Jun–Aug 2025</i>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	// The industry has its own section, and the change in expected earnings
	// is left out: it read without its base.
	for _, unwanted := range []string{"middle company", "116%", "up 116"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("the glance still has %q", unwanted)
		}
	}
	if strings.Contains(page, "Cash less debt") {
		t.Error("cash and debt at different dates were netted")
	}
	if g, f := strings.Index(page, "At a glance"), strings.Index(page, "Quarter by quarter"); g > f {
		t.Error("the box comes after the tables")
	}
}

// The periods read from a results release are marked, and the page says where
// they came from.
func TestTheReleaseColumnsAreMarked(t *testing.T) {
	acc := Accounts{Currency: "USD", Release: "filed 30 September 2026", Quarters: []Period{
		{Label: "3 months to 3 Sep 2026", Figures: map[string]float64{"revenue": 54.2e9}, Release: true},
		{Label: "3 months to 28 May 2026", Figures: map[string]float64{"revenue": 41.5e9}},
	}}
	page := render(AnalysisDoc("MU", "Micron", AnalysisVerdict{}, "", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		`<th class="num">Mar–May 2026</th><th class="num">Jun–Aug 2026*</th>`,
		"where marked *, from its results release, filed 30 September 2026",
		"Micron, from its filings and its results release, filed 30 September 2026",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

// Every quarter's growth is shown, though the year-ago quarter of all but
// the newest is not a column: the owner found the row blank on 4 October
// 2026 but for one figure. Gross profit is given beside its margin.
func TestEveryQuarterGrowsAgainstAYearEarlier(t *testing.T) {
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	acc := Accounts{Currency: "USD", Quarters: []Period{
		{Label: "3 months to 3 Sep 2026", End: day(9, 3), Figures: map[string]float64{"revenue": 54e9, "grossProfit": 47e9}, YearAgo: 12e9, YearAgoLabel: "Jun–Aug 2025"},
		{Label: "3 months to 28 May 2026", End: day(5, 28), Figures: map[string]float64{"revenue": 40e9}, YearAgo: 10e9},
	}, TTM: &Period{Label: "12 months to 3 Sep 2026", End: day(9, 3), Figures: map[string]float64{"revenue": 120e9}, YearAgo: 40e9}}
	page := render(AnalysisDoc("MU", "Micron", AnalysisVerdict{}, "", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		"<td>Revenue growth</td><td class=\"num\">+300.0%<i>from $10.00bn</i></td><td class=\"num\">+350.0%<i>from $12.00bn</i><i>in Jun–Aug 2025</i></td><td class=\"num\">+200.0%<i>from $40.00bn</i></td>",
		"<th class=\"num\">Sep 2025–Aug 2026</th>",
		"<td>Gross profit</td><td class=\"num\">–</td><td class=\"num\">$47.00bn</td>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

// An analysis's citations link to its footnotes, on the page and in the
// chat, as the brief's do.
func TestTheAnalysisCitationsLinkToTheirSources(t *testing.T) {
	v := AnalysisVerdict{Verdict: model.Hold, Body: "### Why\n- Rivals are building [2]."}
	prose := "THE BUSINESS\n\n### What it sells\n- Rides [1]."
	sources := []pages.Link{{Title: "CNBC", URL: "https://www.cnbc.com/a"}, {Title: "Reuters", URL: "https://www.reuters.com/b"}}
	page := render(AnalysisDoc("GRAB", "Grab Holdings", v, prose, Accounts{}, nil, sources, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{`<a href="https://www.cnbc.com/a" class="cite"`, `<a href="https://www.reuters.com/b" class="cite"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	chat := strings.Join(RenderAnalysis("GRAB", v, prose, sources, IdeasOptions{}), "\n")
	for _, want := range []string{`Rides <a href="https://www.cnbc.com/a">[1]</a>`, `building <a href="https://www.reuters.com/b">[2]</a>`} {
		if !strings.Contains(chat, want) {
			t.Errorf("the chat is missing %q:\n%s", want, chat)
		}
	}
}

// A quarter's column is headed by its months, and the rows' names stay in
// view while the figures scroll on a phone.
func TestTheAnalysisPageNamesQuartersByTheirMonths(t *testing.T) {
	acc := Accounts{Currency: "USD", Quarters: []Period{
		{Label: "3 months to 31 Jan 2026", Figures: map[string]float64{"revenue": 9e8}},
		{Label: "3m to Oct 2025", Figures: map[string]float64{"revenue": 8e8}},
	}, TTM: &Period{Label: "12 months to 31 Jan 2026", Figures: map[string]float64{"revenue": 3.4e9}}}
	page := render(AnalysisDoc("GRAB", "Grab Holdings", AnalysisVerdict{}, "", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{
		`<table class="labelled">`,
		"<th class=\"num\">Aug–Oct 2025</th><th class=\"num\">Nov 2025–Jan 2026</th><th class=\"num\">Feb 2025–Jan 2026</th>",
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
		"FY to 1 Sep 2022":        "Sep 2021–Aug 2022",
		"FY to 28 Aug 2025":       "Sep 2024–Aug 2025",
		"FY to 31 Dec 2025":       "2025",
		"12 months to 3 Sep 2026": "Sep 2025–Aug 2026",
		"Sep 2025–Aug 2026":       "Sep 2025–Aug 2026",
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
	page := render(IndustryDoc("robotics", "### 🧭 The big picture\n- Robots move.", companies, nil, IdeasOptions{}, nil))
	for _, want := range []string{
		`<figure class="chain">`, `<span class="step-name">Motion parts</span><span class="step-items"><code>6324.JP</code><code>6268.JP</code>`,
		`<span class="step-name">Robot makers</span>`, "<figcaption>Motion parts</figcaption>", "Robots move.",
		"<b>Harmonic Drive</b> <i>🇯🇵 Japan</i>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}

	// Its citations link to numbered footnotes, on the page and in the chat.
	sources := []pages.Link{{Title: "Reuters, robot orders", URL: "https://www.reuters.com/robots"}}
	prose := "### 🧭 The big picture\n- Robots sold for US$50bn [1]."
	page = render(IndustryDoc("robotics", prose, companies, sources, IdeasOptions{}, nil))
	for _, want := range []string{`<a href="https://www.reuters.com/robots" class="cite"`, "Sources", "Reuters, robot orders"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	chat := strings.Join(RenderIndustry("robotics", prose, companies, sources, IdeasOptions{}, nil), "\n")
	if !strings.Contains(chat, `<a href="https://www.reuters.com/robots">[1]</a>`) || !strings.Contains(chat, "Reuters, robot orders") {
		t.Errorf("the chat lacks the citation or its footnote:\n%s", chat)
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
	page := render(AnalysisDoc("MU", "Micron", AnalysisVerdict{}, "", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{"Beside its industry", "Operating margin", "<b>26.1%</b>", "-10.2% to 16.1%", "56 of 65", "Nasdaq's Semiconductors group."} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// The analysis page draws the price's year against its averages, and the
// cash the business makes against what it invests.
func TestTheAnalysisPageDrawsThePriceAndTheCash(t *testing.T) {
	tr := &model.Trading{Last: 120, High52: 130, Low52: 80}
	for i := 0; i < 30; i++ {
		tr.Path = append(tr.Path, model.PricePoint{Date: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i), Close: 90 + float64(i), MA50: 95})
	}
	acc := Accounts{Trading: tr, Years: []Period{
		{Label: "FY to 31 Dec 2025", Figures: map[string]float64{"operatingCashFlow": 5e9, "capitalExpenditure": 2e9}},
		{Label: "FY to 31 Dec 2024", Figures: map[string]float64{"operatingCashFlow": 4e9, "capitalExpenditure": 3e9}},
	}}
	page := render(AnalysisDoc("MU", "Micron", AnalysisVerdict{}, "", acc, nil, nil, IdeasOptions{}, nil, time.Now(), time.UTC))
	for _, want := range []string{"The price over the year", "200-day average", "Cash from the business against what it invests", "<b>$3.00bn</b>", "<b>$1.00bn</b>"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// A share price keeps its cents however large it is.
func TestPricesAreGivenToTheCent(t *testing.T) {
	for v, want := range map[float64]string{1074.89: "1,074.89", 1255: "1,255.00", 179.614: "179.61", 12345.6: "12,345.60"} {
		if got := formatPrice(v); got != want {
			t.Errorf("formatPrice(%v) = %q, want %q", v, got, want)
		}
	}
}
