package model

import "time"

// Report is one generated market brief, ready to be rendered and sent.
type Report struct {
	GeneratedAt  time.Time `json:"generated_at"`
	Overview     string    `json:"overview"`
	Sections     []Section `json:"sections"`
	ArticleCount int       `json:"article_count"`
	SourceCount  int       `json:"source_count"`
	Usage        Usage     `json:"usage"`

	// Triage is what rating and placing the articles cost, kept apart from
	// Usage because it is a different model at a different price.
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
}

// Usage is what one brief cost to generate.
//
// It is per-run only. The Messages API reports the tokens a request spent and
// has no notion of an account balance, so nothing here can say how much credit
// remains -- that lives in the Anthropic Console. What this does give is the
// burn rate: multiply by thirty for a month of daily briefs.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens,omitempty"`

	// EstimatedUSD is priced from a table compiled into the binary, not quoted
	// by the API, so it is an estimate and drifts if rates change.
	EstimatedUSD float64 `json:"estimated_usd"`
}

// Total is the token count a reader thinks of as "how big was this run".
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// Section is the per-watchlist part of a report. Articles are the ones the
// summarizer was given, kept so the rendered report can link its sources.
type Section struct {
	GroupID   string    `json:"group_id"`
	GroupName string    `json:"group_name"`
	Body      string    `json:"body"`
	Articles  []Article `json:"articles"`
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
