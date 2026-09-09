package model

import "time"

// Report is one generated market brief, ready to be rendered and sent.
type Report struct {
	GeneratedAt  time.Time `json:"generated_at"`
	Overview     string    `json:"overview"`
	Sections     []Section `json:"sections"`
	ArticleCount int       `json:"article_count"`
	SourceCount  int       `json:"source_count"`
}

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
