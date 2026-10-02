package config

import (
	"strings"
	"testing"
)

// The method is carried in Agent Skill format so the same file can be uploaded
// to the Skills API unchanged. What the model reads is the body: its own filing
// card tells it nothing.
func TestMethodDropsTheSkillFrontmatter(t *testing.T) {
	if strings.HasPrefix(Method, "---") || strings.Contains(Method, "name: financial-analysis") {
		t.Errorf("the frontmatter reached the prompt:\n%s", Method[:200])
	}
	if !strings.HasPrefix(Method, "# Reading filed accounts") {
		t.Errorf("the method does not start with its heading:\n%s", Method[:200])
	}
}

func TestMethodCarriesTheDiagnosticsAndTheSectorTraps(t *testing.T) {
	for _, want := range []string{
		"Cash conversion",
		"accrual check",
		"Days of receivables",
		"DuPont",
		"Banks and insurers",
		"Semiconductors, memory, shipping",
		"Never compute a multiple across currencies",
		"worst year on the page",
		"CAN SLIM",
		"Where the checklist does not fit",
	} {
		if !strings.Contains(Method, want) {
			t.Errorf("the method is missing %q", want)
		}
	}
}

func TestStripFrontmatterLeavesADocumentWithoutItAlone(t *testing.T) {
	plain := "# Heading\n\nBody."
	if got := stripFrontmatter(plain); got != plain {
		t.Errorf("stripFrontmatter changed a document with no frontmatter: %q", got)
	}
}
