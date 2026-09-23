package feed

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/joseph1009/market-watch/internal/model"
)

// Result is one collection run: the articles worth summarizing plus whatever
// went wrong along the way. Errors are reported rather than returned so a
// partial run still produces a report, with the gaps visible.
type Result struct {
	Articles []model.Article
	Errors   []SourceError

	// Fetched is how many articles arrived, and Deduped how many distinct
	// stories remained. The gap between them is what duplicate coverage was
	// costing in slots, which is the number worth watching over time.
	Fetched int
	Deduped int

	// Matched is how many of the kept articles a watchlist claimed, and Dropped
	// how many the cap discarded. Together they answer whether the cut is
	// costing anything: a rising Dropped is only a problem if Matched articles
	// are among them.
	Matched int
	Dropped int

	// Triage outcome, all zero when no triager ran. Rated is how many articles
	// got a rating; NewlyMatched how many that keywords missed were placed in a
	// watchlist, and Placements how many placements were added in all. Trivial
	// is how many unmatched articles rated 1 were removed before the cap, and
	// CutImportant how many rated 4 or 5 the cap still discarded -- the number
	// that says whether the cap is set too low.
	Rated        int
	NewlyMatched int
	Placements   int
	Trivial      int
	CutImportant int
	TriageUsage  model.Usage

	// Placed is a sample of the articles judgment put in a watchlist that
	// keyword matching had missed entirely, strongest first.
	//
	// The counts above say how often that happened; they cannot say whether it
	// was right. A watchlist described as a sector is a wider net than a list
	// of tickers, and the only way to tell a good catch from a stretch is to
	// read a few of them, which means keeping a few where they can be read
	// tomorrow rather than in a log line that scrolls away tonight.
	Placed []Placement

	// TriageErr reports a partial or total triage failure. It is not fatal:
	// unrated articles rank on the other signals and the brief still goes out.
	TriageErr error
}

// Placement is one article a watchlist claimed on substance rather than on any
// word it contained.
type Placement struct {
	Title  string
	Rating int
	Groups []string
}

// PlacedSamples is how many placements are kept for later inspection. Enough to
// see what kind of thing is arriving, few enough to read in a chat message.
const PlacedSamples = 5

// AllFailed reports whether every source errored, the one case where sending a
// report would misrepresent an outage as a quiet news day.
func (r Result) AllFailed() bool {
	return len(r.Articles) == 0 && len(r.Errors) > 0
}

// Options configures one collection run.
//
// A struct rather than more positional arguments: Extra was the fifth thing to
// pass, and the SEC filings it carries will not be the last source that is not
// an RSS feed.
type Options struct {
	Sources []model.Source
	Groups  []model.Group
	Max     int

	// Extra are articles gathered outside the feed fetcher -- currently SEC
	// filings from the submissions API. They join before dedupe and scoring, so
	// a filing competes for a place on the same terms as everything else.
	Extra []model.Article

	// Triage, when set, rates and places articles after keyword matching and
	// before the cap. Nil ranks on keyword matches and source weight alone.
	Triage Triager
}

// Triager judges articles by content. It returns the articles in the same order
// with Rating set and watchlist placements added, never removed. An error means
// some articles came back unrated; the articles are still usable.
type Triager interface {
	Triage(ctx context.Context, articles []model.Article, groups []model.Group) ([]model.Article, model.Usage, error)
}

// Collect runs the full pipeline: fetch every source, collapse duplicates, tag
// articles against the watchlists, and keep the most useful Max of them.
func Collect(ctx context.Context, f *Fetcher, opts Options) Result {
	sources, groups, max := opts.Sources, opts.Groups, opts.Max

	// A source with no URL is not a feed -- SEC filings arrive through their own
	// API -- but it still needs a weight, so it stays in sources for scoring and
	// is skipped for fetching.
	fetchable := make([]model.Source, 0, len(sources))
	for _, s := range sources {
		if s.URL != "" {
			fetchable = append(fetchable, s)
		}
	}

	articles, errs := f.Fetch(ctx, fetchable)
	articles = append(articles, opts.Extra...)

	fetched := len(articles)
	articles = DropStale(articles, f.now(), MaxArticleAge)
	articles = Dedupe(articles, sources)
	deduped := len(articles)
	// Matching runs before the cut so watchlist relevance can inform it.
	articles = Match(articles, groups)

	res := Result{Errors: errs, Fetched: fetched, Deduped: deduped}
	if opts.Triage != nil {
		articles = res.triage(ctx, opts.Triage, articles, groups)
	}

	ranked := Limit(articles, sources, 0, f.now())
	kept := ranked
	if max > 0 && len(ranked) > max {
		kept = ranked[:max]
		for _, a := range ranked[max:] {
			if a.Rating >= 4 {
				res.CutImportant++
			}
		}
	}

	for _, a := range kept {
		if len(a.GroupIDs) > 0 {
			res.Matched++
		}
	}
	res.Articles = kept
	res.Dropped = len(ranked) - len(kept)
	return res
}

// triage runs the triager, records what it changed, and removes the trivia: an
// article rated 1 that no watchlist claims. A keyword match is kept here
// whatever its rating, so it is counted and ranked with everything else; whether
// it is worth writing about is decided later, by report.MinSectionRating.
func (r *Result) triage(ctx context.Context, t Triager, articles []model.Article, groups []model.Group) []model.Article {
	before := make(map[string]int, len(articles))
	for _, a := range articles {
		before[a.ID] = len(a.GroupIDs)
	}

	triaged, usage, err := t.Triage(ctx, articles, groups)
	r.TriageUsage, r.TriageErr = usage, err

	out := make([]model.Article, 0, len(triaged))
	for _, a := range triaged {
		if a.Rating > 0 {
			r.Rated++
		}
		if added := len(a.GroupIDs) - before[a.ID]; added > 0 {
			r.Placements += added
			if before[a.ID] == 0 {
				r.NewlyMatched++
				// Only the ones keywords missed entirely are worth sampling:
				// an article that already matched a watchlist and gained a
				// second says nothing about whether the wider net works.
				r.Placed = append(r.Placed, Placement{
					Title:  a.Title,
					Rating: a.Rating,
					Groups: append([]string(nil), a.GroupIDs...),
				})
			}
		}
		if a.Rating == 1 && len(a.GroupIDs) == 0 {
			r.Trivial++
			continue
		}
		out = append(out, a)
	}

	// Strongest first, so the sample shows the placements that actually reach
	// a section rather than whatever happened to be fetched first.
	sort.SliceStable(r.Placed, func(i, j int) bool { return r.Placed[i].Rating > r.Placed[j].Rating })
	if len(r.Placed) > PlacedSamples {
		r.Placed = r.Placed[:PlacedSamples]
	}
	return out
}

// MaxArticleAge is how far back a story may be and still belong in a daily
// brief. Several primary sources publish an archive rather than a news feed:
// the BEA's carries releases back to 2016, and the Fed's monetary feed reaches
// back through years of past meetings. Those items are real and correctly
// dated, but presenting a 2018 GDP release beside today's news invites the
// summarizer to write about it as though it just happened.
//
// A week rather than a day, because a Friday release should still be in view on
// Monday, and because feeds occasionally date an item a little oddly.
const MaxArticleAge = 7 * 24 * time.Hour

// DropStale removes articles older than the cutoff. Undated articles are kept:
// the fetcher already substitutes its own clock for those, so they read as
// today's and there is nothing further to judge them on.
func DropStale(articles []model.Article, now time.Time, maxAge time.Duration) []model.Article {
	if maxAge <= 0 {
		return articles
	}
	cutoff := now.Add(-maxAge)

	out := make([]model.Article, 0, len(articles))
	for _, a := range articles {
		if a.Published.IsZero() || a.Published.After(cutoff) {
			out = append(out, a)
		}
	}
	return out
}

// Dedupe collapses the same story down to one copy, keeping whichever source
// ranks highest. Ties go to the earliest publication, which is the closest
// thing available to the original report.
//
// Two passes. The first matches identical URLs, which catches a feed listing a
// story twice. The second compares headlines, which is the one that matters:
// three outlets covering one story are three different URLs, so URL matching
// alone left them taking three slots in the brief.
func Dedupe(articles []model.Article, sources []model.Source) []model.Article {
	weights := make(map[string]int, len(sources))
	for _, s := range sources {
		weights[s.ID] = s.Weight
	}
	return dedupeByTitle(dedupeByURL(articles, weights), weights, DefaultSimilarity)
}

func dedupeByURL(articles []model.Article, weights map[string]int) []model.Article {
	index := make(map[string]int, len(articles)) // article ID -> position in out
	out := make([]model.Article, 0, len(articles))
	for _, a := range articles {
		pos, seen := index[a.ID]
		if !seen {
			index[a.ID] = len(out)
			out = append(out, a)
			continue
		}

		kept := out[pos]
		if better(a, kept, weights) {
			a = merge(a, kept)
			out[pos] = a
		} else {
			out[pos] = merge(kept, a)
		}
	}
	return out
}

// dedupeByTitle groups near-identical headlines. It is a linear scan against
// the survivors so far -- quadratic in the worst case, which at a few hundred
// articles is nothing, and keeps the grouping easy to reason about.
func dedupeByTitle(articles []model.Article, weights map[string]int, threshold float64) []model.Article {
	clusters := clusterByTitle(articles, weights, threshold)

	out := make([]model.Article, 0, len(clusters))
	for _, c := range clusters {
		a := c.article
		// Corroboration counts outlets, not articles: one outlet running three
		// versions of its own story is not three independent confirmations.
		a.Corroborations = len(c.sources) - 1
		out = append(out, a)
	}
	return out
}

// maxStoryGap is how far apart two copies of one story may be published.
//
// Headline similarity alone was actively destructive against real feeds. The
// agencies title recurring releases to a template, so "Personal Income and
// Outlays, July 2026" and the June, May, April and January editions share four
// words in five and collapsed into a single article, losing four months of
// data. Genuine duplicate coverage appears within hours of the original; a
// recurring release repeats months apart, and that gap separates them where
// wording cannot.
const maxStoryGap = 36 * time.Hour

func within(a, b time.Time, d time.Duration) bool {
	if a.IsZero() || b.IsZero() {
		return true // an undated article is judged on its headline alone
	}
	gap := a.Sub(b)
	if gap < 0 {
		gap = -gap
	}
	return gap <= d
}

// titleCluster is a group of headlines judged to be one story. It is kept as a
// named type so the live check can report what merged with what.
type titleCluster struct {
	article model.Article
	tokens  map[string]bool
	sources map[string]bool
	members []string // titles folded in, for diagnostics
}

func clusterByTitle(articles []model.Article, weights map[string]int, threshold float64) []*titleCluster {
	clusters := make([]*titleCluster, 0, len(articles))
	for _, a := range articles {
		tokens := titleTokens(a.Title)

		var found *titleCluster
		for _, c := range clusters {
			if !within(a.Published, c.article.Published, maxStoryGap) {
				continue
			}
			if sameStory(tokens, c.tokens, threshold) {
				found = c
				break
			}
		}
		if found == nil {
			clusters = append(clusters, &titleCluster{
				article: a,
				tokens:  tokens,
				sources: map[string]bool{a.SourceID: true},
				members: []string{a.Title},
			})
			continue
		}

		found.sources[a.SourceID] = true
		found.members = append(found.members, a.Title)
		if better(a, found.article, weights) {
			// The better-ranked copy takes over, but keeps the headline tokens
			// of the cluster so later members still match against the original.
			found.article = merge(a, found.article)
		} else {
			found.article = merge(found.article, a)
		}
	}
	return clusters
}

// merge folds a discarded copy into the one being kept. Watchlist matches are
// unioned because outlets word headlines differently -- one may name the ticker
// where another only names the company -- and the story qualifies if any of
// them matched. The earliest publication wins as the closest thing to when the
// story broke. The discarded copy's source is remembered in Also, so the kept
// one still says every route the story came by.
func merge(keep, drop model.Article) model.Article {
	for _, t := range drop.Tickers {
		keep.Tickers = appendUnique(keep.Tickers, t)
	}
	for _, g := range drop.GroupIDs {
		keep.GroupIDs = appendUnique(keep.GroupIDs, g)
	}
	for _, s := range drop.Carriers() {
		if s != keep.SourceID {
			keep.Also = appendUnique(keep.Also, s)
		}
	}
	if !drop.Published.IsZero() && drop.Published.Before(keep.Published) {
		keep.Published = drop.Published
	}
	return keep
}

func better(candidate, kept model.Article, weights map[string]int) bool {
	cw, kw := weights[candidate.SourceID], weights[kept.SourceID]
	if cw != kw {
		return cw > kw
	}
	return candidate.Published.Before(kept.Published)
}

// Match tags every article with the watchlist groups it belongs to and the
// tickers it names. Articles matching nothing are kept: they are the raw
// material for the general market overview.
func Match(articles []model.Article, groups []model.Group) []model.Article {
	out := make([]model.Article, len(articles))
	for i, a := range articles {
		text := a.Title + " " + a.Summary
		lower := strings.ToLower(text)

		a.Tickers = nil
		a.GroupIDs = nil
		for _, g := range groups {
			matched := false
			for _, ticker := range g.Tickers {
				if mentionsTicker(text, ticker) {
					a.Tickers = appendUnique(a.Tickers, strings.ToUpper(strings.TrimSpace(ticker)))
					matched = true
				}
			}
			if !matched {
				// Names and keywords match the same way; they are separate
				// fields so a watchlist reads as "these companies, plus these
				// themes" when someone edits prefs.yaml by hand.
				for _, term := range append(append([]string{}, g.Names...), g.Keywords...) {
					if mentionsWord(lower, strings.ToLower(strings.TrimSpace(term))) {
						matched = true
						break
					}
				}
			}
			if matched {
				a.GroupIDs = appendUnique(a.GroupIDs, g.ID)
			}
		}
		out[i] = a
	}
	return out
}

// minBareTicker is the shortest symbol trusted on its own. Single letters are
// real tickers -- C is Citigroup, F is Ford, V is Visa -- but as bare words they
// match C-suite, F-150 and V-shaped recovery, since a hyphen is a word boundary
// like any other. Those need an explicit marker to count.
//
// Two-letter symbols are left alone: GE and KO collide with far less, and
// demanding a marker would lose most genuine mentions of them.
const minBareTicker = 2

// mentionsTicker looks for a ticker as a standalone token, case-sensitively.
// Case matters: lowercasing would match "arm" and "it" in ordinary prose and
// tag half the feed into the wrong watchlist. This still trusts an all-caps
// headline, which is the residual false positive worth accepting.
func mentionsTicker(text, ticker string) bool {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return false
	}
	for i := 0; i+len(ticker) <= len(text); i++ {
		if text[i:i+len(ticker)] != ticker {
			continue
		}
		if i > 0 && isWordByte(text[i-1]) {
			continue // inside a longer token
		}
		if end := i + len(ticker); end < len(text) && isWordByte(text[end]) {
			continue
		}
		if len(ticker) >= minBareTicker || hasTickerMarker(text, i) {
			return true
		}
	}
	return false
}

// hasTickerMarker reports whether the symbol at index i is introduced as one:
// "$C", or the "(NYSE: C)" form quotes use. Anything else is a stray capital.
func hasTickerMarker(text string, i int) bool {
	j := i - 1
	for j >= 0 && text[j] == ' ' {
		j--
	}
	if j < 0 {
		return false
	}
	return text[j] == '$' || text[j] == ':'
}

// mentionsWord matches a keyword on word boundaries so "CPI" does not fire on
// "recipient". Both arguments must already be lowercased.
func mentionsWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		leftOK := start == 0 || !isWordByte(haystack[start-1])
		rightOK := end == len(haystack) || !isWordByte(haystack[end])
		if leftOK && rightOK {
			return true
		}
		i = start + 1
	}
}

// isWordByte reports whether b continues a token. Multi-byte UTF-8 sequences
// count as word bytes, which keeps a match from starting mid-character.
func isWordByte(b byte) bool {
	if b >= utf8Continuation {
		return true
	}
	r := rune(b)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// utf8Continuation is the first byte value outside 7-bit ASCII.
const utf8Continuation = 0x80

// Scoring weights. They are relative, not absolute: only their ratios matter.
//
// The triage rating counts most, because it is the only signal that reads what
// an article says: without it, an unmatched story ranked on its outlet's weight,
// which kept routine sanctions notices over a Gulf oil-supply shock. It still
// cannot outvote everything else together, so a named ticker from a primary
// source holds its place even when triage undersells it.
const (
	weightRating  = 6.0 // how much the content matters, judged by triage
	weightMatch   = 4.0 // the reader asked for this subject
	weightSource  = 3.0 // a filing outranks an aggregator's rewrite of it
	weightAgree   = 2.0 // several outlets carrying it means it mattered
	weightRecency = 2.0
	weightBody    = 0.5 // a headline with no text gives the summarizer nothing

	// recencyHalfLife is how long an article takes to lose half its freshness
	// score. A day of market news is the unit of interest, so a morning story
	// is still worth reading by the evening report.
	recencyHalfLife = 18.0

	// maxAgreement caps the corroboration bonus. Beyond a handful of outlets
	// the story is established and further copies say nothing new.
	maxAgreement = 3.0
)

// score rates an article's claim on a place in the brief. Every term is
// normalized to 0..1 first so the weights above are the only thing deciding
// how much each signal counts.
func score(a model.Article, weights map[string]int, now time.Time) float64 {
	match := 0.0
	switch {
	case len(a.Tickers) > 0:
		match = 1.0 // named a symbol outright
	case len(a.GroupIDs) > 0:
		match = 0.6 // matched a watchlist by name or theme
	}
	// Belonging to more than one watchlist is a stronger claim than one.
	if len(a.GroupIDs) > 1 {
		match = math.Min(1.0, match+0.2)
	}

	source := float64(weights[a.SourceID]) / 10.0
	agree := math.Min(float64(a.Corroborations), maxAgreement) / maxAgreement

	recency := 0.0
	if !a.Published.IsZero() {
		hours := now.Sub(a.Published).Hours()
		if hours < 0 {
			hours = 0
		}
		recency = math.Exp2(-hours / recencyHalfLife)
	}

	body := 0.0
	if a.Summary != "" {
		body = 1.0
	}

	// Unrated sits at the midpoint, level with a 3. With triage off every
	// article gets it, which leaves the ranking exactly as it was; after a
	// failed batch its articles are neither buried nor promoted.
	rating := 0.5
	if a.Rating >= 1 && a.Rating <= 5 {
		rating = float64(a.Rating-1) / 4
	}

	return rating*weightRating +
		match*weightMatch +
		source*weightSource +
		agree*weightAgree +
		recency*weightRecency +
		body*weightBody
}

// Limit keeps the n articles with the strongest claim on the reader's
// attention. Ordering by recency alone, as this once did, meant a wire
// aggregator's rewrite could displace the filing it was rewriting.
func Limit(articles []model.Article, sources []model.Source, n int, now time.Time) []model.Article {
	weights := make(map[string]int, len(sources))
	for _, s := range sources {
		weights[s.ID] = s.Weight
	}

	sorted := make([]model.Article, len(articles))
	copy(sorted, articles)

	scores := make(map[string]float64, len(sorted))
	for _, a := range sorted {
		scores[a.ID] = score(a, weights, now)
	}

	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if scores[a.ID] != scores[b.ID] {
			return scores[a.ID] > scores[b.ID]
		}
		return a.ID < b.ID // stable across runs when scores tie
	})

	if n > 0 && len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
