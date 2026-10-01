package model

import (
	"regexp"
	"strconv"
	"time"
)

// Report is one generated market brief, ready to be rendered and sent.
type Report struct {
	GeneratedAt  time.Time `json:"generated_at"`
	Overview     string    `json:"overview"`
	Sections     []Section `json:"sections"`
	ArticleCount int       `json:"article_count"`
	SourceCount  int       `json:"source_count"`
	Usage        Usage     `json:"usage"`

	// Triage is what rating and placing the articles took, kept apart from
	// Usage because it is a different, smaller model.
	Triage Usage `json:"triage,omitempty"`

	// QuietGroups names the watchlists that had too little news to be worth a
	// section. Reporting them distinguishes "nothing happened" from "this was
	// not looked at", which matters more as the number of watchlists grows.
	QuietGroups []string `json:"quiet_groups,omitempty"`

	// Cited are the articles as numbered for the model, so a "[12]" in the prose
	// can be turned back into a link to article twelve. The order is the order
	// the numbering used and must not be rearranged.
	Cited []Article `json:"-"`

	// Candidates are companies the news kept mentioning that no watchlist
	// tracks. Named rather than recommended: catalyst and evidence, no advice.
	Candidates []Candidate `json:"candidates,omitempty"`

	// General holds the articles the summarizer was given that no section
	// claimed. The overview is written partly from these, so without them a
	// claim in the overview cannot be traced back to anything.
	General []Article `json:"general,omitempty"`

	// Calendar is what is due today and in the rest of the week, shown
	// after the overview so the reader knows what is coming, not only what
	// happened.
	Calendar Calendar `json:"calendar,omitempty"`
}

// Usage is how much text one run sent and got back.
//
// Tokens, not dollars. The calls are answered through a Claude subscription,
// which is not billed per token, so a price would be a figure nobody pays; the
// token count is still what says how large a run was and how much of the
// plan's allowance it drew on.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens,omitempty"`
}

// Total is the token count a reader thinks of as "how big was this run".
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// Section is the per-watchlist part of a report. Articles are the ones the
// summarizer was given, kept so the rendered report can link its sources.
type Section struct {
	GroupID   string    `json:"group_id"`
	GroupName string    `json:"group_name"`
	Emoji     string    `json:"emoji,omitempty"`
	Body      string    `json:"body"`
	Articles  []Article `json:"articles"`

	// Movers are the watchlist's biggest share moves on the day, largest
	// first, shown in a line under the section's heading. The program picks
	// them from the prices, not the model from the prose, so the figures are
	// the exchange's.
	Movers []Quote `json:"movers,omitempty"`
}

// IsEmpty reports whether the report has no prose worth sending.
func (r Report) IsEmpty() bool {
	if r.Overview != "" {
		return false
	}
	for _, s := range r.Sections {
		if s.Body != "" {
			return false
		}
	}
	return true
}

// citation matches the "[12]" the brief cites an article with.
var citation = regexp.MustCompile(`\[(\d{1,4})\]`)

// Referenced returns the articles the prose actually cites, each once, in the
// order they were numbered.
//
// Cited is everything the model was offered, most of which it leaves alone.
// What it chose to cite is the measure of which sources earn their place: a
// source whose stories are fetched every day and never cited is costing
// tokens and giving nothing.
func (r Report) Referenced() []Article {
	used := make(map[int]bool)
	scan := func(text string) {
		for _, m := range citation.FindAllStringSubmatch(text, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= len(r.Cited) {
				used[n] = true
			}
		}
	}
	scan(r.Overview)
	for _, s := range r.Sections {
		scan(s.Body)
	}

	var out []Article
	for i, a := range r.Cited {
		if used[i+1] {
			out = append(out, a)
		}
	}
	return out
}
