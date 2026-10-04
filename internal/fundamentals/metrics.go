package fundamentals

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// Value is a figure that may not exist. A zero float would be a lie: a company
// that does not report inventory has no inventory line, not inventory of nil.
type Value struct {
	Amount float64
	Known  bool
}

func known(v float64) Value { return Value{Amount: v, Known: true} }

// tagRef is one taxonomy-and-tag pair a figure might be filed under.
type tagRef struct {
	taxonomy string
	tag      string
}

func usGAAP(tags ...string) []tagRef { return refs("us-gaap", tags) }
func ifrs(tags ...string) []tagRef   { return refs("ifrs-full", tags) }

func refs(taxonomy string, tags []string) []tagRef {
	out := make([]tagRef, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tagRef{taxonomy: taxonomy, tag: tag})
	}
	return out
}

// concept is one line we try to read, with every tag filers actually use for
// it.
//
// The alternatives are not interchangeable in meaning so much as in practice.
// The same figure is tagged differently by industry, by filing age and by
// accounting standard: a US filer tags revenue under us-gaap, while a company
// filing a 20-F tags it under IFRS. Alibaba reports in yuan under us-gaap,
// TSMC in Taiwan dollars under IFRS, and Toyota files both. A single tag leaves
// large companies looking like they report nothing at all.
type concept struct {
	key  string
	refs []tagRef
}

func both(key string, us, international []tagRef) concept {
	return concept{key: key, refs: append(append([]tagRef{}, us...), international...)}
}

var incomeConcepts = []concept{
	both("revenue",
		usGAAP("RevenueFromContractWithCustomerExcludingAssessedTax", "Revenues", "SalesRevenueNet",
			"RevenueFromContractWithCustomerIncludingAssessedTax"),
		ifrs("Revenue", "RevenueFromContractsWithCustomers")),
	both("grossProfit", usGAAP("GrossProfit"), ifrs("GrossProfit")),
	both("operatingIncome",
		usGAAP("OperatingIncomeLoss"),
		ifrs("ProfitLossFromOperatingActivities")),
	both("netIncome",
		usGAAP("NetIncomeLoss", "ProfitLoss"),
		ifrs("ProfitLossAttributableToOwnersOfParent", "ProfitLoss")),
	both("researchDevelopment",
		usGAAP("ResearchAndDevelopmentExpense"),
		ifrs("ResearchAndDevelopmentExpense")),
	both("epsDiluted",
		usGAAP("EarningsPerShareDiluted", "EarningsPerShareBasicAndDiluted"),
		ifrs("DilutedEarningsLossPerShare")),
	both("operatingCashFlow",
		usGAAP("NetCashProvidedByUsedInOperatingActivities",
			"NetCashProvidedByUsedInOperatingActivitiesContinuingOperations"),
		ifrs("CashFlowsFromUsedInOperatingActivities")),
	both("capitalExpenditure",
		usGAAP("PaymentsToAcquirePropertyPlantAndEquipment", "PaymentsToAcquireProductiveAssets"),
		ifrs("PurchaseOfPropertyPlantAndEquipmentClassifiedAsInvestingActivities")),

	// Read for the sensitivity (sensitivity.go) rather than the table: the
	// tax rate a change in profit is taxed at, and the shares it is spread
	// over.
	both("pretaxIncome",
		usGAAP("IncomeLossFromContinuingOperationsBeforeIncomeTaxesExtraordinaryItemsNoncontrollingInterest",
			"IncomeLossFromContinuingOperationsBeforeIncomeTaxesMinorityInterestAndIncomeLossFromEquityMethodInvestments"),
		ifrs("ProfitLossBeforeTax")),
	both("incomeTax", usGAAP("IncomeTaxExpenseBenefit"), ifrs("IncomeTaxExpenseContinuingOperations")),
	both("dilutedShares",
		usGAAP("WeightedAverageNumberOfDilutedSharesOutstanding"),
		ifrs("AdjustedWeightedAverageShares")),
}

var balanceConcepts = []concept{
	both("assets", usGAAP("Assets"), ifrs("Assets")),
	both("liabilities", usGAAP("Liabilities"), ifrs("Liabilities")),
	both("equity",
		usGAAP("StockholdersEquity", "StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest"),
		ifrs("EquityAttributableToOwnersOfParent", "Equity")),
	both("marketableSecurities",
		// NVIDIA moved its short-term investments to DebtSecuritiesCurrent
		// after October 2025, once it split out the shares it holds; last,
		// so the broader line wins where a filing gives both.
		usGAAP("MarketableSecuritiesCurrent", "ShortTermInvestments", "DebtSecuritiesCurrent"),
		ifrs("OtherCurrentFinancialAssets")),
	both("cash",
		usGAAP("CashAndCashEquivalentsAtCarryingValue",
			"CashCashEquivalentsRestrictedCashAndRestrictedCashEquivalents"),
		ifrs("CashAndCashEquivalents")),
	both("currentAssets", usGAAP("AssetsCurrent"), ifrs("CurrentAssets")),
	both("currentLiabilities", usGAAP("LiabilitiesCurrent"), ifrs("CurrentLiabilities")),
	both("longTermDebt",
		usGAAP("LongTermDebtNoncurrent", "LongTermDebt"),
		ifrs("NoncurrentPortionOfNoncurrentBorrowings", "BorrowingsNoncurrent", "NoncurrentFinancialLiabilities")),
	both("inventory", usGAAP("InventoryNet"), ifrs("Inventories")),
	{key: "sharesOutstanding", refs: refs("dei", []string{"EntityCommonStockSharesOutstanding"})},
}

// Year is one fiscal year as the company reported it.
type Year struct {
	Label   string
	End     time.Time
	Figures map[string]Value

	// FromRelease marks a period the filings do not reach yet, read from
	// the company's results release (release.go).
	FromRelease bool `json:",omitempty"`

	// YearAgoRevenue is the revenue of the same three or twelve months a
	// year earlier, for a quarter or a twelve months. Only five quarters are
	// kept, so the older four would otherwise have nothing to grow against.
	YearAgoRevenue Value
}

// Balance is the most recent balance sheet.
type Balance struct {
	AsOf    time.Time
	Figures map[string]Value

	// Form is the filing the newest figure came from. It matters: a foreign
	// filer's most recent balance sheet often sits in an unaudited 6-K while the
	// income statements stop at the last annual report, and comparing the two
	// without saying so invents a consistency that is not there.
	Form string

	// FromRelease marks a balance sheet read from the results release.
	FromRelease bool `json:",omitempty"`

	// Older gives the date of each line taken from an earlier balance sheet
	// than AsOf: one the latest does not give, or, from a release, one that
	// does not match the filings.
	Older map[string]time.Time `json:",omitempty"`
}

// Snapshot is everything read for one company.
type Snapshot struct {
	Ticker  string
	Company string
	CIK     int

	Years   []Year // newest first
	Balance Balance

	// YTD is the current financial year so far, as the latest interim report
	// states it, and PriorYTD the same stretch of the year before. Full years
	// alone can be most of a year out of date: NVIDIA's last annual figures
	// were seven months old while two quarters of trading sat unreported in
	// between. Either may be nil when the company has filed no interim figures.
	YTD      *Year
	PriorYTD *Year

	// Quarters are the latest quarters, each three months on its own, newest
	// first, and TTM the twelve months the last four make (quarters.go).
	// Empty for a filer that files no interim figures in XBRL, as most
	// foreign ones do not.
	Quarters []Year
	TTM      *Year

	// Business is what the company says it does, from the annual report, and
	// Events what it has told the SEC lately. The accounts describe a shape;
	// these say whose shape it is. Empty where neither could be read.
	Business     string
	BusinessFrom string
	Events       []string

	// Price is what the share last traded at, where a quote was available. The
	// accounts are historical; this is today, and the two together are what a
	// multiple means.
	Price *model.Quote

	// Trading is what the share has done over months: where the price sits
	// against its own averages, how much of it changes hands, how far it swings.
	// A filing cannot say any of that, and it is half of what a reader means
	// when they ask how a company is doing.
	Trading *model.Trading

	// News is what has been written about the company lately, after the
	// headlines that only mention it in passing have been dropped. Reported
	// claims rather than filed facts, which the analysis is told to say.
	News []model.Article

	// Expectations is what analysts expect of the company, and what its
	// insiders, short sellers and funds have done, written out already
	// (consensus.Report.Facts). The accounts say what happened; this says
	// what the price was measured against.
	Expectations string

	// ExpectedEPS is the analysts' consensus for the nearest fiscal year, and
	// ExpectedFor that year's end as Nasdaq writes it ("Aug 2026"). Zero where
	// none was read. The sensitivity is measured against it.
	ExpectedEPS float64
	ExpectedFor string

	// Release is the company's latest results announcement in its own words,
	// and ReleaseFrom which one. It carries what the XBRL does not: the
	// outlook for the next quarter, and the business measures behind the
	// totals.
	Release     string
	ReleaseFrom string

	// Peers is where the company stands in its industry group, each company
	// on its latest twelve months (peers.go). Nil where no group could be read.
	Peers *PeerGroup

	// Backdrop is the commodities, the dollar and the cost of money on the
	// day, written out already (prices.Reading.Line), for the company whose
	// fortunes follow one of them.
	Backdrop []string

	// Currency is what the company reports money in. Filers use their own:
	// Alibaba reports in yuan, TSMC in Taiwan dollars, Toyota in yen. Printing
	// those under a dollar heading would be a straightforward falsehood.
	Currency string

	// Missing names the lines this filer does not report, so the analysis can
	// say so rather than treat an absence as a zero.
	Missing []string

	// ReleaseAdded says the release's own figures were added for the periods
	// the filings do not reach yet (release.go).
	ReleaseAdded bool `json:",omitempty"`

	// obs is every figure read, by line, and years how many full years are
	// shown: kept so the periods can be built again once a release's figures
	// are added. Neither is saved.
	obs   map[string][]Observation
	years int
}

// Fetch reads a company's reported figures: the last few fiscal years of the
// income and cash-flow lines, and the latest balance sheet.
func (c *Client) Fetch(ctx context.Context, ticker string, years int) (Snapshot, error) {
	cik, name, err := c.Lookup.LookupCIK(ctx, ticker)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Ticker: strings.ToUpper(ticker), Company: name, CIK: cik}

	type result struct {
		key string
		obs []Observation
		err error
	}
	all := append(append([]concept{}, incomeConcepts...), balanceConcepts...)
	results := make([]result, len(all))

	sem := make(chan struct{}, defaultConcurrency)
	var wg sync.WaitGroup
	for i, con := range all {
		wg.Add(1)
		go func(i int, con concept) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			obs, err := c.series(ctx, cik, con)
			results[i] = result{key: con.key, obs: obs, err: err}
		}(i, con)
	}
	wg.Wait()

	byKey := make(map[string][]Observation, len(all))
	for _, r := range results {
		switch {
		case errors.Is(r.err, ErrNotReported):
			snap.Missing = append(snap.Missing, r.key)
		case r.err != nil:
			return Snapshot{}, fmt.Errorf("read %s for %s: %w", r.key, snap.Ticker, r.err)
		default:
			byKey[r.key] = r.obs
		}
	}
	sort.Strings(snap.Missing)

	// Older figures a share put on today's basis (splits.go). Best-effort: a
	// company that tags no split, or whose tag cannot be read, is left as
	// filed.
	if tagged, err := c.Concept(ctx, cik, "us-gaap", splitTag); err == nil {
		adjustForSplits(byKey, splits(tagged, byKey["dilutedShares"]))
	}

	snap.Currency = reportingCurrency(byKey)
	snap.obs, snap.years = byKey, years
	snap.build()
	if len(snap.Years) == 0 && len(snap.Balance.Figures) == 0 {
		return Snapshot{}, fmt.Errorf("%s files with the SEC but reports no figures this reads", snap.Ticker)
	}
	return snap, nil
}

// build lines the figures read up into years, the year so far, quarters and
// the balance sheet.
func (s *Snapshot) build() {
	s.Years = buildYears(s.obs, s.years, s.Currency)
	var lastYear time.Time
	if len(s.Years) > 0 {
		lastYear = s.Years[0].End
	}
	s.YTD, s.PriorYTD = buildYTD(s.obs, s.Currency, lastYear)
	s.Quarters, s.TTM = buildQuarters(s.obs, s.Currency)
	s.Balance = buildBalance(s.obs, s.Currency)
}

// buildYears lines the annual figures up by fiscal year end. Years are matched
// on the period end rather than the fiscal-year label, because a filer whose
// year ends in January labels it inconsistently across tags.
func buildYears(byKey map[string][]Observation, want int, currency string) []Year {
	years := map[string]*Year{}
	for _, con := range incomeConcepts {
		for _, o := range Annual(keepCurrency(byKey[con.key], currency)) {
			key := o.End.Format(time.DateOnly)
			y, ok := years[key]
			if !ok {
				y = &Year{
					End:     o.End,
					Label:   MonthSpan(o.End, 12),
					Figures: map[string]Value{},
				}
				years[key] = y
			}
			y.Figures[con.key] = known(o.Value)
		}
	}

	out := make([]Year, 0, len(years))
	for _, y := range years {
		out = append(out, *y)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].End.After(out[j].End) })
	if want > 0 && len(out) > want {
		out = out[:want]
	}
	return out
}

func buildBalance(byKey map[string][]Observation, currency string) Balance {
	b := Balance{Figures: map[string]Value{}}
	dates := map[string]time.Time{}
	var cover Observation
	for _, con := range balanceConcepts {
		instants := Instant(keepCurrency(byKey[con.key], currency))
		if len(instants) == 0 {
			continue
		}
		latest := instants[0]
		b.Figures[con.key] = known(latest.Value)
		dates[con.key] = latest.End

		// The share count is from the filing's cover page, counted on a day
		// weeks after the quarter closed. Left to date the balance sheet, it
		// made Micron's 28 May balance sheet read as at 17 June, and twenty
		// days younger than it was.
		if con.key == "sharesOutstanding" {
			cover = latest
			continue
		}
		if latest.End.After(b.AsOf) {
			b.AsOf, b.Form = latest.End, latest.Form
		}
	}
	if b.AsOf.IsZero() {
		b.AsOf, b.Form = cover.End, cover.Form
	}
	for key, end := range dates {
		if key != "sharesOutstanding" && daysBetween(b.AsOf, end) > 3 {
			if b.Older == nil {
				b.Older = map[string]time.Time{}
			}
			b.Older[key] = end
		}
	}
	return b
}

// Figure reads a line, reporting whether the company disclosed it.
func (y Year) Figure(key string) Value { return y.Figures[key] }

// At is the date of a balance-sheet line: the balance sheet's own, or the
// earlier one it was taken from.
func (b Balance) At(key string) time.Time {
	if at, ok := b.Older[key]; ok {
		return at
	}
	return b.AsOf
}

// CashPot is cash with short-term investments where both are of one date,
// and cash alone otherwise, with its date and whether the investments are in
// it. Until 4 October 2026 NVIDIA's cash at 26 July 2026 was added to its
// investments at 26 October 2025, the last its filings gave under the tag
// then read, and the sum was dated October.
func (b Balance) CashPot() (Value, time.Time, bool) {
	cash, investments := b.Figure("cash"), b.Figure("marketableSecurities")
	at := b.At("cash")
	if cash.Known && investments.Known && b.At("marketableSecurities").Equal(at) {
		return Add(cash, investments), at, true
	}
	return cash, at, false
}

// Figure reads a balance-sheet line.
func (b Balance) Figure(key string) Value { return b.Figures[key] }

// Ratio divides two values, and is unknown whenever either input is or the
// denominator is zero. Every derived figure in the analysis goes through here,
// so a missing line can never silently become a plausible-looking number.
func Ratio(num, den Value) Value {
	if !num.Known || !den.Known || den.Amount == 0 {
		return Value{}
	}
	return known(num.Amount / den.Amount)
}

// Less subtracts, unknown if either side is.
func Less(a, b Value) Value {
	if !a.Known || !b.Known {
		return Value{}
	}
	return known(a.Amount - b.Amount)
}

// FreeCashFlow is operating cash flow less capital spending: the cash a year
// actually left behind, which is the figure a balance sheet has to be read
// against.
func (y Year) FreeCashFlow() Value {
	return Less(y.Figure("operatingCashFlow"), y.Figure("capitalExpenditure"))
}

// series reads every tag a concept might be filed under and merges them by
// period.
//
// Taking the first tag that returns anything looked sufficient and was not:
// NVIDIA tagged revenue as RevenueFromContractWithCustomerExcludingAssessedTax
// until 2022 and as Revenues after it, so the first tag existed, held only
// stale years, and the last four years read as "not reported". Capital spending
// moved the same way in 2020. Where two tags cover one period, the more
// recently filed value wins, which is also how a restatement supersedes the
// figure it replaces.
func (c *Client) series(ctx context.Context, cik int, con concept) ([]Observation, error) {
	merged := map[string]Observation{}
	for i, ref := range con.refs {
		if i > 0 {
			time.Sleep(requestPause) // stay inside the SEC's published rate limit
		}
		obs, err := c.Concept(ctx, cik, ref.taxonomy, ref.tag)
		if errors.Is(err, ErrNotReported) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, o := range obs {
			// Keyed by unit as well as period: a foreign filer reports the same
			// year twice in one filing, once in its own currency and once as a
			// US-dollar convenience translation. Collapsing those into one
			// entry picked between them arbitrarily, and when the dollar copy
			// won, the currency filter then dropped the year entirely.
			key := o.Unit + "|" + o.Start.Format(time.DateOnly) + "|" + o.End.Format(time.DateOnly)
			if prior, seen := merged[key]; seen && !supersedes(o, prior) {
				continue
			}
			merged[key] = o
		}
	}
	if len(merged) == 0 {
		return nil, ErrNotReported
	}

	out := make([]Observation, 0, len(merged))
	for _, o := range merged {
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].End.After(out[j].End) })
	return out, nil
}

// Add sums two values, unknown if either side is. Cash and short-term
// investments are one pot in practice but two lines on the balance sheet, and
// judging liquidity from the cash line alone understates companies that park
// most of it in securities -- which is most large technology firms.
func Add(a, b Value) Value {
	if !a.Known || !b.Known {
		return Value{}
	}
	return known(a.Amount + b.Amount)
}

// supersedes reports whether o should replace prior as the copy of a period.
//
// Filing date alone is not enough. NVIDIA's proxy statement repeats the year's
// net income and is filed months after the annual report, so "latest filed
// wins" handed the year to a DEF 14A -- which the annual filter then rejected
// as not an annual report, leaving the line reading "not reported". Periodic
// reports are the authoritative copy; among equals the later filing wins, which
// is how a restatement replaces what it corrects.
func supersedes(o, prior Observation) bool {
	if r, pr := formRank(o.Form), formRank(prior.Form); r != pr {
		return r > pr
	}
	return o.Filed.After(prior.Filed)
}

// formRank orders the filings a figure can appear in by authority.
func formRank(form string) int {
	switch {
	case strings.HasPrefix(form, "10-K"), strings.HasPrefix(form, "20-F"), strings.HasPrefix(form, "40-F"):
		return 3
	case strings.HasPrefix(form, "10-Q"):
		return 2
	default:
		return 1 // 8-K earnings releases, proxy statements, registration documents
	}
}

// reportingCurrency is the unit the company states money in.
//
// A few filers tag the odd figure in a second currency -- a dollar convenience
// translation beside the statutory yen, say -- so the currency is the one most
// of the figures use, and everything in another unit is then left out rather
// than added to it.
func reportingCurrency(byKey map[string][]Observation) string {
	counts := map[string]int{}
	for key, obs := range byKey {
		if key == "sharesOutstanding" || key == "epsDiluted" {
			continue
		}
		for _, o := range obs {
			if monetary(o.Unit) {
				counts[o.Unit]++
			}
		}
	}

	best, most := "", 0
	for unit, n := range counts {
		if n > most || (n == most && unit < best) {
			best, most = unit, n
		}
	}
	if best == "" {
		return "USD"
	}
	return best
}

// monetary excludes share counts, per-share amounts and ratios, which are not
// denominated in the reporting currency.
func monetary(unit string) bool {
	return unit != "" && unit != "shares" && unit != "pure" && !strings.Contains(unit, "/")
}

// reportedIn keeps figures stated in the company's own currency, and keeps
// per-share and share-count lines whatever their unit says.
func reportedIn(o Observation, currency string) bool {
	// A per-share unit names its currency too: TSMC files earnings per share in
	// TWD/shares and earnings per American share in USD/shares, and taking
	// whichever came first put 1.36 dollars a share in a column of Taiwan
	// dollars.
	if base, _, isPerShare := strings.Cut(o.Unit, "/"); isPerShare {
		return base == currency
	}
	if !monetary(o.Unit) {
		return true
	}
	return o.Unit == currency
}

// keepCurrency drops the copies of a figure stated in another currency, and has
// to run before the annual and instant filters rather than after them. Those
// filters keep one entry per period, so with both copies still present they
// could pick the convenience translation and the currency check would then
// discard the period altogether -- which is how TSMC lost four of five years.
func keepCurrency(obs []Observation, currency string) []Observation {
	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if reportedIn(o, currency) {
			out = append(out, o)
		}
	}
	return out
}

// ytdWindow bounds what counts as a year-to-date period: longer than a single
// quarter is not required, but a full year is a year, not a part of one.
const (
	minYTDDays  = 60
	maxYTDDays  = 330
	periodSlack = 12 // fiscal quarters do not land on the same date each year
)

// buildYTD assembles the current financial year so far and the same stretch of
// the year before.
//
// The comparison is the point. "Revenue was 96,221 million in the half year" is
// a number; "96,221 against 49,116 for the same half last year" is a fact about
// the business, and without the prior period the reader is left to do the
// arithmetic from a full year that covers a different stretch of time.
//
// Only a period ending after lastYear, the latest annual report, counts. A
// foreign filer's half-year reports are often filed without figures the SEC
// can read, so the newest interim figures it holds can be years old: Alibaba's
// "year so far" was the half year to 30 Sep 2020, shown beside its 2026
// accounts and used for a return on equity that meant nothing.
func buildYTD(byKey map[string][]Observation, currency string, lastYear time.Time) (current, prior *Year) {
	// The cumulative period ending on the latest date the company has filed:
	// where a quarter and a year-to-date figure share an end date, the longer
	// one is the one that says how the year is going.
	var end time.Time
	days := 0
	for _, con := range incomeConcepts {
		for _, o := range keepCurrency(byKey[con.key], currency) {
			if !o.Duration() || o.Days() < minYTDDays || o.Days() > maxYTDDays {
				continue
			}
			if !o.End.After(lastYear) {
				continue
			}
			if o.End.After(end) || (o.End.Equal(end) && o.Days() > days) {
				end, days = o.End, o.Days()
			}
		}
	}
	if end.IsZero() {
		return nil, nil
	}

	current = &Year{
		End:     end,
		Label:   spanOfDays(end, days),
		Figures: map[string]Value{},
	}
	prior = &Year{
		End:     end.AddDate(-1, 0, 0),
		Label:   spanOfDays(end.AddDate(-1, 0, 0), days),
		Figures: map[string]Value{},
	}

	for _, con := range incomeConcepts {
		series := keepCurrency(byKey[con.key], currency)
		if o, ok := First(series, matchingPeriod(end, days)); ok {
			current.Figures[con.key] = known(o.Value)
		}
		if o, ok := First(series, matchingPeriod(end.AddDate(-1, 0, 0), days)); ok {
			prior.Figures[con.key] = known(o.Value)
		}
	}

	if len(current.Figures) == 0 {
		return nil, nil
	}
	if len(prior.Figures) == 0 {
		return current, nil
	}
	return current, prior
}

// matchingPeriod finds a period of about the same length ending at about the
// same date, since a company's quarters fall on different calendar days each
// year.
func matchingPeriod(end time.Time, days int) func(Observation) bool {
	return func(o Observation) bool {
		if !o.Duration() {
			return false
		}
		if abs(o.Days()-days) > periodSlack {
			return false
		}
		return abs(daysBetween(o.End, end)) <= periodSlack
	}
}

func daysBetween(a, b time.Time) int { return int(a.Sub(b).Hours() / 24) }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
