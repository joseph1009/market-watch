package prices

import "testing"

func TestChartSymbolSpellsEachMarketTheWayTheChartSourceDoes(t *testing.T) {
	for _, tc := range []struct{ ticker, exchange, want string }{
		{"RMBS", "US", "RMBS"},
		{"rmbs", "", "RMBS"},
		{"BRK/B", "US", "BRK-B"},
		{"BF.B", "US", "BF-B"},
		{"000660", "KS", "000660.KS"},
		{"700", "HK", "0700.HK"},
		{"9988", "HK", "9988.HK"},
		{"6857", "JP", "6857.T"},
		{"2330", "TT", "2330.TW"},
		{"ASML", "NA", "ASML.AS"},
		{"BESI", "NA", "BESI.AS"},
		{"D05", "SP", "D05.SI"},
		{"X", "ZZ", ""},
		{"", "US", ""},
	} {
		if got := ChartSymbol(tc.ticker, tc.exchange); got != tc.want {
			t.Errorf("ChartSymbol(%q, %q) = %q, want %q", tc.ticker, tc.exchange, got, tc.want)
		}
	}
}
