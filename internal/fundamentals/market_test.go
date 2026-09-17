package fundamentals

import (
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func tradingSnapshot() Snapshot {
	day := func(d int) time.Time { return time.Date(2026, time.September, d, 0, 0, 0, 0, time.UTC) }
	return Snapshot{
		Ticker:  "MU",
		Company: "Micron Technology, Inc.",
		Trading: &model.Trading{
			Symbol:   "MU",
			Currency: "USD",
			AsOf:     day(16),
			From:     time.Date(2024, time.September, 16, 0, 0, 0, 0, time.UTC),
			Days:     502,
			Last:     929.65,
			Returns: []model.Return{
				{Over: "1 week", Percent: 4.2, From: 892.15, Since: day(9)},
				{Over: "12 months", Percent: -12.5},
			},
			MA50:        870.10,
			MA200:       640.55,
			High52:      961.00,
			HighAt:      day(9),
			Low52:       344.10,
			LowAt:       time.Date(2026, time.April, 7, 0, 0, 0, 0, time.UTC),
			VWAP30:      881.44,
			VWAP90:      702.13,
			VolumeAvg30: 22_400_000,
			VolumeAvg90: 25_100_000,
			VolumeLast:  41_000_000,
			Volatility:  52.7,
		},
		News: []model.Article{{
			Title:      "Micron lifts capital spending on HBM capacity",
			SourceName: "Reuters",
			Summary:    "The memory maker said it would raise spending to meet demand for high-bandwidth memory.",
			Published:  day(12),
		}},
	}
}

func TestTableCarriesTheTradingHistory(t *testing.T) {
	table := tradingSnapshot().Table()

	for _, want := range []string{
		"How the share has actually traded",
		"502 sessions",
		"50-day average close",
		"200-day average close",
		"52-week high",
		"Average price paid, 30 days",
		"Latest session volume",
		"1.63x the 90-day average",
		"Annualised volatility",
		"+4.2%    (from USD 892.15 on 9 Sep 2026)",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not mention %q", want)
		}
	}
}

// The whole discipline of the analysis rests on the reader being able to tell
// a filed figure from a traded one from a reported claim. Each block has to
// label itself, every time.
func TestTableSeparatesTradedFromFiled(t *testing.T) {
	table := tradingSnapshot().Table()

	if !strings.Contains(table, "not from the filings") {
		t.Error("the trading block does not say it is not filed data")
	}
	if !strings.Contains(table, "None of them forecasts anything") {
		t.Error("the trading block does not disclaim being a forecast")
	}
	if !strings.Contains(table, "not filings") {
		t.Error("the news block does not say press reports are not filings")
	}
}

func TestTableCarriesTheNews(t *testing.T) {
	table := tradingSnapshot().Table()

	for _, want := range []string{
		"What has been reported about the company recently",
		"Micron lifts capital spending on HBM capacity",
		"Reuters, 12 Sep 2026",
		"high-bandwidth memory",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not mention %q", want)
		}
	}
}

// A snapshot with neither is the ordinary case for a company whose price could
// not be read, and it must render without a heading standing over nothing.
func TestTableOmitsWhatItDoesNotHave(t *testing.T) {
	table := Snapshot{Ticker: "X", Company: "X Corp"}.Table()

	if strings.Contains(table, "How the share has actually traded") {
		t.Error("a trading block was rendered with no trading history")
	}
	if strings.Contains(table, "What has been reported") {
		t.Error("a news block was rendered with no news")
	}
}

// The chartists' names for a crossing carry a prediction, which is not
// something two averages can support.
func TestAveragesAreDescribedWithoutTheJargon(t *testing.T) {
	got := sits(870.10, 640.55)

	if !strings.Contains(got, "above") {
		t.Errorf("sits() = %q", got)
	}
	for _, jargon := range []string{"golden", "death", "cross", "bullish", "signal"} {
		if strings.Contains(strings.ToLower(got), jargon) {
			t.Errorf("sits() = %q, which reads as a prediction", got)
		}
	}
}
