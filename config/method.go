package config

import (
	_ "embed"
	"strings"
)

// The analysis knew how to read a table and not what to look for in one. This
// is the method: which diagnostics carry information, what each means, and
// where a standard ratio is the wrong question for the industry -- a bank has
// no gross margin, a memory maker's peak-year multiple is the most misleading
// number its accounts can produce.
//
// It is written in Agent Skill format, with the frontmatter a skill carries, so
// the same file can be uploaded to the Skills API unchanged if the analysis
// ever moves to a code-execution container. It is the only copy: an identical
// one used to sit in skills/, read by nothing, waiting to drift. Today it is read straight into the
// system prompt: no container to start, no beta to depend on, and the model
// follows it either way. The value of a skill here is the method it carries,
// not the machinery that delivers it.
//
//go:embed method.md
var methodRaw string

// Method is the analysis playbook, with the skill frontmatter stripped: the
// name and description are for a skills catalogue, and the model does not need
// to read its own filing card.
var Method = strings.TrimSpace(stripFrontmatter(methodRaw))

func stripFrontmatter(doc string) string {
	const fence = "---"
	if !strings.HasPrefix(doc, fence) {
		return doc
	}
	rest := doc[len(fence):]
	if end := strings.Index(rest, fence); end >= 0 {
		return rest[end+len(fence):]
	}
	return doc
}
