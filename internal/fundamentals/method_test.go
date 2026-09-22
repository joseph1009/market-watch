package fundamentals

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
	} {
		if !strings.Contains(Method, want) {
			t.Errorf("the method is missing %q", want)
		}
	}
}

// The method is only useful if the analysis actually receives it. It used to
// reach only the agent, which went with the API; this is the check that it
// did not go with it.
func TestTheAnalysisPromptCarriesTheMethod(t *testing.T) {
	if !strings.Contains(systemPrompt, "Cash conversion") {
		t.Error("the analysis prompt does not carry the method")
	}
	if !strings.Contains(systemPrompt, "THE CASE FOR IT") {
		t.Error("the analysis prompt lost its own instructions")
	}
	// The tools went with the agent. An instruction to call one that does not
	// exist would have the model announce lookups it cannot make.
	if strings.Contains(systemPrompt, "find_concepts") {
		t.Error("the analysis prompt still tells the model to use tools it does not have")
	}
}

func TestStripFrontmatterLeavesADocumentWithoutItAlone(t *testing.T) {
	plain := "# Heading\n\nBody."
	if got := stripFrontmatter(plain); got != plain {
		t.Errorf("stripFrontmatter changed a document with no frontmatter: %q", got)
	}
}
