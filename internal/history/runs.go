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

	NewNames int      `json:"new_names"`
	Failed   []string `json:"failed_sources,omitempty"`

	USD float64 `json:"usd"`
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
		kept, matched, cut, names, repeats, trivial int
		spend                                       float64
		failures                                    = map[string]int{}
	)
	for _, run := range r.runs {
		kept += run.Kept
		matched += run.Matched
		cut += run.CutImportant
		names += run.NewNames
		repeats += run.Repeats
		trivial += run.Trivial
		spend += run.USD
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
	fmt.Fprintf(&b, "%d removed as trivia, %d already covered\n", trivial/n, repeats/n)
	fmt.Fprintf(&b, "%d new names surfaced\n", names/n)
	fmt.Fprintf(&b, "~$%.2f spent, ~$%.2f a month at this rate\n\n", spend/float64(n), spend/float64(n)*30)

	if cut > 0 {
		fmt.Fprintf(&b, "<b>⚠ The cap cut %d article(s) rated 4 or 5</b>\nRaise MAX_ARTICLES if this keeps happening.\n\n", cut)
	} else {
		b.WriteString("<b>Nothing important lost to the cap</b>\nMAX_ARTICLES is high enough.\n\n")
	}

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
