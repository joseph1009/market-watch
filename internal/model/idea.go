package model

// Idea is a company put in front of the owner with a verdict: one picked
// under the week's themes, found from the market's numbers, or one whose
// share moved far beyond its usual on the day's news.
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

	// Kind is how it was found: IdeaTheme or IdeaReaction.
	Kind string

	// Theme is the week's theme it was picked under, and Lean which way the
	// research proposed it: "buy" for the part of the industry the market
	// has not paid for, "sell" for the part it has paid too much for.
	Theme string
	Lean  string

	// Link is why it is here, in a sentence -- where it sits in its theme,
	// or how far it moved on what news -- and Articles the stories it hangs
	// on, in the brief's numbering.
	Link     string
	Articles []Article

	// Quote is the last session's move, for a US listing. Trading is a year
	// of daily prices, wherever the chart source has the listing.
	Quote   *Quote
	Trading *Trading

	// Accounts says whether SEC filings were read for it. A verdict made
	// without accounts has to say so, and is never more than low confidence.
	Accounts bool

	// Valuation is its multiples set against its theme's and its own
	// history's, written out for the verdict; Flags the warning signs that
	// it may be priced for more than it can deliver. BuyClosed says why a
	// BUY is not open to it, where it is not: the code turns one into a
	// HOLD, and Overruled says so where it did.
	Valuation string
	Flags     []string
	BuyClosed string
	Overruled string

	// Before is the verdict it was last given as a pick, within the weeks a
	// pick is not repeated -- "BUY on 6 Oct" -- where this one differs.
	Before string

	// The verdict, as written: BUY, HOLD or SELL over the stated horizon, with a
	// confidence, the case in a sentence or two, the figures that decide it,
	// and the one thing most likely to prove it wrong.
	Verdict    string
	Confidence string
	Case       string
	Numbers    string
	Risk       string

	// Value is a theme pick's price set against its theme and its own
	// history, as the verdict weighs it.
	Value string

	// Changed is what the news changed in the business and by how much,
	// Moved how far the share went and against what, and Reaction whether
	// the one fits the other: overreacted, underreacted or matched, and why.
	// Together they are the question a reaction answers -- whether the
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

// How an idea was found.
const (
	IdeaTheme    = "theme"
	IdeaReaction = "reaction"
)

// Shown reports whether the verdict belongs in the section: a BUY or a SELL.
// A HOLD on a pick says the research found nothing to act on, and on a
// reaction that the move matched the news; neither is a recommendation.
func (i Idea) Shown() bool {
	return i.Verdict == Buy || i.Verdict == Sell
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
