package model

// Markets are the exchanges a company may be named on, as OpenFIGI codes,
// with the country each stands for. Beyond these the reader is unlikely to be
// able to trade, and the further the list stretches the more room there is
// for a symbol to mean two things. Every ticker is checked under one of them.
var Markets = map[string]Market{
	"US": {"United States", "🇺🇸"},
	"CN": {"Canada", "🇨🇦"}, // not China: that is CH
	"LN": {"United Kingdom", "🇬🇧"},
	"NA": {"Netherlands", "🇳🇱"},
	"FP": {"France", "🇫🇷"},
	"GR": {"Germany", "🇩🇪"},
	"SW": {"Switzerland", "🇨🇭"},
	"IM": {"Italy", "🇮🇹"},
	"SM": {"Spain", "🇪🇸"},
	"SS": {"Sweden", "🇸🇪"},
	"DC": {"Denmark", "🇩🇰"},
	"NO": {"Norway", "🇳🇴"},
	"FH": {"Finland", "🇫🇮"},
	"JP": {"Japan", "🇯🇵"},
	"HK": {"Hong Kong", "🇭🇰"},
	"CH": {"China", "🇨🇳"}, // Shanghai and Shenzhen both
	"KS": {"South Korea", "🇰🇷"},
	"TT": {"Taiwan", "🇹🇼"},
	"SP": {"Singapore", "🇸🇬"},
	"IN": {"India", "🇮🇳"},
	"AU": {"Australia", "🇦🇺"},
}

// Market is the country an exchange code stands for.
type Market struct {
	Country, Flag string
}

// MarketOf names where a listing trades, flag first: "🇯🇵 Japan". Empty for
// a code that is not one of the Markets.
func MarketOf(exchange string) string {
	m, ok := Markets[exchange]
	if !ok {
		return ""
	}
	return m.Flag + " " + m.Country
}

// Flag is the flag of the country a listing trades in: "🇯🇵" for JP. Empty for
// a code that is not one of the Markets, and for none.
func Flag(exchange string) string {
	return Markets[exchange].Flag
}
