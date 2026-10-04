package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/joseph1009/market-watch/internal/model"
)

// The learned terms are added to the end of the glossary, still a list the
// service can load, and one the glossary already has is not added again.
func TestLearnedTermsJoinTheGlossary(t *testing.T) {
	dir := t.TempDir()
	start := "# Terms.\n\n- url: https://example.com/eps\n  words: [EPS]\n"
	if err := os.WriteFile(filepath.Join(dir, GlossaryFile), []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	learned := []model.Term{
		{URL: "https://www.google.com/search?q=CoWoS+chip+packaging+meaning", Words: []string{"CoWoS"}},
		{URL: "https://www.google.com/search?q=eps+meaning", Words: []string{"eps"}},
	}
	changes, err := FoldTerms(dir, learned, time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	if err != nil || len(changes) != 1 || changes[0] != "glossary: learned CoWoS" {
		t.Fatalf("changes %v, err %v", changes, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, GlossaryFile))
	if !strings.HasPrefix(string(raw), start) || !strings.Contains(string(raw), "# Learned from the reports, 4 Oct 2026") {
		t.Errorf("glossary:\n%s", raw)
	}
	var terms []model.Term
	if err := yaml.Unmarshal(raw, &terms); err != nil || len(terms) != 2 || terms[1].Words[0] != "CoWoS" || !strings.Contains(terms[1].URL, "CoWoS") {
		t.Errorf("terms %+v, err %v", terms, err)
	}
	if again, _ := FoldTerms(dir, learned, time.Now()); len(again) != 0 {
		t.Errorf("folded twice: %v", again)
	}
}
