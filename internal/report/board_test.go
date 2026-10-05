package report

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// The model is given the market in its parts and the gaps between them, the
// unusual ones marked, and the board's funds are not listed again among the
// prices.
func TestThePromptCarriesTheMarketInItsParts(t *testing.T) {
	board := model.Board{
		Session: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		Funds: []model.BoardFund{
			{Symbol: "SPY", Name: "S&P 500", Group: "The broad market", Level: 763.99, Day: 0.5, Week: 1.1, Month: -2},
			{Symbol: "TNX", Name: "10-year Treasury yield", Group: "Commodities, rates, the dollar and Bitcoin", Yield: true, Level: 5.3, Day: 0.08},
		},
		Gaps: []model.Gap{{Name: "Copper vs gold", A: "CPER", B: "GLD", Day: 3, Month: 9, UsualDay: 1, UsualMonth: 3,
			Unusual: true, Window: "month", Reading: "Copper over gold: a bet on growth."}},
	}
	got := renderBoard(board)
	for _, want := range []string{
		"on the last US session, Monday 5 October",
		"The broad market:\n- S&P 500 (SPY): $763.99, +0.5% on the day, +1.1% over the week, -2.0% over the month\n",
		"- 10-year Treasury yield: 5.30%, +0.08 percentage points on the day",
		"- Copper vs gold (CPER less GLD): +3.0 on the day, against a usual 1.0; +9.0 over the month, against a usual 3.0. UNUSUAL over the month. Reads: Copper over gold: a bet on growth.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("board block lacks %q:\n%s", want, got)
		}
	}

	quotes := []model.Quote{{Symbol: "SPY", Price: 763.99}, {Symbol: "MU", Price: 120}}
	if left := offBoard(quotes, board); len(left) != 1 || left[0].Symbol != "MU" {
		t.Errorf("prices left = %+v, want MU alone", left)
	}
	if left := offBoard(quotes, model.Board{}); len(left) != 2 {
		t.Errorf("without a board, every price stays: %+v", left)
	}
}
