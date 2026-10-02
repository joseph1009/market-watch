package telegram

import "testing"

// A ticker carries the flag of where it trades, since OpenFIGI's suffixes can
// read as another country's: CN is Canada, CH is China.
func TestATickerShowsTheFlagOfWhereItTrades(t *testing.T) {
	for _, tc := range []struct{ symbol, exchange, want string }{
		{"SHOP.CN", "CN", "🇨🇦 <code>SHOP.CN</code>"},
		{"600519.CH", "CH", "🇨🇳 <code>600519.CH</code>"},
		{"NVDA", "US", "🇺🇸 <code>NVDA</code>"},
		{"X", "", "<code>X</code>"},
	} {
		if got := flagged(tc.symbol, tc.exchange); got != tc.want {
			t.Errorf("flagged(%q, %q) = %q, want %q", tc.symbol, tc.exchange, got, tc.want)
		}
	}
	for symbol, want := range map[string]string{
		"D05.SP": "🇸🇬 <code>D05.SP</code>",
		"RMBS":   "🇺🇸 <code>RMBS</code>",
		"BF.B":   "🇺🇸 <code>BF.B</code>",
	} {
		if got := flaggedSymbol(symbol); got != want {
			t.Errorf("flaggedSymbol(%q) = %q, want %q", symbol, got, want)
		}
	}
}
