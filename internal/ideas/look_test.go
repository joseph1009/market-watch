package ideas

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/joseph1009/market-watch/internal/model"
)

// The research is shown the market's largest moves, and never returns a
// company the watchlists follow, which the screen judges instead: a followed
// one found twice would take a new name's place.
func TestResearchSeesTheMoversAndLeavesTheFollowedCompanies(t *testing.T) {
	c := &fakeCompleter{reply: "Micron|MU|US|news|1|Raised its outlook.\nRambus|RMBS|US|connected|1|Its chips go into HBM.\nMicron Japan|MU|JP|news|1|A Tokyo listing is not the followed one."}
	r := &Researcher{Completer: c, Verifier: fakeVerifier{"MU.US": "MICRON", "RMBS.US": "RAMBUS INC", "MU.JP": "MICRON JAPAN"}}

	got, _, err := r.Propose(context.Background(), Input{
		Cited:    cited,
		Followed: map[string]bool{"MU": true},
		Movers:   []Mover{{Symbol: "WOLF", Name: "Wolfspeed Inc", Percent: 23.4, DollarVolume: 412e6}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var symbols []string
	for _, g := range got {
		symbols = append(symbols, g.Symbol())
	}
	if strings.Join(symbols, ",") != "RMBS,MU.JP" {
		t.Errorf("got %v, want Rambus and the Tokyo listing, and not the followed MU", symbols)
	}
	if !strings.Contains(c.prompt, "- Wolfspeed Inc (WOLF) +23.4%, US$412m traded") {
		t.Errorf("the prompt does not show the movers:\n%s", c.prompt)
	}
}

// The screen chooses from the rows it was shown, up to its limit, and each
// choice arrives as a followed idea carrying the screen's reason and the
// day's articles that name it.
func TestTheScreenChoosesOnlyFromItsRows(t *testing.T) {
	rows := []Row{
		{Ticker: "MU", Name: "Micron", Sector: "Semiconductors", Line: "today -3.0%; 1W -9%", Articles: cited[:1]},
		{Ticker: "NVDA", Name: "Nvidia", Sector: "Semiconductors", Line: "today +0.2%"},
		{Ticker: "BRK.B", Name: "Berkshire", Sector: "Financials", Line: "today +0.1%"},
	}
	c := &fakeCompleter{reply: "Here are my picks.\nZZZZ|not on the list\nMU|Forecasts up 4% while the share fell 9% in a week.\nMU|twice\nBRK.B|Cash pile against a falling price.\nNVDA|third"}
	s := &Screener{Completer: c, Max: 2}

	got, _, err := s.Pick(context.Background(), "OVERVIEW\nMicron raised its outlook [1].", cited, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Ticker != "MU" || got[1].Ticker != "BRK.B" {
		t.Fatalf("picked %+v, want MU then BRK.B", got)
	}
	mu := got[0]
	if !mu.Followed || mu.Exchange != "US" || mu.Link != "Forecasts up 4% while the share fell 9% in a week." || len(mu.Articles) != 1 {
		t.Errorf("MU = %+v", mu)
	}
	if !strings.Contains(c.system, "up to 2 companies") {
		t.Errorf("the system prompt did not carry the limit:\n%s", c.system)
	}
	if !strings.Contains(c.prompt, "MU | Micron | Semiconductors | today -3.0%; 1W -9% | today's articles [1]") {
		t.Errorf("the prompt does not carry the rows:\n%s", c.prompt)
	}
}

// batchCompleter answers each verdict call with blocks for the symbols in
// its prompt, and fails the call that carries a given one.
type batchCompleter struct {
	mu      sync.Mutex
	prompts []string
	fail    string
}

func (b *batchCompleter) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	b.mu.Lock()
	b.prompts = append(b.prompts, prompt)
	b.mu.Unlock()
	var reply strings.Builder
	for _, line := range strings.Split(prompt, "\n") {
		symbol, ok := strings.CutPrefix(line, "=== ")
		if !ok {
			continue
		}
		if symbol == b.fail {
			return "", model.Usage{}, errors.New("session limit")
		}
		verdict := "BUY"
		if symbol == "HOLDME" {
			verdict = "HOLD"
		}
		reply.WriteString("=== " + symbol + "\nVERDICT: " + verdict + "\nCONFIDENCE: medium\n" +
			"CHANGED: Revenue guidance up 12%.\nMOVE: Down 4% today, up 30% this year.\nREACTION: underreacted, the guide beat and the share fell.\n" +
			"CASE: Cheap for the growth [1].\nNUMBERS: 14x next year\nRISK: Memory prices turn.\n")
	}
	return reply.String(), model.Usage{InputTokens: 100, OutputTokens: 10}, nil
}

// Twenty companies are judged five at a time. A failed batch costs its own
// companies and is reported; the rest keep their verdicts, with what changed,
// how the share moved and whether the move was justified. The backdrop is
// written once above each batch, and a followed company is marked as one.
func TestVerdictsAreJudgedInBatchesAndSayWhetherTheMoveWasJustified(t *testing.T) {
	var all []model.Idea
	for _, s := range []string{"AAA", "BBB", "CCC", "DDD", "EEE", "FFF", "GGG"} {
		all = append(all, model.Idea{Name: s + " Corp", Ticker: s, Exchange: "US", Link: "news"})
	}
	all[0].Followed = true
	c := &batchCompleter{fail: "FFF"}
	j := &Judge{Completer: c, Batch: 3}

	got, usage, err := j.Judge(context.Background(), all, make([]string, len(all)), cited, []string{"Brent crude oil, US$ a barrel: 114.89"})
	if err == nil || !strings.Contains(err.Error(), "1 of 3 verdict batches failed") {
		t.Errorf("err = %v", err)
	}
	if len(c.prompts) != 3 || usage.InputTokens != 200 {
		t.Errorf("%d calls, usage %+v; want three calls and two answered", len(c.prompts), usage)
	}
	if len(got) != 4 {
		t.Fatalf("got %d verdicts, want the four outside the failed batch", len(got))
	}
	first := got[0]
	if first.Changed != "Revenue guidance up 12%." || first.Moved != "Down 4% today, up 30% this year." ||
		!strings.HasPrefix(first.Reaction, "underreacted") {
		t.Errorf("first = %+v", first)
	}
	for _, p := range c.prompts {
		if !strings.Contains(p, "- Brent crude oil, US$ a barrel: 114.89") {
			t.Errorf("a batch went without the backdrop:\n%s", p)
		}
	}
	if !strings.Contains(strings.Join(c.prompts, ""), "Only BUY or SELL will be shown for it") {
		t.Error("the followed company was not marked as one")
	}
}

// Only BUY and SELL are shown on a followed company; any verdict is shown on
// a new one.
func TestAFollowedHoldIsNotShown(t *testing.T) {
	for _, tt := range []struct {
		idea model.Idea
		want bool
	}{
		{model.Idea{Followed: true, Verdict: model.Hold}, false},
		{model.Idea{Followed: true, Verdict: model.Sell}, true},
		{model.Idea{Verdict: model.Hold}, true},
		{model.Idea{}, false},
	} {
		if got := tt.idea.Shown(); got != tt.want {
			t.Errorf("%+v: Shown = %v", tt.idea, got)
		}
	}
}
