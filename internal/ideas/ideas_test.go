package ideas

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
)

var cited = []model.Article{
	{ID: "a1", Title: "Micron raises HBM outlook", SourceName: "CNBC", URL: "https://example.com/1", Summary: "Micron lifted its guide."},
	{ID: "a2", Title: "Memory prices climb", SourceName: "Reuters", URL: "https://example.com/2"},
	{ID: "a3", Title: "Oil slips", SourceName: "Reuters", URL: "https://example.com/3"},
}

// fakeCompleter answers with a fixed reply and remembers what it was asked.
type fakeCompleter struct {
	reply          string
	err            error
	system, prompt string
}

func (f *fakeCompleter) Complete(_ context.Context, system, prompt string) (string, model.Usage, error) {
	f.system, f.prompt = system, prompt
	return f.reply, model.Usage{InputTokens: 10, OutputTokens: 5}, f.err
}

// fakeVerifier registers the names it is given, by ticker.
type fakeVerifier map[string]string

func (v fakeVerifier) Verify(_ context.Context, queries []discover.Query) ([]string, error) {
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = v[q.Ticker+"."+q.Exchange]
	}
	return out, nil
}

// The sorting keeps to the leaders it was shown, a share in one theme at
// most, and a theme of fewer than three is no theme.
func TestSortingKeepsToTheLeaders(t *testing.T) {
	leaders := []market.Stock{}
	for _, sym := range []string{"NVDA", "VRT", "VST", "CEG", "AVGO", "LLY"} {
		leaders = append(leaders, market.Stock{Listing: market.Listing{Symbol: sym, Name: sym + " Inc. Common Stock", Industry: "Semiconductors", Sector: "Technology"},
			R24: 1, R12: 0.5, R12x1: 0.4, R6: 0.2, R3: 0.1, Volatility: 0.4})
	}
	c := &fakeCompleter{reply: `THEME: AI data centres
DRIVER: Hyperscalers are spending
  $400bn a year.
MEMBERS: NVDA, VRT, VST, FAKE, CEG
THEME: Obesity drugs
DRIVER: GLP-1s.
MEMBERS: LLY, NVDA
THEME: **Custom chips**
DRIVER: Own silicon.
MEMBERS: AVGO, VRT, VST, NVDA`}
	got, _, err := (&Sorter{Completer: c}).Sort(context.Background(), SortInput{
		Leaders: leaders, Bench: market.Stock{R24: 0.3, R12: 0.1, R12x1: 0.1, R6: 0.05, R3: 0.02},
		Headlines: []string{"Nvidia sells out"}, LastWeek: []string{"AI data centres"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "AI data centres" || strings.Join(got[0].Members, ",") != "NVDA,VRT,VST,CEG" {
		t.Fatalf("themes = %+v", got)
	}
	if got[0].Driver != "Hyperscalers are spending $400bn a year." || got[0].Kind != ThemePopular {
		t.Errorf("theme = %+v", got[0])
	}
	for _, want := range []string{"NVDA | NVDA | Semiconductors (Technology) | +70 pts | +30 pts | +15 pts | +8 pts | 40% | 1.25", "- Nvidia sells out", "Last week's themes: AI data centres"} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, c.prompt)
		}
	}
}

func TestTheScoutsFindsAreRead(t *testing.T) {
	c := &fakeCompleter{reply: `Some thinking out loud first.
THEME: Grid batteries
DRIVER: Utility storage orders up 60% a year.
EVIDENCE: US storage installs 12 GW in 2025 (EIA, March 2026); backlog $5bn (Fluence, Aug 2026)
MEMBERS: FLNC:US, 5E2:SP
THEME: Nothing much
MEMBERS: X:US`}
	got, _, err := (&Scout{Completer: c}).Find(context.Background(), ScoutInput{
		Early:   []market.Industry{{Name: "Electrical Products", Sector: "Industrials", Members: 6, Top: []string{"FLNC"}}},
		Names:   map[string]string{"FLNC": "Fluence Energy, Inc. Class A Common Stock"},
		Popular: []Theme{{Name: "AI data centres", Driver: "Spending."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != ThemeEarly || got[0].Evidence == "" || strings.Join(got[0].Members, ",") != "FLNC:US,5E2:SP" {
		t.Errorf("finds = %+v", got)
	}
	for _, want := range []string{"Electrical Products (Industrials, 6 companies)", "Fluence Energy (FLNC)", "- AI data centres: Spending."} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, c.prompt)
		}
	}
}

// The research's companies are checked against the exchange; a followed
// company, one listed where accounts cannot be read, and one whose ticker
// does not check out are dropped.
func TestResearchKeepsCheckableNewCompanies(t *testing.T) {
	c := &fakeCompleter{reply: `DRIVING: $400bn of spending,
growing 30% a year.
PRICED IN: Chips at 40x.
THE VALUE: Cooling and power.
Vertiv|VRT|US|buy|Cools the racks.
Nvidia|NVDA|US|buy|Followed already.
SK Hynix|000660|KS|buy|Korea.
Seatrium|5E2|SP|sell|Priced for more.
Madeup|ZZZ|US|buy|Does not exist.
Vertiv|VRT|US|buy|Twice.
Holdco|HLD|US|maybe|Not a lean.`}
	v := fakeVerifier{"VRT.US": "VERTIV HOLDINGS CO", "5E2.SP": "SEATRIUM LTD", "000660.KS": "SK HYNIX INC"}
	r := &Researcher{Completer: c, Verifier: v}
	got, _, err := r.Research(context.Background(), ResearchInput{
		Theme:    Theme{Kind: ThemePopular, Name: "AI data centres", Driver: "Spending.", Figures: "median +40 pts"},
		Previous: "Last week: chips.",
		Recent:   []string{"Rambus (RMBS): BUY on 6 Oct"},
		Tracked:  []string{"Nvidia"},
		Followed: map[string]bool{"NVDA": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Driving != "$400bn of spending, growing 30% a year." || got.PricedIn != "Chips at 40x." || got.Value != "Cooling and power." {
		t.Errorf("research = %+v", got)
	}
	var kept []string
	for _, i := range got.Ideas {
		kept = append(kept, i.Ticker+" "+i.Lean)
		if i.Kind != model.IdeaTheme || i.Theme != "AI data centres" || i.Listed == "" {
			t.Errorf("idea = %+v", i)
		}
	}
	if strings.Join(kept, ",") != "VRT buy,5E2 sell" {
		t.Errorf("kept %v", kept)
	}
	for _, want := range []string{"Last week's research on this theme:\nLast week: chips.", "- Rambus (RMBS): BUY on 6 Oct", "median +40 pts", "must not be chosen: Nvidia"} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, c.prompt)
		}
	}

	c.err = errors.New("plan exhausted")
	if _, _, err := r.Research(context.Background(), ResearchInput{Theme: Theme{Name: "X"}}); err == nil {
		t.Error("a failed call was not reported")
	}
}

// Verdicts are matched to their companies by symbol; a BUY the valuation has
// closed is turned into a HOLD, saying why, and a company without accounts is
// low confidence at most.
func TestVerdictsHoldToTheRules(t *testing.T) {
	ideas := []model.Idea{
		{Name: "Vertiv", Ticker: "VRT", Exchange: "US", Listed: "VERTIV HOLDINGS CO", Kind: model.IdeaTheme, Theme: "AI data centres", Lean: "buy",
			Link: "Cools the racks.", Accounts: true, Valuation: "Valuation against its theme: P/E 22 against 35.\n"},
		{Name: "Dear Co", Ticker: "DEAR", Exchange: "US", Listed: "DEAR CO", Kind: model.IdeaTheme, Lean: "buy", Accounts: true,
			BuyClosed: "it is dearer than its comparison on every measure"},
		{Name: "Seatrium", Ticker: "5E2", Exchange: "SP", Listed: "SEATRIUM LTD", Kind: model.IdeaTheme, Lean: "buy"},
		{Name: "Micron", Ticker: "MU", Exchange: "US", Listed: "MICRON TECHNOLOGY INC", Kind: model.IdeaReaction, Accounts: true,
			Link: "Up 12% last session, 4.1 times its usual daily move.", Articles: []model.Article{cited[0]},
			Quote: &model.Quote{Symbol: "MU", Price: 120, Percent: 12, AsOf: time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)}},
	}
	c := &fakeCompleter{reply: `=== VRT
VERDICT: BUY
CONFIDENCE: medium
VALUE: 22x earnings against the theme's 35x.
CASE: Orders outrun the price.
=== DEAR
VERDICT: BUY
CONFIDENCE: high
=== 5E2.SP
VERDICT: SELL
CONFIDENCE: high
=== MU
VERDICT: BUY
CONFIDENCE: high
CHANGED: Guide up 10% [1].
REACTION: Underreacted: the guide rose more.`}
	got, _, err := (&Judge{Completer: c, Batch: 10, Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }}).
		Judge(context.Background(), ideas, []string{"FACTS VRT", "", "", "FACTS MU"}, cited, []string{"Brent $80"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d verdicts", len(got))
	}
	if got[0].Verdict != model.Buy || got[0].Value != "22x earnings against the theme's 35x." {
		t.Errorf("Vertiv = %+v", got[0])
	}
	if got[1].Verdict != model.Hold || !strings.Contains(got[1].Overruled, "dearer") || got[1].Shown() {
		t.Errorf("a closed BUY = %+v", got[1])
	}
	if got[2].Confidence != "low" || got[2].Verdict != model.Sell {
		t.Errorf("no accounts = %+v", got[2])
	}
	if got[3].Changed != "Guide up 10% [1]." {
		t.Errorf("Micron = %+v", got[3])
	}

	for _, want := range []string{
		"=== VRT", `A theme pick, under "AI data centres". The research proposed it as a buy candidate: Cools the racks.`,
		"Answer VALUE, and leave out CHANGED, MOVE and REACTION.",
		"P/E 22 against 35.", "FACTS VRT",
		"=== 5E2.SP", "Its accounts could not be read, so your confidence can be low at most.",
		"A reaction: Up 12% last session, 4.1 times its usual daily move. [1]",
		"Answer CHANGED, MOVE and REACTION, and leave out VALUE.",
		"[1] Micron raises HBM outlook (CNBC", "    Micron lifted its guide.",
		"- Brent $80",
	} {
		if !strings.Contains(c.prompt, want) {
			t.Errorf("the verdict prompt is missing %q:\n%s", want, c.prompt)
		}
	}
	// Only the articles the companies hang on are listed, not the day's.
	if strings.Contains(c.prompt, "Oil slips") {
		t.Errorf("an article no company is tied to was listed:\n%s", c.prompt)
	}
}

// Verdicts come in batches, two at a time; a batch that fails costs its own
// companies and the error says how many batches that was.
func TestAFailedBatchCostsItsOwnCompanies(t *testing.T) {
	ideas := []model.Idea{{Name: "A", Ticker: "A", Exchange: "US"}, {Name: "B", Ticker: "B", Exchange: "US"}}
	c := &fakeCompleter{err: errors.New("down")}
	got, _, err := (&Judge{Completer: c, Batch: 1}).Judge(context.Background(), ideas, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "2 of 2 verdict batches failed") || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

// News that came after a price is marked as not traded yet: before the open,
// a share that has not moved on it has not shrugged it off.
func TestNewsAfterThePriceIsMarkedAsNotYetTraded(t *testing.T) {
	late := cited[0]
	late.Published = time.Date(2026, 9, 25, 21, 0, 0, 0, time.UTC)
	idea := model.Idea{Name: "Micron", Ticker: "MU", Exchange: "US", Kind: model.IdeaReaction, Articles: []model.Article{late},
		Quote: &model.Quote{Symbol: "MU", Price: 120, AsOf: time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)}}
	c := &fakeCompleter{reply: "=== MU\nVERDICT: HOLD\nCONFIDENCE: low"}
	if _, _, err := (&Judge{Completer: c}).Judge(context.Background(), []model.Idea{idea}, []string{""}, []model.Article{late}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.prompt, "Articles [1] came out after that price: the market has not traded on them yet.") {
		t.Errorf("prompt:\n%s", c.prompt)
	}
}

// A BUY must be cheaper than its theme on a measure, or have the growth to
// pay for being dearer; dearer on every measure with two warning signs is no
// BUY whatever the growth; nothing to compare holds it to neither.
func TestTheValuationGate(t *testing.T) {
	theme := Medians([]Multiples{
		{PE: 30, PS: 6, EVEBIT: 25, Growth: 0.20, HasGrowth: true},
		{PE: 35, PS: 8, EVEBIT: 28, Growth: 0.30, HasGrowth: true},
		{PE: 40, PS: 10, EVEBIT: 32, Growth: 0.25, HasGrowth: true},
	})
	if theme.PE != 35 || theme.PS != 8 || theme.EVEBIT != 28 || !theme.HasGrowth || math.Abs(theme.Growth-0.3) > 1e-9 {
		t.Fatalf("medians = %+v", theme)
	}
	for _, tc := range []struct {
		name   string
		v      Valuation
		closed bool
	}{
		{"cheaper on one", Valuation{Now: Multiples{PE: 30, PS: 12, EVEBIT: 40}, Theme: theme}, false},
		{"dearer, growth pays", Valuation{Now: Multiples{PE: 50, PS: 12, EVEBIT: 40, Growth: 0.40, HasGrowth: true}, Theme: theme}, false},
		{"dearer, growth does not pay", Valuation{Now: Multiples{PE: 50, PS: 12, EVEBIT: 40, Growth: 0.10, HasGrowth: true}, Theme: theme}, true},
		{"dearer with two flags, growth or not", Valuation{Now: Multiples{PE: 50, PS: 12, EVEBIT: 40, Growth: 0.60, HasGrowth: true}, Theme: theme, Flags: []string{"a", "b"}}, true},
		{"a loss, but cheaper on sales", Valuation{Now: Multiples{PS: 4}, Theme: theme}, false},
		{"no theme: its own history", Valuation{Now: Multiples{PE: 20}, Own: Multiples{PE: 15}}, true},
		{"nothing to compare", Valuation{Now: Multiples{PE: 20}}, false},
		{"no accounts", Valuation{}, false},
	} {
		if got := tc.v.BuyClosed() != ""; got != tc.closed {
			t.Errorf("%s: closed = %v (%q)", tc.name, got, tc.v.BuyClosed())
		}
	}
	// Fewer than three peers make no median.
	if m := Medians([]Multiples{{PE: 10}, {PE: 20}}); m.PE != 0 {
		t.Errorf("median of two = %v", m.PE)
	}

	facts := Valuation{Now: Multiples{PE: 50, PS: 12, Growth: 0.1, HasGrowth: true}, Theme: theme, Peers: 3, Flags: []string{"far above its average"}}.Facts()
	for _, want := range []string{"- Price to earnings: 50.0; its theme's median 35.0", "Revenue growth, latest year: +10%", "over 3 companies", "Warning signs", "BUY is not open to this company"} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts are missing %q:\n%s", want, facts)
		}
	}
}

func TestWarningSigns(t *testing.T) {
	got := WarningSigns(150, 100, 140, -300_000, 12e6, 100e6)
	if len(got) != 4 {
		t.Errorf("signs = %v", got)
	}
	if got := WarningSigns(110, 100, 0, -1000, 0, 100e6); len(got) != 0 {
		t.Errorf("signs from nothing much = %v", got)
	}
}

// The log says whether this week's themes are done, and hands last week's
// research to a theme of the same name.
func TestTheThemeLog(t *testing.T) {
	path := t.TempDir() + "/themes.json"
	l, err := LoadThemeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	monday := time.Date(2026, 9, 28, 11, 50, 0, 0, time.UTC)
	if l.DoneThisWeek(monday, ny) {
		t.Error("an empty log said the week was done")
	}
	if err := l.Add(ThemeRun{At: monday, Themes: []ThemeEntry{
		{Kind: ThemePopular, Name: "AI data centres", Research: "Chips priced in."},
		{Kind: ThemeEarly, Name: "Grid batteries", Research: "Orders up."},
	}}); err != nil {
		t.Fatal(err)
	}
	l, _ = LoadThemeLog(path)
	if !l.DoneThisWeek(monday.Add(3*24*time.Hour), ny) || l.DoneThisWeek(monday.Add(7*24*time.Hour), ny) {
		t.Error("the week is not counted from Monday")
	}
	if l.Previous("ai data centres") != "Chips priced in." || l.Previous("Obesity") != "" {
		t.Error("last week's research was not found by name")
	}
	if strings.Join(l.Names(ThemeEarly), ",") != "Grid batteries" {
		t.Errorf("early names = %v", l.Names(ThemeEarly))
	}
}
