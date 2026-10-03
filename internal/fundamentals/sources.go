package fundamentals

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// SourcesMarker heads the analysis's numbered list of the pages it drew on.
// The owner found addresses pasted into the text hard to read (2026-10-03),
// and then the outlet and date named in brackets (2026-10-04). So the text
// carries a number, [3], as the brief does, and the links are footnotes.
const SourcesMarker = "SOURCES"

// Source is one web page an analysis drew on.
type Source struct {
	Title string
	URL   string
}

var (
	// markdownLink is "[CNBC](https://...)".
	markdownLink = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)

	// bareURL is an address written into the text as it is.
	bareURL = regexp.MustCompile(`https?://[^\s)\]>]+`)

	// emptyBrackets and spaceBeforeStop tidy what an address leaves behind:
	// "the release (), revenue" is "the release, revenue".
	emptyBrackets   = regexp.MustCompile(`\s*\(\s*\)`)
	spaceBeforeStop = regexp.MustCompile(`\s+([.,;:])`)

	// listNumber is a source's number at the start of its line: "3. ",
	// "[3] ", "3) ".
	listNumber = regexp.MustCompile(`^\[?(\d{1,3})[\].):]?\s+`)

	// citeMarks is a citation in the text, "[3]" or "[3, 5]".
	citeMarks = regexp.MustCompile(`[ \t]*\[(\d{1,3}(?:\s*,\s*\d{1,3})*)\]`)
)

// SplitSources takes the links out of the analysis prose: the SOURCES
// section, and any address written into the text anyway. A markdown link
// keeps its words in the text; a bare address is dropped from it; a bullet
// that was nothing but a link goes, and a sub-heading left with nothing under
// it goes with it. The sources come back once each, in the order found.
//
// The footnotes are numbered from 1 in the order of the list, and each
// citation in the text, [3], is renumbered to match. A citation with no
// source behind it is dropped, rather than left pointing at nothing.
func SplitSources(prose string) (string, []Source) {
	var out []Source
	seen := map[string]int{}
	add := func(title, link string) int {
		link = strings.TrimRight(link, ".,;:")
		if n, ok := seen[link]; ok {
			return n
		}
		if title = strings.TrimSpace(title); title == "" {
			title = host(link)
		}
		out = append(out, Source{Title: title, URL: link})
		seen[link] = len(out)
		return len(out)
	}

	lines := strings.Split(prose, "\n")
	var kept []string
	// numbered is each footnote against the number the model gave it.
	numbered := map[int]int{}
	if start, end := sectionAt(lines, SourcesMarker); start >= 0 {
		for _, line := range lines[start+1 : end] {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-•*"))
			given := 0
			if m := listNumber.FindStringSubmatch(line); m != nil {
				given, _ = strconv.Atoi(m[1])
				line = line[len(m[0]):]
			}
			n := 0
			if m := markdownLink.FindStringSubmatch(line); m != nil {
				n = add(m[1], m[2])
			} else if title, link, ok := strings.Cut(line, "|"); ok && bareURL.MatchString(strings.TrimSpace(link)) {
				n = add(title, bareURL.FindString(link))
			} else if link := bareURL.FindString(line); link != "" {
				n = add(strings.Trim(strings.Replace(line, link, "", 1), " :-–—"), link)
			}
			if given > 0 && n > 0 {
				numbered[given] = n
			}
		}
		lines = append(append([]string{}, lines[:start]...), lines[end:]...)
	}
	listed := len(out)

	for _, line := range lines {
		if !strings.Contains(line, "http") {
			kept = append(kept, line)
			continue
		}
		body := strings.TrimSpace(line)
		bullet := strings.HasPrefix(body, "- ")
		body = strings.TrimSpace(strings.TrimPrefix(body, "- "))
		if m := markdownLink.FindStringSubmatch(body); m != nil && m[0] == body {
			add(m[1], m[2])
			continue // a bullet that was only a link
		}
		if link := bareURL.FindString(body); link == body {
			add("", link)
			continue
		}
		line = markdownLink.ReplaceAllStringFunc(line, func(s string) string {
			m := markdownLink.FindStringSubmatch(s)
			add(m[1], m[2])
			return m[1]
		})
		line = bareURL.ReplaceAllStringFunc(line, func(s string) string {
			add("", s)
			return ""
		})
		line = emptyBrackets.ReplaceAllString(line, "")
		line = spaceBeforeStop.ReplaceAllString(line, "$1")
		if bullet && strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")) == "" {
			continue
		}
		kept = append(kept, strings.TrimRight(line, " "))
	}

	text := strings.Join(withoutEmptySubheadings(kept), "\n")
	text = citeMarks.ReplaceAllStringFunc(text, func(s string) string {
		marks := ""
		done := map[int]bool{}
		for _, part := range strings.Split(citeMarks.FindStringSubmatch(s)[1], ",") {
			given, _ := strconv.Atoi(strings.TrimSpace(part))
			n := numbered[given]
			// A list without numbers is taken to be numbered in its order.
			if len(numbered) == 0 && given >= 1 && given <= listed {
				n = given
			}
			if n > 0 && !done[n] {
				done[n] = true
				marks += "[" + strconv.Itoa(n) + "]"
			}
		}
		if marks == "" {
			return ""
		}
		return " " + marks
	})
	return strings.TrimSpace(text), out
}

// withoutEmptySubheadings drops a "### " line with no bullet or text before
// the next sub-heading or section.
func withoutEmptySubheadings(lines []string) []string {
	var out []string
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "### ") {
			empty := true
			for _, next := range lines[i+1:] {
				t := strings.TrimSpace(next)
				if t == "" {
					continue
				}
				empty = strings.HasPrefix(t, "### ") || heading(t)
				break
			}
			if empty {
				// The blank line before it goes too.
				if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "" {
					out = out[:n-1]
				}
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

// host is a link's site, "cnbc.com", for a source given without a title.
func host(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return link
	}
	return strings.TrimPrefix(u.Host, "www.")
}
