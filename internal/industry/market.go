package industry

import (
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// marketOrder is the markets a reader may keep the companies to, by the
// exchange codes the companies' list uses, in the order the prompt names
// them.
var marketOrder = []string{"US", "CN", "LN", "NA", "FP", "GR", "SW", "IM", "SM", "SS", "DC", "NO", "FH", "JP", "HK", "CH", "KS", "TT", "SP", "IN", "AU"}

// marketFlags are the ways the market flag may be written. A phone may turn
// "--" into a dash.
var marketFlags = map[string]bool{"-m": true, "--m": true, "--market": true, "—m": true, "–m": true}

// ParseMarket reads "<topic> -m <market>": the industry, and the market its
// companies are kept to, as an exchange code. Without the flag the market is
// empty and the companies come from anywhere. A flag with no market, or one
// that is not a code from the list, is an error that lists them.
func ParseMarket(asked string) (topic, market string, err error) {
	words := strings.Fields(asked)
	var rest []string
	for i := 0; i < len(words); i++ {
		if !marketFlags[strings.ToLower(words[i])] {
			rest = append(rest, words[i])
			continue
		}
		if i+1 >= len(words) {
			return "", "", fmt.Errorf("-m needs a market after it. %s", MarketList())
		}
		i++
		code := strings.ToUpper(words[i])
		if _, ok := model.Markets[code]; !ok {
			return "", "", fmt.Errorf("%q is not a market I know. %s", words[i], MarketList())
		}
		market = code
	}
	return strings.Join(rest, " "), market, nil
}

// MarketList names the markets -m takes: "US United States, CN Canada, …".
func MarketList() string {
	parts := make([]string, len(marketOrder))
	for i, code := range marketOrder {
		parts[i] = code + " " + model.Markets[code].Country
	}
	return "The markets are: " + strings.Join(parts, ", ") + "."
}

// MarketLabel names a market for a title: "US", "UK", "Japan".
func MarketLabel(code string) string {
	switch code {
	case "US":
		return "US"
	case "LN":
		return "UK"
	}
	return model.Markets[code].Country
}
