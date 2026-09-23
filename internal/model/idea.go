package model

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

	// Link is how today's news bears on it, in a sentence, and Articles the
	// stories it hangs on, in the brief's numbering.
	Link     string
	Articles []Article

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
