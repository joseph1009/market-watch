package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/joseph1009/market-watch/internal/model"
)

// Fold writes the edits and switches made from Telegram into the files in dir
// -- the repository's config directory -- so they become part of the lists
// rather than something kept beside them. It returns what it changed, one line
// each. scripts/sync-from-fly.sh runs it after copying the service's state down.
//
// The files are edited line by line, not re-encoded: an addition is one new
// line, a removal takes out its line and the comment above it, a switch
// changes one word. Re-encoding keeps the comments but not the blank lines
// between groups, and a sync that reformatted the whole file would bury the
// one line that changed in a diff of fifty.
//
// Once the files are committed and deployed, the edits on the volume match
// what the files say and are dropped at the next start (pruneEdits).
func Fold(dir string, p Prefs) ([]string, error) {
	var done []string
	if !p.Edits.IsZero() {
		changes, err := foldFile(filepath.Join(dir, CompaniesFile), func(raw []byte) ([]byte, []string, error) {
			return foldCompanies(raw, p.Edits)
		})
		if err != nil {
			return done, err
		}
		done = append(done, changes...)
	}
	if len(p.FeedSwitches) > 0 {
		changes, err := foldFile(filepath.Join(dir, SourcesFile), func(raw []byte) ([]byte, []string, error) {
			return foldSwitches(raw, p.FeedSwitches)
		})
		if err != nil {
			return done, err
		}
		done = append(done, changes...)
	}
	return done, nil
}

func foldFile(path string, change func([]byte) ([]byte, []string, error)) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out, changes, err := change(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(changes) == 0 {
		return nil, nil
	}
	return changes, os.WriteFile(path, out, 0o644)
}

// lines is a file being edited by line number, as the YAML parser counts them
// (from 1). Edits are recorded and applied together, so each one's line number
// still means the line the parser saw.
type lines struct {
	text    []string
	drop    map[int]bool
	after   map[int][]string
	replace map[int]string
}

func newLines(raw []byte) *lines {
	return &lines{
		text:    strings.Split(string(raw), "\n"),
		drop:    map[int]bool{},
		after:   map[int][]string{},
		replace: map[int]string{},
	}
}

func (l *lines) at(n int) string { return l.text[n-1] }

func (l *lines) bytes() []byte {
	var out []string
	for i, t := range l.text {
		n := i + 1
		if r, ok := l.replace[n]; ok {
			t = r
		}
		if !l.drop[n] {
			out = append(out, t)
		}
		out = append(out, l.after[n]...)
	}
	return []byte(strings.Join(out, "\n"))
}

func foldCompanies(raw []byte, e Edits) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("expected sectors at the top level")
	}
	root := doc.Content[0]
	sector := func(id string) (key, list *yaml.Node) {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == id {
				return root.Content[i], root.Content[i+1]
			}
		}
		return nil, nil
	}

	l := newLines(raw)
	var done []string

	for _, r := range e.Removed {
		_, list := sector(r.Sector)
		if list == nil {
			continue
		}
		for _, item := range list.Content {
			var c model.Company
			if item.Decode(&c) != nil || !c.Is(r.Company) {
				continue
			}
			l.drop[item.Line] = true
			// The comment directly above an entry is about that entry: it
			// goes with it rather than being left to explain nothing.
			indent := l.at(item.Line)[:strings.Index(l.at(item.Line), "-")]
			for n := item.Line - 1; n >= 1 && strings.HasPrefix(l.at(n), indent+"#"); n-- {
				l.drop[n] = true
			}
			done = append(done, fmt.Sprintf("removed %s from %s", r.Company, r.Sector))
		}
	}

	for _, a := range e.Added {
		key, list := sector(a.Sector)
		if list == nil {
			return nil, done, fmt.Errorf("no sector %s to add %s to", a.Sector, firstTerm(a.Company))
		}
		present := false
		for _, item := range list.Content {
			var c model.Company
			if item.Decode(&c) == nil && c.Is(firstTerm(a.Company)) {
				present = true
			}
		}
		if present {
			continue
		}
		entry, err := flowEntry(tidy(a.Company))
		if err != nil {
			return nil, done, err
		}
		if len(list.Content) == 0 {
			// "macro-rates: []" becomes a block list of one.
			l.replace[key.Line] = key.Value + ":"
			l.after[key.Line] = append(l.after[key.Line], "  - "+entry)
		} else {
			last := list.Content[len(list.Content)-1].Line
			prefix := l.at(last)[:strings.Index(l.at(last), "-")+2]
			l.after[last] = append(l.after[last], prefix+entry)
		}
		done = append(done, fmt.Sprintf("added %s to %s", firstTerm(a.Company), a.Sector))
	}
	return l.bytes(), done, nil
}

// flowEntry writes a company the way the file lists them, on one line:
// {symbol: PLTR, name: Palantir}. The encoder does the quoting.
func flowEntry(c model.Company) (string, error) {
	var node yaml.Node
	if err := node.Encode(c); err != nil {
		return "", err
	}
	node.Style = yaml.FlowStyle
	out, err := yaml.Marshal(&node)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func foldSwitches(raw []byte, switches map[string]bool) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.SequenceNode {
		return nil, nil, fmt.Errorf("expected a list of feeds")
	}

	l := newLines(raw)
	var done []string
	for _, feed := range doc.Content[0].Content {
		var id string
		var enabled *yaml.Node
		for i := 0; i+1 < len(feed.Content); i += 2 {
			switch feed.Content[i].Value {
			case "id":
				id = feed.Content[i+1].Value
			case "enabled":
				enabled = feed.Content[i+1]
			}
		}
		on, ok := switches[id]
		if !ok || enabled == nil || enabled.Value == fmt.Sprint(on) {
			continue
		}
		line := l.at(enabled.Line)
		col := enabled.Column - 1
		l.replace[enabled.Line] = line[:col] + fmt.Sprint(on) + line[col+len(enabled.Value):]
		state := "off"
		if on {
			state = "on"
		}
		done = append(done, fmt.Sprintf("switched %s %s", id, state))
	}
	return l.bytes(), done, nil
}

// GlossaryFile is the glossary's file in the config directory.
const GlossaryFile = "glossary.yaml"

// FoldTerms adds the terms the service learned to the glossary in dir, each
// on the search it was learned with, under a comment saying where they came
// from. A term whose word the glossary already has is left out. Reading the
// diff, a learned term can be pointed at a better page by hand.
func FoldTerms(dir string, learned []model.Term, now time.Time) ([]string, error) {
	return foldFile(filepath.Join(dir, GlossaryFile), func(raw []byte) ([]byte, []string, error) {
		var have []model.Term
		if err := yaml.Unmarshal(raw, &have); err != nil {
			return nil, nil, err
		}
		known := map[string]bool{}
		for _, t := range have {
			for _, w := range t.Words {
				known[strings.ToLower(w)] = true
			}
		}
		var add, changes []string
		for _, t := range learned {
			if len(t.Words) == 0 || known[strings.ToLower(t.Words[0])] {
				continue
			}
			known[strings.ToLower(t.Words[0])] = true
			add = append(add, "- url: "+strconv.Quote(t.URL), "  words: ["+strconv.Quote(t.Words[0])+"]")
			changes = append(changes, "glossary: learned "+t.Words[0])
		}
		if len(add) == 0 {
			return raw, nil, nil
		}
		text := strings.TrimRight(string(raw), "\n") + "\n\n# Learned from the reports, " + now.Format("2 Jan 2006") +
			": each links to a search for its meaning.\n" + strings.Join(add, "\n") + "\n"
		return []byte(text), changes, nil
	})
}
