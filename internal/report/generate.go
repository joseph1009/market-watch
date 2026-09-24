package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// ErrNoArticles is returned when collection produced nothing. The caller sends
// a short "no news" notice instead of a brief; asking the model to summarize an
// empty day only invites it to invent one.
var ErrNoArticles = errors.New("report: no articles to summarize")

// Completion is one model response: the prose, which model wrote it, and how
// much text went each way.
type Completion struct {
	Text  string
	Usage model.Usage
	Model string
}

// Completer is the single model call a report needs. In the service it is the
// relay; the interface exists so prompt assembly and response parsing are
// testable without running a model at all.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (Completion, error)
}

// Generator turns collected articles into a written brief.
type Generator struct {
	Completer Completer

	// DisplayLocation renders timestamps in the reader's timezone; the model is
	// told the local date so "today" means the reader's today.
	DisplayLocation *time.Location

	// Levels are market readings shown to the model as measured values rather
	// than as news. Empty omits the block entirely.
	Levels []prices.Reading

	// Quotes are what shares did on the session the brief covers. They answer
	// the question the articles cannot: whether the market agreed with the news.
	Quotes []model.Quote

	// Trends are the price histories of the shares that moved furthest beyond
	// the market, keyed by symbol, so the brief can say what kind of move it
	// was: a break below the 50-day average, a new low for the year.
	Trends map[string]model.Trading

	// MovesSince is the previous brief. A price from before it belongs to a
	// session that brief already reported, and is not shown as today's move.
	MovesSince time.Time

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

	// Only watchlists with real news get a section. Filtering here rather than
	// after the response matters: asking for a section and then discarding it
	// would pay output tokens for prose nobody reads, and a watchlist with two
	// articles produces filler -- "the watchlist was thin today" -- rather than
	// anything worth the space.
	active, quiet := splitByCoverage(articles, groups, MinSectionArticles)
	m := market{levels: g.Levels, quotes: g.Quotes, trends: g.Trends, since: g.MovesSince}
	prompt := buildPrompt(articles, active, m, now, g.display())

	completion, err := g.Completer.Complete(ctx, systemPrompt, prompt)
	if err != nil {
		return model.Report{}, fmt.Errorf("summarize %d articles: %w", len(articles), err)
	}

	got := parseResponse(completion.Text)
	rep := model.Report{
		GeneratedAt:  now,
		Overview:     got.Overview,
		ArticleCount: len(articles),
		SourceCount:  countSources(articles),
		Usage:        completion.Usage,
	}

	for _, grp := range active {
		body := got.Sections[grp.ID]
		if body == "" {
			continue // the model skipped it despite being asked
		}
		rep.Sections = append(rep.Sections, model.Section{
			GroupID:   grp.ID,
			GroupName: grp.Name,
			Body:      body,
			// The same set the prompt showed, capped included: the rendered
			// sources are meant to be what the section was written from, and
			// listing articles the model never saw would misstate that.
			Articles: sectionArticles(articles, grp.ID),
			Movers:   sectionMoves(grp, g.Quotes, g.MovesSince),
		})
	}

	// Naming the quiet watchlists is worth a line: it tells the reader the
	// sector was checked and had nothing, rather than leaving them to wonder
	// whether it was dropped.
	rep.QuietGroups = quiet

	// The same set the prompt offered as general market news, so the rendered
	// sources can show everything the overview had to work with.
	rep.General = generalArticles(articles, active)

	// The same order newNumbering used, so citation [n] resolves to the article
	// the model was looking at.
	rep.Cited = articles

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
