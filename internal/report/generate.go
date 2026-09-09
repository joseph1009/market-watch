package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

// ErrNoArticles is returned when collection produced nothing. The caller sends
// a short "no news" notice instead of a brief; asking the model to summarize an
// empty day only invites it to invent one.
var ErrNoArticles = errors.New("report: no articles to summarize")

// Completer is the single model call a report needs. The interface exists so
// prompt assembly and response parsing are testable without the network, and so
// the Anthropic client stays replaceable.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, error)
}

// Generator turns collected articles into a written brief.
type Generator struct {
	Completer Completer

	// DisplayLocation renders timestamps in the reader's timezone; the model is
	// told the local date so "today" means the reader's today.
	DisplayLocation *time.Location

	// Now is injected for tests.
	Now func() time.Time
}

// Generate writes the brief. Sections come back in the order the watchlists
// were given, regardless of the order the model emitted them.
func (g *Generator) Generate(ctx context.Context, articles []model.Article, groups []model.Group) (model.Report, error) {
	if len(articles) == 0 {
		return model.Report{}, ErrNoArticles
	}

	now := g.now()
	prompt := buildPrompt(articles, groups, now, g.display())

	raw, err := g.Completer.Complete(ctx, systemPrompt, prompt)
	if err != nil {
		return model.Report{}, fmt.Errorf("summarize %d articles: %w", len(articles), err)
	}

	got := parseResponse(raw)
	rep := model.Report{
		GeneratedAt:  now,
		Overview:     got.Overview,
		ArticleCount: len(articles),
		SourceCount:  countSources(articles),
	}

	for _, grp := range groups {
		body := got.Sections[grp.ID]
		matched := articlesInGroup(articles, grp.ID)
		// A section with neither prose nor articles is silence about a quiet
		// watchlist; carrying it into the report would render an empty heading.
		if body == "" && len(matched) == 0 {
			continue
		}
		rep.Sections = append(rep.Sections, model.Section{
			GroupID:   grp.ID,
			GroupName: grp.Name,
			Body:      body,
			Articles:  matched,
		})
	}

	if rep.IsEmpty() {
		return model.Report{}, fmt.Errorf("report: model returned no usable prose for %v", sortedGroupIDs(groups))
	}
	return rep, nil
}

func (g *Generator) display() *time.Location {
	if g.DisplayLocation != nil {
		return g.DisplayLocation
	}
	return time.UTC
}

func (g *Generator) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now().UTC()
}
