package model

import "strings"

// Term is a piece of jargon and the page that explains it: the brief links
// the first of its words in each section to the page.
type Term struct {
	URL   string   `yaml:"url"`
	Words []string `yaml:"words"`
}

// ListedTerm is a term a writer listed as one it used, with a few words on
// what field it belongs to: "HBM", "memory chips". The context goes into the
// search for its meaning, so an abbreviation finds the right one.
type ListedTerm struct {
	Term    string `json:"term"`
	Context string `json:"context,omitempty"`
}

// ParseTerms reads a writer's list of terms, one to a line as
// "- term | context", once each whatever the case. A line that is too long or
// too many words to be a term is left out: it is a sentence, not a term.
func ParseTerms(lines []string) []ListedTerm {
	var out []ListedTerm
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-•*"))
		term, context, _ := strings.Cut(line, "|")
		// "HBM: memory chips" is the same as "HBM | memory chips".
		if t, c, ok := strings.Cut(term, ":"); ok && context == "" {
			term, context = t, c
		}
		term = strings.Trim(strings.TrimSpace(term), `"'*`)
		context = strings.Trim(strings.TrimSpace(context), `"'*.`)
		key := strings.ToLower(term)
		if term == "" || len(term) > 40 || len(strings.Fields(term)) > 4 || seen[key] {
			continue
		}
		if len(strings.Fields(context)) > 6 {
			context = ""
		}
		seen[key] = true
		out = append(out, ListedTerm{Term: term, Context: context})
	}
	return out
}
