package prices

import (
	"strings"
)

// chartSuffix maps the exchange codes a ticker is verified under (OpenFIGI's)
// to the suffix the chart source writes after a symbol on that market. The US
// has none: "RMBS" is Rambus on whichever American exchange it trades.
var chartSuffix = map[string]string{
	"US": "",
	"HK": ".HK",
	"JP": ".T",
	"LN": ".L",
	"NA": ".AS",
	"FP": ".PA",
	"GR": ".DE",
	"SP": ".SI",
	"AU": ".AX",
	"KS": ".KS",
	"TT": ".TW",
	"IN": ".NS",
	"CH": ".SZ", // or .SS, by the code: see ChartSymbol
	"CN": ".TO",
	"SW": ".SW",
	"IM": ".MI",
	"SM": ".MC",
	"SS": ".ST",
	"DC": ".CO",
	"NO": ".OL",
	"FH": ".HE",
}

// ChartSymbol is how the chart source spells a verified listing: "000660.KS"
// for SK Hynix, "0700.HK" for Tencent, "BRK-B" for Berkshire's B shares. Empty
// for an exchange it is not known to cover, rather than a guess that might
// return another company's prices.
func ChartSymbol(ticker, exchange string) string {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	exchange = strings.ToUpper(strings.TrimSpace(exchange))
	if exchange == "" {
		exchange = "US"
	}
	suffix, ok := chartSuffix[exchange]
	if ticker == "" || !ok {
		return ""
	}

	// A Swedish or Danish share class (OpenFIGI's "VOLVB") is written with
	// a dash by the chart source ("VOLV-B"), but whether a last letter is a
	// class cannot be told from the ticker -- Genmab's GMAB is not GMA-B -- so
	// those classes go without a price rather than with a guess.
	switch exchange {
	case "US", "CN":
		// A share class is written with a slash or a dot by the exchanges and
		// with a dash by the chart source.
		ticker = strings.NewReplacer("/", "-", ".", "-").Replace(ticker)
	case "HK":
		// Hong Kong codes are numbers, written to four digits: 700 is 0700.
		for len(ticker) < 4 {
			ticker = "0" + ticker
		}
	case "CH":
		// OpenFIGI has one code for mainland China; the chart source has one
		// for each exchange. Shanghai's codes start with a 6 (or a 9 for its
		// B shares), Shenzhen's with a 0, a 2 or a 3.
		if strings.HasPrefix(ticker, "6") || strings.HasPrefix(ticker, "9") {
			suffix = ".SS"
		}
	}
	return ticker + suffix
}

// minorUnits are the currencies the chart source prices some listings in
// hundredths of: London in pence (GBp), Johannesburg in cents (ZAc), Tel Aviv
// in agorot (ILA). The history reads the currency in capitals, so GBp arrives
// as GBP. A rate is only ever used as a ratio of two days, which a hundredth
// leaves unchanged, so each maps to its whole currency.
var minorUnits = map[string]string{"GBX": "GBP", "ZAC": "ZAR", "ILA": "ILS"}

// DollarRateChart is the chart symbol for what one unit of currency is worth
// in US dollars: "JPYUSD=X" for the yen. Empty for the dollar itself.
func DollarRateChart(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if whole, ok := minorUnits[currency]; ok {
		currency = whole
	}
	if currency == "" || currency == "USD" {
		return ""
	}
	return currency + "USD=X"
}
