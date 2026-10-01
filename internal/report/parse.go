package report

import (
	"strings"
)

// parsed is the raw shape of a model response, before it is matched against
// the watchlists that were actually requested.
type parsed struct {
	Summary  string // the few bullets shown before the rest is opened
	Overview string
	Sections map[string]string
	Order    []string
}

// parseResponse splits the delimited response. It is deliberately forgiving:
// text before the first marker is treated as the overview, because a model
// that skips the opening marker has still written a usable brief, and losing
// the whole report over a missing "## OVERVIEW" line would be worse than
// guessing correctly here.
func parseResponse(raw string) parsed {
	out := parsed{Sections: make(map[string]string)}

	var (
		current string // "" means the overview
		body    []string
		summary bool // in the IN SHORT block
	)
	flush := func() {
		text := strings.TrimSpace(strings.Join(body, "\n"))
		body = body[:0]
		if text == "" {
			return
		}
		if summary {
			out.Summary = strings.TrimSpace(out.Summary + "\n" + text)
			return
		}
		if current == "" {
			// A second run of leading text appends rather than replaces, so
			// nothing written before the first section marker is dropped.
			out.Overview = strings.TrimSpace(out.Overview + "\n\n" + text)
			return
		}
		if _, seen := out.Sections[current]; !seen {
			out.Order = append(out.Order, current)
		}
		out.Sections[current] = strings.TrimSpace(out.Sections[current] + "\n\n" + text)
	}

	for _, line := range strings.Split(raw, "\n") {
		switch trimmed := strings.TrimSpace(line); {
		case strings.EqualFold(trimmed, summaryMarker):
			flush()
			summary = true
		case strings.EqualFold(trimmed, overviewMarker):
			flush()
			current, summary = "", false
		case hasMarkerPrefix(trimmed, sectionMarker):
			flush()
			summary = false
			current = strings.ToLower(strings.TrimSpace(trimmed[len(sectionMarker):]))
		default:
			body = append(body, line)
		}
	}
	flush()

	return out
}

func hasMarkerPrefix(line, marker string) bool {
	return len(line) >= len(marker) && strings.EqualFold(line[:len(marker)], marker)
}
