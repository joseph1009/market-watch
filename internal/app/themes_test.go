package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
)

// byPrompt answers each call with the reply of the first key its prompt
// contains, and counts the calls.
type byPrompt struct {
	replies [][2]string
	mu      *sync.Mutex
	calls   *int
}

func (b byPrompt) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	b.mu.Lock()
	*b.calls++
	b.mu.Unlock()
	for _, r := range b.replies {
		if strings.Contains(prompt, r[0]) {
			return r[1], model.Usage{}, nil
		}
	}
	return "", model.Usage{}, nil
}

func stage(calls *int, replies ...[2]string) byPrompt {
	return byPrompt{replies: replies, mu: &sync.Mutex{}, calls: calls}
}

// The week's themes run on the scheduled look once a week: the leaders are
// sorted into a popular theme, the scout adds an early one, each is
// researched, and the companies proposed are judged and shown under their
// theme -- a BUY shown, a HOLD not. The picks go on the scorecard under their
// theme, and the week is written down so it does not run again until the
// next.
func TestTheWeeksThemesRunOnceAWeek(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	var listings []market.Listing
	trend := map[string]float64{market.Benchmark: 0.0003}
	for _, sym := range []string{"AAA", "BBB", "CCC", "DDD"} {
		listings = append(listings, market.Listing{Symbol: sym, Name: sym + " Chips Inc. Common Stock", MarketCap: 50e9, Industry: "Semiconductors", Sector: "Technology"})
		trend[sym] = 0.002
	}
	listings = append(listings, market.Listing{Symbol: "VRT", Name: "Vertiv Holdings Co Class A Common Stock", MarketCap: 40e9, Industry: "Electrical Products", Sector: "Industrials"})
	fakeMarket(t, a, 300, listings, nil, trend)

	var sorts, scouts, research, verdicts int
	a.Sorter = &ideas.Sorter{Completer: stage(&sorts, [2]string{"leaders", "THEME: AI chips\nDRIVER: Hyperscalers are spending.\nMEMBERS: AAA, BBB, CCC"})}
	a.Scout = &ideas.Scout{Completer: stage(&scouts, [2]string{"turn", "THEME: Grid batteries\nDRIVER: Orders up 60%.\nEVIDENCE: 12 GW (EIA)\nMEMBERS: FLNC:US"})}
	a.Researcher = &ideas.Researcher{
		Completer: stage(&research,
			[2]string{"AI chips", "DRIVING: $400bn a year.\nPRICED IN: The chips.\nTHE VALUE: Cooling.\nVertiv|VRT|US|buy|Cools the racks."},
			[2]string{"Grid batteries", "DRIVING: Orders.\nPRICED IN: Nothing yet.\nTHE VALUE: Integrators.\nFluence|FLNC|US|buy|Builds the systems."}),
		Verifier: stubVerifier{"VRT.US": "VERTIV HOLDINGS CO", "FLNC.US": "FLUENCE ENERGY INC"},
	}
	a.Judge = &ideas.Judge{Completer: stage(&verdicts, [2]string{"=== ", "=== VRT\nVERDICT: BUY\nCONFIDENCE: medium\nVALUE: Cheaper than the chips.\n=== FLNC\nVERDICT: HOLD\nCONFIDENCE: low"})}
	themes, err := ideas.LoadThemeLog(a.Cfg.DataDir + "/themes.json")
	if err != nil {
		t.Fatal(err)
	}
	a.Themes = themes
	card, _ := ideas.LoadScorecard(a.Cfg.DataDir + "/scorecard.json")
	a.Scorecard = card

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, true))

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	for _, want := range []string{
		"<b>🔎 This week's picks</b>",
		"<b>AI chips</b> · <i>Popular</i>",
		"<i>The numbers:</i> 3 companies;",
		"<i>Where the value is:</i> Cooling.",
		"<b>Vertiv</b> <code>VRT</code>",
		"<i>Where it fits:</i> Cools the racks.",
		"<i>The price:</i> Cheaper than the chips.",
	} {
		if !strings.Contains(owner, want) {
			t.Errorf("the week's picks are missing %q:\n%s", want, owner)
		}
	}
	if strings.Contains(owner, "Fluence") || strings.Contains(owner, "Grid batteries") {
		t.Errorf("a HOLD, or a theme with nothing shown, was shown:\n%s", owner)
	}
	if sorts != 1 || scouts != 1 || research != 2 {
		t.Errorf("calls: %d sorts, %d scouts, %d research", sorts, scouts, research)
	}

	all := card.All()
	if len(all) != 1 || all[0].Symbol != "VRT" || all[0].Source != ideas.SourceTheme || all[0].Theme != "AI chips" {
		t.Errorf("scorecard = %+v", all)
	}
	themes, _ = ideas.LoadThemeLog(a.Cfg.DataDir + "/themes.json")
	if last := themes.Last(); last == nil || len(last.Themes) != 2 || last.Themes[1].Kind != ideas.ThemeEarly || !strings.Contains(last.Themes[0].Research, "Cooling") {
		t.Errorf("the week was not written down: %+v", last)
	}

	// The next day in the same week, the themes do not run again.
	a.sendIdeas(context.Background(), lookFrom(todaysBrief, true))
	if sorts != 1 {
		t.Errorf("the themes ran twice in a week")
	}
}

// Without enough of the market's history, the week's themes wait: the week
// is not written down, so they run once the history has filled.
func TestTheThemesWaitForTheMarketsHistory(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242
	fakeMarket(t, a, 100, []market.Listing{{Symbol: "AAA", Name: "AAA Inc.", MarketCap: 5e9}}, nil, nil)
	var sorts int
	a.Sorter = &ideas.Sorter{Completer: stage(&sorts)}
	a.Researcher = &ideas.Researcher{Completer: stage(new(int))}
	a.Judge = &ideas.Judge{Completer: stage(new(int))}
	a.Themes, _ = ideas.LoadThemeLog(a.Cfg.DataDir + "/themes.json")

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, true))

	if sorts != 0 || a.Themes.Last() != nil {
		t.Errorf("sorted %d times on a hundred sessions; log %+v", sorts, a.Themes.Last())
	}
}
