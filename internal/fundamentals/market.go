package fundamentals

import (
	"fmt"
	"strings"

	"github.com/joseph1009/market-watch/internal/model"
)

// The two things the filings cannot say.
//
// A set of accounts describes a period that closed months ago. It cannot say
// what the share has been doing since, and it cannot say what has happened to
// the company since. Both are part of the question a reader is actually asking,
// and neither is available anywhere in EDGAR.
//
// They are rendered apart from the filed table, and labelled, because the whole
// discipline of the analysis rests on the reader being able to tell where each
// figure came from. A price average is not a filed fact and a headline is not a
// fact at all.

// TradingFacts is the trading block on its own, for a company whose accounts
// were not read -- most listed outside the US file nothing with the SEC -- so
// its price history is the one set of facts at hand.
func TradingFacts(t *model.Trading) string {
	return Snapshot{Trading: t}.trading()
}

// trading is what the share has done, from daily closes.
func (s Snapshot) trading() string {
	t := s.Trading
	if t == nil || t.Days == 0 {
		return ""
	}
	unit := t.Currency
	if unit == "" {
		unit = "USD"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\nHow the share has actually traded. These come from daily closing prices, not from the filings, and cover %d sessions from %s to %s:\n",
		t.Days, t.From.Format("2 Jan 2006"), t.AsOf.Format("2 Jan 2006"))

	if t.Partial {
		fmt.Fprintf(&b, "%s%s %s   (%s, a session that may still be open, so this is a price and not a close)\n",
			pad("Latest price", 32), unit, price(t.Last), t.AsOf.Format("2 Jan 2006"))
	} else {
		fmt.Fprintf(&b, "%s%s %s on %s\n", pad("Last close", 32),
			unit, price(t.Last), t.AsOf.Format("2 Jan 2006"))
	}

	for _, r := range t.Returns {
		market := ""
		if m, ok := t.MarketOver(r.Over); ok && m.Sign() != "" {
			market = "S&P 500 " + m.Sign() + " over the same stretch"
		}
		switch {
		case r.From <= 0 && market == "":
			fmt.Fprintf(&b, "%s%s\n", pad("Change over "+r.Over, 32), r.Sign())
		case r.From <= 0:
			fmt.Fprintf(&b, "%s%s(%s)\n", pad("Change over "+r.Over, 32), pad(r.Sign(), 9), market)
		case market == "":
			fmt.Fprintf(&b, "%s%s(from %s %s on %s)\n", pad("Change over "+r.Over, 32), pad(r.Sign(), 9),
				unit, price(r.From), r.Since.Format("2 Jan 2006"))
		default:
			fmt.Fprintf(&b, "%s%s(from %s %s on %s; %s)\n", pad("Change over "+r.Over, 32), pad(r.Sign(), 9),
				unit, price(r.From), r.Since.Format("2 Jan 2006"), market)
		}
	}

	if t.MA50 > 0 {
		fmt.Fprintf(&b, "%s%s %s   (last close %s against it)\n", pad("50-day average close", 32),
			unit, price(t.MA50), t.Against(t.MA50))
	}
	if t.MA200 > 0 {
		fmt.Fprintf(&b, "%s%s %s   (last close %s against it)\n", pad("200-day average close", 32),
			unit, price(t.MA200), t.Against(t.MA200))
	}
	if t.MA50 > 0 && t.MA200 > 0 {
		fmt.Fprintf(&b, "%s%s\n", pad("50-day against 200-day", 32), sits(t.MA50, t.MA200))
	}

	if t.High52 > 0 {
		fmt.Fprintf(&b, "%s%s %s on %s   (last close %s from it)\n", pad("52-week high", 32),
			unit, price(t.High52), t.HighAt.Format("2 Jan 2006"), t.Against(t.High52))
	}
	if t.Low52 > 0 {
		fmt.Fprintf(&b, "%s%s %s on %s   (last close %s from it)\n", pad("52-week low", 32),
			unit, price(t.Low52), t.LowAt.Format("2 Jan 2006"), t.Against(t.Low52))
	}

	if t.VWAP30 > 0 {
		fmt.Fprintf(&b, "%s%s %s\n", pad("Average price paid, 30 days", 32), unit, price(t.VWAP30))
	}
	if t.VWAP90 > 0 {
		fmt.Fprintf(&b, "%s%s %s\n", pad("Average price paid, 90 days", 32), unit, price(t.VWAP90))
	}
	if t.VolumeAvg30 > 0 {
		fmt.Fprintf(&b, "%s%s shares\n", pad("Daily volume, 30-day average", 32), shares(t.VolumeAvg30))
	}
	if t.VolumeAvg90 > 0 {
		fmt.Fprintf(&b, "%s%s shares\n", pad("Daily volume, 90-day average", 32), shares(t.VolumeAvg90))
	}
	if t.VolumeLast > 0 {
		label, line := "Latest session volume", shares(t.VolumeLast)+" shares"
		switch busy := t.Busy(); {
		case t.Partial:
			label = "Volume so far today"
			line += "   (an unfinished session, so not comparable with the averages above)"
		case busy > 0:
			line += fmt.Sprintf("   (%.2fx the 90-day average)", busy)
		}
		fmt.Fprintf(&b, "%s%s\n", pad(label, 32), line)
	}
	if t.Volatility > 0 {
		fmt.Fprintf(&b, "%s%.1f%%\n", pad("Annualised volatility", 32), t.Volatility)
	}

	b.WriteString("The average price paid weights each day's close by the shares that changed hands, so it is what the market has actually been paying rather than an average of quoted prices. Annualised volatility is the usual size of this share's daily swings, not their direction.\n")
	b.WriteString("Every figure here describes what the price has already done. None of them forecasts anything, a moving average is not a signal, and a share near a high is not thereby expensive nor one near a low cheap.\n")
	return b.String()
}

// sits says where one average stands against another in words, rather than
// leaving the model to name the crossing. The chartists' terms for these carry
// a prediction, which is not something two averages can support.
func sits(short, long float64) string {
	gap := (short - long) / long * 100
	switch {
	case gap >= 0.05:
		return fmt.Sprintf("the 50-day is %.1f%% above the 200-day, so the recent months have traded higher than the year", gap)
	case gap <= -0.05:
		return fmt.Sprintf("the 50-day is %.1f%% below the 200-day, so the recent months have traded lower than the year", -gap)
	default:
		return "the two are level"
	}
}

// NewsFacts is the news block on its own, for a company whose accounts were
// not read.
func (s Snapshot) NewsFacts() string { return s.news() }

// news is what has been written about the company lately. Its items are
// numbered "News 1", not "[1]": a verdict cites the brief's articles in
// square brackets, and the two must not be confused.
func (s Snapshot) news() string {
	if len(s.News) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nWhat has been reported about the company recently, newest first. These are press reports, not filings: they are what an outlet has said, which is a claim and not a filed fact.\n")
	for i, a := range s.News {
		when := "undated"
		if !a.Published.IsZero() {
			when = a.Published.Format("2 Jan 2006")
		}
		source := a.SourceName
		if source == "" {
			source = "unattributed"
		}
		fmt.Fprintf(&b, "News %d: %s — %s, %s\n", i+1, a.Title, source, when)
		if summary := trimSummary(a.Summary); summary != "" {
			fmt.Fprintf(&b, "    %s\n", summary)
		}
		// The address, so an analysis can list the story among its sources.
		if a.URL != "" {
			fmt.Fprintf(&b, "    Address: %s\n", a.URL)
		}
	}
	b.WriteString("Say when a report is unconfirmed or when it contradicts what the filings show. Where a story would change the figures, say which line it would land on and in which period.\n")
	return b.String()
}

// summaryRunes is how much of a story's own summary is worth carrying. Enough
// to say what happened; not so much that ten of them crowd out the accounts.
const summaryRunes = 320

func trimSummary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) <= summaryRunes {
		return s
	}
	cut := string(rs[:summaryRunes])
	if at := strings.LastIndexAny(cut, ".!?"); at > summaryRunes/2 {
		return cut[:at+1]
	}
	return strings.TrimSpace(cut) + "..."
}

// price writes a share price at the precision it trades in. A yen price with
// two decimals implies a precision the market does not quote in, and a penny
// stock rounded to two loses the move.
func price(v float64) string {
	if v < 1 {
		return fmt.Sprintf("%.4f", v)
	}

	whole, cents, _ := strings.Cut(fmt.Sprintf("%.2f", v), ".")
	if len(whole) <= 3 {
		return whole + "." + cents
	}

	var b strings.Builder
	for i, digit := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String() + "." + cents
}

// shares writes a volume at the scale a reader thinks in.
func shares(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.2fbn", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fm", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}
