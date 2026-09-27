package market

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/prices"
)

// Listing is a company as the exchange lists it: its name, how much it is
// worth, and the industry the exchange files it under. From Nasdaq's
// screener, which covers every US listing in one request.
type Listing struct {
	Symbol, Name       string
	Country            string
	Sector, Industry   string
	MarketCap          float64
	NotListedInAmerica bool // a Singapore share, priced from the chart source
}

// Rules are what a company must be to be considered at all: worth enough,
// priced high enough and traded enough that its price means something, and
// with a year's history to measure.
type Rules struct {
	MinMarketCap    float64
	MinPrice        float64
	MinDollarVolume float64
	MinHistory      int
}

// DefaultRules: a company worth at least US$2bn, a share of at least US$5, at
// least US$20m changing hands on an average day over three months, and
// thirteen months of trading.
var DefaultRules = Rules{MinMarketCap: 2e9, MinPrice: 5, MinDollarVolume: 20e6, MinHistory: Year + Month}

// Benchmark is the index fund every return is measured against.
const Benchmark = "SPY"

// Stock is one company's standing, from its bars.
type Stock struct {
	Listing
	Price    float64
	Sessions int

	// Returns over the last two years, year, year less its latest month, six
	// months, three months and month, as fractions; NaN where the history is
	// short.
	R24, R12, R12x1, R6, R3, R1 float64

	// Volatility is the annualised swing over the year, and Recent over the
	// last month.
	Volatility, Recent float64

	MA50, MA200 float64

	// Above50Before says whether the share stood above its fifty-day average
	// a month ago, to tell a group that is turning up from one that was
	// already there.
	Above50Before bool

	// DollarVolume is the average value traded a session over three months;
	// YearAgo the same three months a year before, and Before the six months
	// before the latest three.
	DollarVolume, YearAgo, Before float64

	// BiggestDay is the largest single-day rise in the year, and
	// BiggestRecent in the last six months, as log moves.
	BiggestDay, BiggestRecent float64

	// Score is where it ranks among the leaders, from 0 to 1, and Why says
	// why it was left out of them, where it was.
	Score float64
	Why   string
}

// Steady is the year's return for each unit of its swing: a share that rose
// 40% without lurching beats one that rose 60% in fits.
func (s Stock) Steady() float64 {
	if s.Volatility <= 0 || math.IsNaN(s.R12) {
		return math.NaN()
	}
	return s.R12 / s.Volatility
}

// Measure reads each listing's standing from the panel. Listings the panel
// has no bars for are left out.
func Measure(p *Panel, listings []Listing) []Stock {
	var out []Stock
	for _, l := range listings {
		ser := p.Get(l.Symbol)
		if ser == nil || ser.Price() <= 0 {
			continue
		}
		out = append(out, measure(ser, l))
	}
	return out
}

// MeasureSeries is Measure for one series read from elsewhere.
func MeasureSeries(ser *Series, l Listing) Stock { return measure(ser, l) }

func measure(ser *Series, l Listing) Stock {
	s := Stock{
		Listing:  l,
		Price:    ser.Price(),
		Sessions: ser.History(),
		// The store reaches two years less a few days, about 497 sessions
		// against the 504 of two full years, so the two years' return is
		// read from a month short of them, where every share that traded
		// throughout has a close.
		R24:           ser.Return(TwoYears - Month),
		R12:           ser.Return(Year),
		R12x1:         ser.ReturnBetween(Year, Month),
		R6:            ser.Return(HalfYear),
		R3:            ser.Return(Quarter),
		R1:            ser.Return(Month),
		Volatility:    ser.Volatility(Year),
		Recent:        ser.Volatility(Month),
		MA50:          ser.Average(50, 0),
		MA200:         ser.Average(200, 0),
		DollarVolume:  ser.DollarVolume(Quarter, 0),
		YearAgo:       ser.DollarVolume(Quarter, Year),
		Before:        ser.DollarVolume(HalfYear, Quarter),
		BiggestDay:    ser.BiggestDay(Year),
		BiggestRecent: ser.BiggestDay(HalfYear),
	}
	if ma := ser.Average(50, Month); ma > 0 {
		s.Above50Before = ser.closeAt(Month) > ma
	}
	return s
}

// Eligible says why a stock cannot be considered, or "" where it can. A
// Singapore share is held to none of the size and trading floors: the list
// of them is fixed, and small by the US's measure.
func (s Stock) Eligible(r Rules) string {
	switch {
	case !s.NotListedInAmerica && !prices.CommonShare(s.Symbol):
		return "not a common share"
	case fundLike(s.Name):
		return "a fund, a trust of that kind, a blank-cheque company or a security other than a share"
	case s.Sessions < r.MinHistory:
		return "less than thirteen months of trading"
	case s.NotListedInAmerica:
		return ""
	case s.MarketCap < r.MinMarketCap:
		return "worth less than US$2bn"
	case s.Price < r.MinPrice:
		return "a share under US$5"
	case s.DollarVolume < r.MinDollarVolume:
		return "under US$20m traded on an average day"
	}
	return ""
}

// Leaders ranks the stocks that have done best, and returns the best n.
//
// A stock must be eligible, still above its two-hundred-day average, and
// have risen for more than one day's news: one whose year's rise came mostly
// in a single day is a jump, not a trend, and one that jumped and then went
// still is pinned to a takeover offer. The rest are ranked on four
// measures, each against the others: the two years' return, the year's less
// its latest month (the month tends to reverse), six months', and the
// year's return for each unit of its swing. Their mean rank is the score.
func Leaders(stocks []Stock, r Rules, n int) (leaders, left []Stock) {
	var pool []Stock
	for _, s := range stocks {
		s.Why = s.Eligible(r)
		if s.Why == "" {
			switch {
			case s.MA200 <= 0 || s.Price <= s.MA200:
				s.Why = "below its 200-day average"
			case s.BiggestRecent > math.Log(1.15) && s.Recent < 0.08:
				s.Why = "jumped and then went still, as a share pinned to a takeover offer does"
			case s.R12 > 0 && s.BiggestDay > 0.5*math.Log1p(s.R12):
				s.Why = "most of the year's rise came in one day"
			}
		}
		if s.Why != "" {
			left = append(left, s)
			continue
		}
		pool = append(pool, s)
	}

	measures := []func(Stock) float64{
		func(s Stock) float64 { return s.R24 },
		func(s Stock) float64 { return s.R12x1 },
		func(s Stock) float64 { return s.R6 },
		func(s Stock) float64 { return s.Steady() },
	}
	scores := make([][]float64, len(pool))
	for _, m := range measures {
		values := make([]float64, len(pool))
		for i, s := range pool {
			values[i] = m(s)
		}
		for i, rank := range percentiles(values) {
			if !math.IsNaN(rank) {
				scores[i] = append(scores[i], rank)
			}
		}
	}
	for i := range pool {
		pool[i].Score = mean(scores[i])
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
	if n > 0 && len(pool) > n {
		pool = pool[:n]
	}
	return pool, left
}

// Industry is one of the exchange's industries, read across every eligible
// company in it rather than its best few.
type Industry struct {
	Name, Sector string
	Members      int

	// The median member's return over three, six and twelve months, less
	// the index's over the same stretch, as fractions.
	Median3, Median6, Median12 float64

	// Breadth is the share of members above their two-hundred-day average;
	// Breadth50 above their fifty-day average now, and Breadth50Before a
	// month ago.
	Breadth, Breadth50, Breadth50Before float64

	// MoneyYear is the value traded over three months against the same
	// three months a year before; MoneyRecent against the six months before
	// them. Above one, more money is going into the industry's shares.
	MoneyYear, MoneyRecent float64

	// Mentions is how many of the recent briefs' headlines named one of its
	// members.
	Mentions int

	// Popular scores how far it has already risen, and Early how far it is
	// starting to, among the industries not yet popular; each from 0 to 1.
	Popular, Early float64

	// Top are its members, best six months first.
	Top []string
}

// minMembers is how many eligible companies an industry needs to be read as
// one: fewer, and the "industry" is a company or two.
const minMembers = 4

// Industries reads every industry with enough eligible members, against the
// index's returns, and scores each as popular and as early.
//
// Popular is the mean rank of the median member's six- and twelve-month
// returns, the share above their long average, the money coming in against a
// year ago, and the headlines. Early is for the industries whose year is
// still behind the typical industry's: the mean rank of the three months'
// return, the rise in the share above their fifty-day average over the month,
// the money coming in against the six months before, and the headlines. Only
// industries whose three months are ahead of the typical industry's, and
// whose share above their fifty-day average rose by more than the typical
// industry's did, are scored early; the rest score zero. Both are measured
// against the other industries rather than against nothing: in a month when
// the whole market slips, the share above the fifty-day average falls
// almost everywhere, and the industry falling least is the one turning.
func Industries(stocks []Stock, bench Stock, r Rules, mentions map[string]int) []Industry {
	groups := map[string][]Stock{}
	for _, s := range stocks {
		if s.Eligible(r) != "" || s.Industry == "" || s.NotListedInAmerica {
			continue
		}
		groups[s.Industry] = append(groups[s.Industry], s)
	}

	var out []Industry
	for name, members := range groups {
		if len(members) < minMembers {
			continue
		}
		ind := Industry{Name: name, Sector: members[0].Sector, Members: len(members)}
		var r3, r6, r12 []float64
		above, above50, above50Before := 0, 0, 0
		var dv, dvYear, dvBefore float64
		for _, m := range members {
			r3 = append(r3, m.R3-bench.R3)
			r6 = append(r6, m.R6-bench.R6)
			r12 = append(r12, m.R12-bench.R12)
			if m.MA200 > 0 && m.Price > m.MA200 {
				above++
			}
			if m.MA50 > 0 && m.Price > m.MA50 {
				above50++
			}
			if m.Above50Before {
				above50Before++
			}
			dv += m.DollarVolume
			dvYear += m.YearAgo
			dvBefore += m.Before
			ind.Mentions += mentions[m.Symbol]
		}
		ind.Median3, ind.Median6, ind.Median12 = median(r3), median(r6), median(r12)
		n := float64(len(members))
		ind.Breadth, ind.Breadth50, ind.Breadth50Before = float64(above)/n, float64(above50)/n, float64(above50Before)/n
		if dvYear > 0 {
			ind.MoneyYear = dv / dvYear
		}
		if dvBefore > 0 {
			ind.MoneyRecent = dv / dvBefore
		}
		sort.SliceStable(members, func(i, j int) bool { return nanLast(members[i].R6) > nanLast(members[j].R6) })
		for _, m := range members {
			ind.Top = append(ind.Top, m.Symbol)
		}
		out = append(out, ind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	score := func(list []int, measures ...func(Industry) float64) []float64 {
		scores := make([][]float64, len(list))
		for _, m := range measures {
			values := make([]float64, len(list))
			for k, i := range list {
				values[k] = m(out[i])
			}
			for k, rank := range percentiles(values) {
				if !math.IsNaN(rank) {
					scores[k] = append(scores[k], rank)
				}
			}
		}
		result := make([]float64, len(list))
		for k := range list {
			result[k] = mean(scores[k])
		}
		return result
	}

	all := make([]int, len(out))
	var twelve, three, turn []float64
	for i, d := range out {
		all[i] = i
		twelve = append(twelve, d.Median12)
		three = append(three, d.Median3)
		turn = append(turn, d.Breadth50-d.Breadth50Before)
	}
	for k, v := range score(all,
		func(d Industry) float64 { return d.Median6 },
		func(d Industry) float64 { return d.Median12 },
		func(d Industry) float64 { return d.Breadth },
		func(d Industry) float64 { return d.MoneyYear },
		func(d Industry) float64 { return float64(d.Mentions) },
	) {
		out[all[k]].Popular = v
	}

	typical12, typical3, typicalTurn := median(twelve), median(three), median(turn)
	var early []int
	for i, d := range out {
		if d.Median12 < typical12 && d.Median3 > typical3 && d.Breadth50-d.Breadth50Before > typicalTurn {
			early = append(early, i)
		}
	}
	for k, v := range score(early,
		func(d Industry) float64 { return d.Median3 },
		func(d Industry) float64 { return d.Breadth50 - d.Breadth50Before },
		func(d Industry) float64 { return d.MoneyRecent },
		func(d Industry) float64 { return float64(d.Mentions) },
	) {
		out[early[k]].Early = v
	}
	return out
}

// Popular is the n industries that have risen furthest, most first.
func Popular(inds []Industry, n int) []Industry {
	return top(inds, n, func(d Industry) float64 { return d.Popular })
}

// Early is the n industries that are starting to rise, most first.
func Early(inds []Industry, n int) []Industry {
	return top(inds, n, func(d Industry) float64 { return d.Early })
}

func top(inds []Industry, n int, by func(Industry) float64) []Industry {
	var out []Industry
	for _, d := range inds {
		if by(d) > 0 {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return by(out[i]) > by(out[j]) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Move is a share that moved far beyond its usual on the latest session.
type Move struct {
	Listing
	Date    time.Time
	Price   float64
	Percent float64 // the session's move, as a fraction

	// Times is the move against the share's usual daily move, and Busy the
	// value traded against its usual session's.
	Times, Busy float64
}

// Moves finds the shares whose latest session's move was at least minTimes
// their usual daily move, on at least minBusy times their usual trading:
// the market repricing them, not a few trades pushing them about. Largest
// against their usual first.
func Moves(p *Panel, listings []Listing, r Rules, minTimes, minBusy float64) []Move {
	var out []Move
	for _, l := range listings {
		ser := p.Get(l.Symbol)
		if ser == nil {
			continue
		}
		s := measure(ser, l)
		if s.Eligible(Rules{MinMarketCap: r.MinMarketCap, MinPrice: r.MinPrice, MinDollarVolume: r.MinDollarVolume, MinHistory: usualDays + 2}) != "" {
			continue
		}
		bar, ok := ser.Last()
		usual := ser.Usual()
		usualValue := ser.DollarVolume(usualDays, 1)
		prev := ser.closeAt(1)
		if !ok || usual <= 0 || usualValue <= 0 || prev <= 0 {
			continue
		}
		move := math.Log(bar.Close / prev)
		times := math.Abs(move) / usual
		busy := bar.Close * bar.Volume / usualValue
		if times < minTimes || busy < minBusy {
			continue
		}
		out = append(out, Move{Listing: l, Date: p.Last(), Price: bar.Close, Percent: bar.Close/prev - 1, Times: times, Busy: busy})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Times > out[j].Times })
	return out
}

// fundLike reports whether a listing's name is a fund's, a blank-cheque
// company's, or a security other than a common share: none is a business
// whose prospects a verdict could weigh.
func fundLike(name string) bool {
	upper := " " + strings.ToUpper(name) + " "
	for _, word := range []string{
		" ETF", " ETN", " FUND", " FUNDS ", "PROSHARES", "DIREXION", "ISHARES", "SPDR", "SHARES TRUST",
		" ACQUISITION CORP", " ACQUISITION COMPANY", " ACQUISITION LTD", " ACQUISITION HOLDINGS",
		" PREFERRED", " NOTES ", " DEBENTURES", " WARRANT", " RIGHTS ", " UNITS ", " SUBORDINATED",
	} {
		if strings.Contains(upper, word) {
			return true
		}
	}
	return strings.Contains(upper, "%")
}

// percentiles ranks values between 0 (lowest) and 1 (highest), ties
// sharing their average rank; NaN stays NaN. Each rank is the middle of its
// share of the range, so the lowest of n is 1/2n rather than zero: a score of
// zero means not scored, and the last of the early industries is still one.
func percentiles(values []float64) []float64 {
	type item struct {
		v float64
		i int
	}
	var items []item
	for i, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			items = append(items, item{v, i})
		}
	}
	out := make([]float64, len(values))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(items) == 0 {
		return out
	}
	sort.Slice(items, func(a, b int) bool { return items[a].v < items[b].v })
	for a := 0; a < len(items); {
		b := a
		for b+1 < len(items) && items[b+1].v == items[a].v {
			b++
		}
		rank := (float64(a+b)/2 + 0.5) / float64(len(items))
		for k := a; k <= b; k++ {
			out[items[k].i] = rank
		}
		a = b + 1
	}
	return out
}

func mean(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vs {
		sum += v
	}
	return sum / float64(len(vs))
}

func median(vs []float64) float64 {
	var clean []float64
	for _, v := range vs {
		if !math.IsNaN(v) {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return math.NaN()
	}
	sort.Float64s(clean)
	mid := len(clean) / 2
	if len(clean)%2 == 1 {
		return clean[mid]
	}
	return (clean[mid-1] + clean[mid]) / 2
}

func nanLast(v float64) float64 {
	if math.IsNaN(v) {
		return math.Inf(-1)
	}
	return v
}
