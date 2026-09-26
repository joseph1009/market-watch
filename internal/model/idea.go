package model

import "time"

// Idea is a company put in front of the owner with a verdict: one today's
// stories are about, or one they bear on without naming it -- a supplier, a
// customer, a rival.
//
// It differs from a Candidate in what it claims. A candidate is a fact about
// the news: this company was in it. An idea is a judgment about the company:
// researched, set against its accounts and its price, and given a verdict.
// That difference is why anywhere an idea is shown to someone other than the
// owner, it is shown under a note saying a model wrote it and that it is not
// advice to act on.
type Idea struct {
	// Name is the company as the research wrote it, Listed as the exchange
	// registered it; the second is what proves the ticker is real.
	Name   string
	Listed string

	Ticker   string
	Exchange string

	// Connected is false for a company the day's stories are about, and true
	// for one they bear on without naming.
	Connected bool

	// Followed is a company the watchlists follow, chosen by the screen of
	// the followed companies rather than found by the research. Only BUY and
	// SELL are shown for one: a HOLD on a company the reader already follows
	// tells them nothing its section would not.
	Followed bool

	// Link is how today's news bears on it, in a sentence, and Articles the
	// stories it hangs on, in the brief's numbering.
	Link     string
	Articles []Article

	// Event is the dated event ahead that the research found on the web --
	// results, a ruling, a launch -- or nil where it found none in the next
	// ninety days. Found rather than checked, unlike the results date in the
	// facts, which Nasdaq publishes.
	Event *Event

	// Quote is today's move, for a US listing. Trading is a year of daily
	// prices, wherever the chart source has the listing.
	Quote   *Quote
	Trading *Trading

	// Accounts says whether SEC filings were read for it. Most companies
	// without a US listing file nothing there, and a verdict made without
	// accounts has to say so.
	Accounts bool

	// The verdict, as written: BUY, HOLD or SELL over the stated horizon, with a
	// confidence, the case in a sentence or two, the figures that decide it,
	// and the one thing most likely to prove it wrong.
	Verdict    string
	Confidence string
	Case       string
	Numbers    string
	Risk       string

	// Changed is what the news changed in the business and by how much,
	// Moved how far the share went and against what, and Reaction whether
	// the one fits the other: overreacted, underreacted or matched, and why.
	// Together they are the question the section answers -- whether the
	// price move is justified by the change.
	Changed  string
	Moved    string
	Reaction string

	// Catalyst is the dated event ahead the verdict turns on, and Sensitivity
	// the lever in the accounts that decides how much the news is worth: how
	// far a point of margin or of revenue moves the earnings.
	Catalyst    string
	Sensitivity string
}

// Event is something dated ahead that could move a share: results, a ruling,
// a contract, a launch.
type Event struct {
	Name string
	Date time.Time

	// Impact is how much it could move the share, HIGH, MEDIUM or LOW, and
	// Bias which way the research expects it to, BULLISH, BEARISH or NEUTRAL.
	// Either may be empty.
	Impact string
	Bias   string
}

// Shown reports whether the verdict belongs in the section: every verdict on
// a company the research found, and only BUY or SELL on a followed one.
func (i Idea) Shown() bool {
	if i.Verdict == "" {
		return false
	}
	return !i.Followed || i.Verdict != Hold
}

// Symbol is how the idea is written for a reader: "000660.KS", "RMBS".
func (i Idea) Symbol() string {
	switch {
	case i.Ticker == "":
		return ""
	case i.Exchange == "" || i.Exchange == "US":
		return i.Ticker
	default:
		return i.Ticker + "." + i.Exchange
	}
}

// The verdicts an idea may carry.
const (
	Buy  = "BUY"
	Hold = "HOLD"
	Sell = "SELL"
)
