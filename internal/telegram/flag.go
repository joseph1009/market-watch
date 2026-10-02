package telegram

import (
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// flagged is a checked symbol as the reader sees it, with the flag of the
// country it trades in beside it: "🇨🇦 <code>SHOP.CN</code>". The suffixes are
// OpenFIGI's, and some read as another country's -- CN is Canada, CH is
// China -- so the flag says where it is from.
func flagged(symbol, exchange string) string {
	code := "<code>" + escape(symbol) + "</code>"
	if flag := model.Flag(exchange); flag != "" {
		return flag + " " + code
	}
	return code
}

// flaggedSymbol is flagged for a symbol already written for the reader,
// "D05.SP" or "RMBS", as the scorecard keeps them: a suffix that is one of
// the markets names it, and none is the US.
func flaggedSymbol(symbol string) string {
	exchange := "US"
	if at := strings.LastIndex(symbol, "."); at >= 0 {
		if _, ok := model.Markets[symbol[at+1:]]; ok {
			exchange = symbol[at+1:]
		}
	}
	return flagged(symbol, exchange)
}
