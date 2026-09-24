package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/telegram"
)

// RunsKept is how many runs the record holds. Enough to see a fortnight of
// weekday briefs, which is the span over which a trend in these numbers means
// anything.
const RunsKept = 30

// Run is what one brief cost and what the machinery did with the day's news.
//
// These numbers only ever reached a terminal log, which meant the advice "watch
// whether the cap keeps cutting important articles" could not actually be
// followed: by the next morning the line was gone. Written down, a fortnight of
// them answers it at a glance.
type Run struct {
	At time.Time `json:"at"`

	Fetched  int `json:"fetched"`
	Deduped  int `json:"deduped"`
	Kept     int `json:"kept"`
	Matched  int `json:"matched"`
	Dropped  int `json:"dropped"`
	Trivial  int `json:"trivial"`
	Repeats  int `json:"repeats"`
	Sections int `json:"sections"`
	Messages int `json:"messages"`

	// CutImportant is the number to watch: articles triage rated 4 or 5 that
	// the cap discarded anyway. Persistently above zero means the cap is too
	// low.
	CutImportant int `json:"cut_important"`

	// Placed is how many articles a watchlist claimed on substance alone,
	// having matched none of its tickers or keywords, and PlacedExamples a few
	// of them written as "headline -> sector".
	//
	// These are the numbers that say whether describing a watchlist as a
	// sector was a good idea. The count alone cannot: sixty placements is
	// either sixty stories keywords would have missed or sixty stretches, and
	// only reading a few tells you which.
	Placed         int      `json:"placed,omitempty"`
	PlacedExamples []string `json:"placed_examples,omitempty"`

	NewNames int      `json:"new_names"`
	Failed   []string `json:"failed_sources,omitempty"`

	// What news search contributed, all zero when it is off. Searched is how
	// many articles the searches returned and SearchCredits what they cost.
	// SearchOnly is how many kept stories no feed carried, and Cited how many
	// stories the brief cited, of which CitedSearchOnly came from search alone.
	// MissedBySearch counts the cited stories no search found, by the source
	// that carried them.
	//
	// Together these answer whether search can replace the media feeds. If
	// what search misses is only ever government releases and company
	// newsrooms, which it was never meant to find, the media feeds can go; if
	// CNBC or the FT keep appearing in it, they cannot yet.
	Searched        int            `json:"searched,omitempty"`
	SearchCredits   int            `json:"search_credits,omitempty"`
	SearchOnly      int            `json:"search_only,omitempty"`
	Cited           int            `json:"cited,omitempty"`
	CitedSearchOnly int            `json:"cited_search_only,omitempty"`
	MissedBySearch  map[string]int `json:"missed_by_search,omitempty"`
}

// Runs is the record of recent briefs.
type Runs struct {
	Path string

	runs []Run
}

// LoadRuns reads the record; a missing file is an empty one.
func LoadRuns(path string) (*Runs, error) {
	r := &Runs{Path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &r.runs); err != nil {
		r.runs = nil // a corrupt history costs the statistics, never the brief
	}
	return r, nil
}

// Add records a run and drops the oldest beyond RunsKept.
func (r *Runs) Add(run Run) error {
	r.runs = append(r.runs, run)
	sort.Slice(r.runs, func(i, j int) bool { return r.runs[i].At.After(r.runs[j].At) })
	if len(r.runs) > RunsKept {
		r.runs = r.runs[:RunsKept]
	}
	return r.save()
}

// All returns the runs, newest first.
func (r *Runs) All() []Run { return r.runs }

// Summary reads the record back as the answer to "is this working".
//
// It reports the averages a reader would otherwise have to compute, and names
// the two things worth acting on: a cap that keeps cutting important articles,
// and a source that has stopped returning anything.
func (r *Runs) Summary(display *time.Location) string {
	if len(r.runs) == 0 {
		return "No briefs recorded yet. The statistics start with the next one."
	}

	var (
		kept, matched, cut, names, repeats, trivial, placed int
		failures                                            = map[string]int{}
	)
	for _, run := range r.runs {
		kept += run.Kept
		matched += run.Matched
		cut += run.CutImportant
		names += run.NewNames
		repeats += run.Repeats
		trivial += run.Trivial
		placed += run.Placed
		for _, f := range run.Failed {
			failures[f]++
		}
	}
	n := len(r.runs)

	var b strings.Builder
	fmt.Fprintf(&b, "<b>Last %d brief(s)</b>\n", n)
	fmt.Fprintf(&b, "Since %s\n\n", r.runs[n-1].At.In(display).Format("Mon 2 Jan"))

	fmt.Fprintf(&b, "<b>Per brief, on average</b>\n")
	fmt.Fprintf(&b, "%d articles kept, %d matched to a watchlist\n", kept/n, matched/n)
	fmt.Fprintf(&b, "of those, %d placed by judgment rather than by name\n", placed/n)
	fmt.Fprintf(&b, "%d removed as trivia, %d already covered\n", trivial/n, repeats/n)
	fmt.Fprintf(&b, "%d new names surfaced\n\n", names/n)

	// From the latest run rather than pooled across the fortnight: these are
	// meant to be read as examples of what arrived, and a fortnight of them
	// would be a wall of headlines nobody reads.
	if ex := r.runs[0].PlacedExamples; len(ex) > 0 {
		// Headlines are feed text, so they are escaped here rather than where
		// they were recorded: what is stored stays readable, and the escaping
		// belongs to the format it is being written into.
		lines := make([]string, len(ex))
		for i, e := range ex {
			lines[i] = telegram.Escape(e)
		}
		fmt.Fprintf(&b, "<b>Placed by judgment, most recently</b>\n%s\n\n", strings.Join(lines, "\n"))
	}

	if cut > 0 {
		fmt.Fprintf(&b, "<b>⚠ The cap cut %d article(s) rated 4 or 5</b>\nRaise MAX_ARTICLES if this keeps happening.\n\n", cut)
	} else {
		b.WriteString("<b>Nothing important lost to the cap</b>\nMAX_ARTICLES is high enough.\n\n")
	}

	b.WriteString(r.searchSummary())

	// A source that fails every run has moved or died, and is worth removing;
	// one that fails occasionally is just an outage.
	var persistent []string
	for source, times := range failures {
		if times >= n/2 && times > 1 {
			persistent = append(persistent, fmt.Sprintf("%s (%d of %d)", source, times, n))
		}
	}
	sort.Strings(persistent)
	if len(persistent) > 0 {
		fmt.Fprintf(&b, "<b>Sources failing regularly</b>\n%s\nTurn them off with /sources off &lt;id&gt;.",
			strings.Join(persistent, "\n"))
	}
	return strings.TrimSpace(b.String())
}

// missedShown is how many sources the search block names. The long tail is one
// story each and says nothing about which feeds matter.
const missedShown = 8

// searchSummary is the side-by-side of search and feeds, over the briefs that
// searched. Empty until one has.
func (r *Runs) searchSummary() string {
	var (
		n, found, credits, only, cited, citedOnly int
		missed                                    = map[string]int{}
	)
	for _, run := range r.runs {
		if run.Searched == 0 {
			continue
		}
		n++
		found += run.Searched
		credits += run.SearchCredits
		only += run.SearchOnly
		cited += run.Cited
		citedOnly += run.CitedSearchOnly
		for source, times := range run.MissedBySearch {
			missed[source] += times
		}
	}
	if n == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<b>News search, over %d brief(s)</b>\n", n)
	fmt.Fprintf(&b, "%d articles found per brief, for %d credits\n", found/n, credits/n)
	fmt.Fprintf(&b, "%d kept stories per brief that no feed carried\n", only/n)
	fmt.Fprintf(&b, "%d of %d cited stories came from search alone\n", citedOnly, cited)

	// Most-missed first: the top of this list is what the feeds give that
	// search does not, and so what would be lost by turning them off.
	sources := make([]string, 0, len(missed))
	for s := range missed {
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool {
		if missed[sources[i]] != missed[sources[j]] {
			return missed[sources[i]] > missed[sources[j]]
		}
		return sources[i] < sources[j]
	})
	if len(sources) > 0 {
		if len(sources) > missedShown {
			sources = sources[:missedShown]
		}
		lines := make([]string, len(sources))
		for i, s := range sources {
			lines[i] = fmt.Sprintf("%s %d", s, missed[s])
		}
		fmt.Fprintf(&b, "Cited stories search did not find, by source:\n%s\n", strings.Join(lines, ", "))
	}
	b.WriteString("\n")
	return b.String()
}

func (r *Runs) save() error {
	data, err := json.MarshalIndent(r.runs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}

	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path)
}
