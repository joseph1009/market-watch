package fundamentals

import (
	"strings"
	"testing"
)

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
