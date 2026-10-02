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
		{"600519", "CH", "600519.SS"}, // Kweichow Moutai, Shanghai
		{"688981", "CH", "688981.SS"}, // SMIC, Shanghai's STAR board
		{"300750", "CH", "300750.SZ"}, // CATL, Shenzhen
		{"000333", "CH", "000333.SZ"}, // Midea, Shenzhen
		{"X", "ZZ", ""},
		{"", "US", ""},
	} {
		if got := ChartSymbol(tc.ticker, tc.exchange); got != tc.want {
			t.Errorf("ChartSymbol(%q, %q) = %q, want %q", tc.ticker, tc.exchange, got, tc.want)
		}
	}
}

func TestDollarRateChartNamesEachCurrencyAgainstTheDollar(t *testing.T) {
	for _, tc := range []struct{ currency, want string }{
		{"JPY", "JPYUSD=X"},
		{"krw", "KRWUSD=X"},
		{"GBP", "GBPUSD=X"}, // London's pence, as the history spells them
		{"ZAC", "ZARUSD=X"},
		{"ILA", "ILSUSD=X"},
		{"USD", ""},
		{"", ""},
	} {
		if got := DollarRateChart(tc.currency); got != tc.want {
			t.Errorf("DollarRateChart(%q) = %q, want %q", tc.currency, got, tc.want)
		}
	}
}
