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
	"CN": ".SS",
	"CH": ".SZ",
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

	switch exchange {
	case "US":
		// A share class is written with a slash or a dot by the exchanges and
		// with a dash by the chart source.
		ticker = strings.NewReplacer("/", "-", ".", "-").Replace(ticker)
	case "HK":
		// Hong Kong codes are numbers, written to four digits: 700 is 0700.
		for len(ticker) < 4 {
			ticker = "0" + ticker
		}
	}
	return ticker + suffix
}
