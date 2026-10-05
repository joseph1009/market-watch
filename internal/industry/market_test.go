package industry

import (
	"strings"
	"testing"
)

func TestTheMarketFlagKeepsTheCompaniesToOneMarket(t *testing.T) {
	for _, c := range []struct{ asked, topic, market string }{
		{"AI -m us", "AI", "US"},
		{"AI -m US", "AI", "US"},
		{"-m jp robotics", "robotics", "JP"},
		{"electric vehicles --market hk", "electric vehicles", "HK"},
		{"AI —m TT", "AI", "TT"},
		{"AI us", "AI us", ""}, // no flag: taken as asked, every market
		{"robotics", "robotics", ""},
	} {
		topic, market, err := ParseMarket(c.asked)
		if err != nil || topic != c.topic || market != c.market {
			t.Errorf("ParseMarket(%q) = %q, %q, %v; want %q, %q", c.asked, topic, market, err, c.topic, c.market)
		}
	}
}

func TestAnUnknownMarketIsRefusedWithTheList(t *testing.T) {
	for _, asked := range []string{"AI -m usa", "AI -m"} {
		_, _, err := ParseMarket(asked)
		if err == nil || !strings.Contains(err.Error(), "US United States, CN Canada") {
			t.Errorf("ParseMarket(%q) error = %v, want one listing the markets", asked, err)
		}
	}
}
