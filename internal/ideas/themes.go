package ideas

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/config"
	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/market"
	"github.com/joseph1009/market-watch/internal/model"
)

// The kinds of theme: one the market has been paying for, and one whose
// business is growing before its shares have followed.
const (
	ThemePopular = "popular"
	ThemeEarly   = "early"
)

// Theme is a reason the market is paying for a group of companies, or is
// starting to.
type Theme struct {
	Kind   string
	Name   string
	Driver string

	// Evidence is an early theme's growth, in figures with their sources,
	// as the scout found them on the web.
	Evidence string

	// Members are the companies in it: US symbols as the market writes them,
	// or "CODE:EX" for one the scout named elsewhere.
	Members []string

	// Figures is its members' numbers, written by the caller for the
	// research and the message.
	Figures string
}

// Sorter groups the market's leaders into the themes driving them.
type Sorter struct {
	Completer Completer
}

// SortInput is what the sorting reads.
type SortInput struct {
	Leaders    []market.Stock
	Bench      market.Stock
	Industries []market.Industry
	Headlines  []string
	LastWeek   []string
}

// Sort returns up to PopularThemes themes, strongest first, each with at
// least three of the leaders as members.
func (s *Sorter) Sort(ctx context.Context, in SortInput) ([]Theme, model.Usage, error) {
	system, err := config.RenderPrompt("themes.system", struct{ Max int }{PopularThemes})
	if err != nil {
		return nil, model.Usage{}, err
	}
	text, usage, err := s.Completer.Complete(ctx, system, sortPrompt(in))
	if err != nil {
		return nil, usage, err
	}
	known := map[string]bool{}
	for _, l := range in.Leaders {
		known[l.Symbol] = true
	}
	themes := parseThemes(text, ThemePopular)
	var out []Theme
	used := map[string]bool{}
	for _, t := range themes {
		var members []string
		for _, m := range t.Members {
			m = strings.ToUpper(strings.TrimSpace(m))
			if known[m] && !used[m] {
				used[m] = true
				members = append(members, m)
			}
		}
		if len(members) < 3 || t.Name == "" {
			continue
		}
		t.Members = members
		out = append(out, t)
		if len(out) == PopularThemes {
			break
		}
	}
	return out, usage, nil
}

func sortPrompt(in SortInput) string {
	var b strings.Builder
	b.WriteString("The market's leaders over the last two years, best first. Returns are against the S&P 500, in percentage points; the swing is how far the share moves in a year, and steadiness the year's return for each unit of it.\n")
	b.WriteString("symbol | name | industry (sector) | 2 years | year less its last month | 6 months | 3 months | swing | steadiness\n")
	for _, s := range in.Leaders {
		industry := s.Industry
		if s.Sector != "" {
			industry += " (" + s.Sector + ")"
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s | %s | %.0f%% | %.2f\n",
			s.Symbol, market.PlainName(s.Name), industry,
			pts(s.R24-in.Bench.R24), pts(s.R12x1-in.Bench.R12x1), pts(s.R6-in.Bench.R6), pts(s.R3-in.Bench.R3),
			100*s.Volatility, s.Steady())
	}

	if len(in.Industries) > 0 {
		b.WriteString("\nThe industries that have risen furthest as a whole:\n")
		for _, d := range in.Industries {
			b.WriteString(IndustryLine(d) + "\n")
		}
	}
	writeHeadlines(&b, in.Headlines)
	if len(in.LastWeek) > 0 {
		b.WriteString("\nLast week's themes: " + strings.Join(in.LastWeek, "; ") + "\n")
	}
	return b.String()
}

// IndustryLine is one industry's figures, as the prompts and the message
// write them.
func IndustryLine(d market.Industry) string {
	return fmt.Sprintf("%s (%s, %d companies): median member %s over 12 months and %s over 3 months against the S&P 500; %.0f%% above their 200-day average; %.0f%% above their 50-day average, from %.0f%% a month ago; trading %s on a year ago; named in %d recent headlines; largest movers %s",
		d.Name, d.Sector, d.Members, pts(d.Median12), pts(d.Median3),
		100*d.Breadth, 100*d.Breadth50, 100*d.Breadth50Before, ratio(d.MoneyYear), d.Mentions,
		strings.Join(d.Top[:min(5, len(d.Top))], ", "))
}

// pts writes a difference of fractions as percentage points: "+12 pts".
func pts(f float64) string {
	if math.IsNaN(f) {
		return "n/a"
	}
	return fmt.Sprintf("%+.0f pts", 100*f)
}

// ratio writes a ratio as a change: 1.3 is "+30%".
func ratio(f float64) string {
	if f <= 0 {
		return "n/a"
	}
	return pct(f - 1)
}

func writeHeadlines(b *strings.Builder, headlines []string) {
	if len(headlines) == 0 {
		return
	}
	b.WriteString("\nRecent headlines from the daily briefs, newest first:\n")
	for _, h := range headlines {
		b.WriteString("- " + oneLine(h) + "\n")
	}
}

// Scout looks for industries whose business is growing before their shares
// have followed.
type Scout struct {
	Completer Completer
}

// ScoutInput is what the scout reads.
type ScoutInput struct {
	Early     []market.Industry
	Names     map[string]string // symbol to name, for the industries' members
	Popular   []Theme
	Headlines []string
	LastWeek  []string
	Today     time.Time
}

// Find returns up to EarlyThemes early themes.
func (s *Scout) Find(ctx context.Context, in ScoutInput) ([]Theme, model.Usage, error) {
	system, err := config.RenderPrompt("scout.system", struct{ Max int }{EarlyThemes})
	if err != nil {
		return nil, model.Usage{}, err
	}
	text, usage, err := s.Completer.Complete(ctx, system, scoutPrompt(in))
	if err != nil {
		return nil, usage, err
	}
	var out []Theme
	for _, t := range parseThemes(text, ThemeEarly) {
		if t.Name == "" || t.Driver == "" {
			continue
		}
		out = append(out, t)
		if len(out) == EarlyThemes {
			break
		}
	}
	return out, usage, nil
}

func scoutPrompt(in ScoutInput) string {
	var b strings.Builder
	if !in.Today.IsZero() {
		fmt.Fprintf(&b, "Today is %s.\n\n", in.Today.Format("Monday 2 January 2006"))
	}
	b.WriteString("Industries whose shares are starting to turn while their year still lags the typical industry's, strongest turn first:\n")
	if len(in.Early) == 0 {
		b.WriteString("(none this week: the numbers show no industry turning)\n")
	}
	for _, d := range in.Early {
		b.WriteString("- " + IndustryLine(d))
		var named []string
		for _, sym := range d.Top[:min(8, len(d.Top))] {
			if n := in.Names[sym]; n != "" {
				named = append(named, fmt.Sprintf("%s (%s)", market.PlainName(n), sym))
			}
		}
		if len(named) > 0 {
			b.WriteString("; its members include " + strings.Join(named, ", "))
		}
		b.WriteString("\n")
	}
	if len(in.Popular) > 0 {
		b.WriteString("\nWhat the market is already paying for this week, which you are not looking for:\n")
		for _, t := range in.Popular {
			fmt.Fprintf(&b, "- %s: %s\n", t.Name, t.Driver)
		}
	}
	writeHeadlines(&b, in.Headlines)
	if len(in.LastWeek) > 0 {
		b.WriteString("\nLast week's finds: " + strings.Join(in.LastWeek, "; ") + "\n")
	}
	return b.String()
}

// parseThemes reads THEME / DRIVER / EVIDENCE / MEMBERS blocks. A field may
// run onto the next line.
func parseThemes(text, kind string) []Theme {
	var out []Theme
	var cur *Theme
	var last *string
	var members string
	flush := func() {
		if cur != nil {
			for _, m := range strings.Split(members, ",") {
				if m = strings.TrimSpace(m); m != "" {
					cur.Members = append(cur.Members, m)
				}
			}
			out = append(out, *cur)
		}
		cur, last, members = nil, nil, ""
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if v, ok := field(line, "THEME:"); ok {
			flush()
			cur = &Theme{Kind: kind, Name: strings.Trim(v, " .")}
			last = &cur.Name
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case hasField(line, "DRIVER:"):
			cur.Driver, _ = field(line, "DRIVER:")
			last = &cur.Driver
		case hasField(line, "EVIDENCE:"):
			cur.Evidence, _ = field(line, "EVIDENCE:")
			last = &cur.Evidence
		case hasField(line, "MEMBERS:"):
			members, _ = field(line, "MEMBERS:")
			last = &members
		default:
			if last != nil {
				*last += " " + line
			}
		}
	}
	flush()
	return out
}

func hasField(line, label string) bool {
	_, ok := field(line, label)
	return ok
}

// Researcher researches one theme and proposes the companies in the part of
// it the market has not paid for.
type Researcher struct {
	Completer Completer
	Verifier  discover.Verifier
}

// ResearchInput is what the research of one theme reads.
type ResearchInput struct {
	Theme Theme

	// Previous is last week's research on the same theme, where it was one,
	// and Recent the companies picked in the last RepeatWindow, as lines:
	// "Nvidia (NVDA): BUY on 6 Oct, under AI data centres".
	Previous string
	Recent   []string

	// Tracked is every company the investor follows, which the research must
	// not choose, and Followed the same as US symbols, which are dropped if
	// it does.
	Tracked  []string
	Followed map[string]bool

	Today time.Time
}

// Research is what the research found.
type Research struct {
	Theme                    Theme
	Driving, PricedIn, Value string

	// Text is the whole reply, kept to show next week's research what this
	// one said.
	Text string

	// Ideas are the companies proposed, best first, every ticker verified.
	Ideas []model.Idea
}

// Research researches a theme.
func (r *Researcher) Research(ctx context.Context, in ResearchInput) (Research, model.Usage, error) {
	system, err := config.RenderPrompt("research.system", struct{ Max int }{PerTheme})
	if err != nil {
		return Research{}, model.Usage{}, err
	}
	text, usage, err := r.Completer.Complete(ctx, system, researchPrompt(in))
	if err != nil {
		return Research{}, usage, err
	}
	out := parseResearch(text, in.Theme)
	var kept []model.Idea
	for _, idea := range out.Ideas {
		if !PickExchanges[idea.Exchange] || (idea.Exchange == "US" && in.Followed[idea.Ticker]) || tracked(idea, in.Tracked) {
			continue
		}
		kept = append(kept, idea)
	}
	kept, err = verify(ctx, r.Verifier, kept)
	if err != nil {
		return out, usage, fmt.Errorf("verifying tickers: %w", err)
	}
	if len(kept) > PerTheme {
		kept = kept[:PerTheme]
	}
	out.Ideas = kept
	return out, usage, nil
}

// tracked reports whether an idea is a company the investor follows by name.
func tracked(idea model.Idea, names []string) bool {
	for _, n := range names {
		if strings.EqualFold(n, idea.Name) || strings.EqualFold(n, idea.Ticker) {
			return true
		}
	}
	return false
}

func researchPrompt(in ResearchInput) string {
	var b strings.Builder
	if !in.Today.IsZero() {
		fmt.Fprintf(&b, "Today is %s.\n\n", in.Today.Format("Monday 2 January 2006"))
	}
	t := in.Theme
	kind := "A theme the market has been paying for"
	if t.Kind == ThemeEarly {
		kind = "An industry whose business is growing before its shares have followed"
	}
	fmt.Fprintf(&b, "%s: %s.\nWhat drives it: %s\n", kind, t.Name, t.Driver)
	if t.Evidence != "" {
		fmt.Fprintf(&b, "The growth, as the scout found it on the web and unchecked: %s\n", t.Evidence)
	}
	if t.Figures != "" {
		b.WriteString("\nIts figures, from share prices:\n" + strings.TrimSpace(t.Figures) + "\n")
	}
	if in.Previous != "" {
		b.WriteString("\nLast week's research on this theme:\n" + strings.TrimSpace(in.Previous) + "\n")
	}
	if len(in.Recent) > 0 {
		b.WriteString("\nPicked in the last eight weeks, not to be proposed again unless something material has changed:\n")
		for _, r := range in.Recent {
			b.WriteString("- " + r + "\n")
		}
	}
	if len(in.Tracked) > 0 {
		b.WriteString("\nCompanies the investor already follows, which must not be chosen: " + strings.Join(in.Tracked, ", ") + "\n")
	}
	return b.String()
}

// parseResearch reads the research's reply.
func parseResearch(text string, theme Theme) Research {
	out := Research{Theme: theme, Text: strings.TrimSpace(text)}
	var last *string
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			last = nil
			continue
		}
		switch {
		case hasField(line, "DRIVING:"):
			out.Driving, _ = field(line, "DRIVING:")
			last = &out.Driving
			continue
		case hasField(line, "PRICED IN:"):
			out.PricedIn, _ = field(line, "PRICED IN:")
			last = &out.PricedIn
			continue
		case hasField(line, "THE VALUE:"):
			out.Value, _ = field(line, "THE VALUE:")
			last = &out.Value
			continue
		}
		parts := strings.Split(strings.TrimLeft(line, "-• "), "|")
		if len(parts) >= 5 {
			last = nil
			idea := model.Idea{
				Kind:     model.IdeaTheme,
				Theme:    theme.Name,
				Name:     strings.TrimSpace(parts[0]),
				Ticker:   strings.ToUpper(strings.TrimSpace(parts[1])),
				Exchange: strings.ToUpper(strings.TrimSpace(parts[2])),
				Lean:     strings.ToLower(strings.TrimSpace(parts[3])),
				Link:     strings.TrimSpace(strings.Join(parts[4:], "|")),
			}
			if idea.Lean != "buy" && idea.Lean != "sell" {
				continue
			}
			key := idea.Ticker + "." + idea.Exchange
			if idea.Name == "" || idea.Ticker == "" || idea.Link == "" || seen[key] {
				continue
			}
			seen[key] = true
			out.Ideas = append(out.Ideas, idea)
			continue
		}
		if last != nil {
			*last += " " + line
		}
	}
	return out
}
