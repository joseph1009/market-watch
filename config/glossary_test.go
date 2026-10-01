package config

import "testing"

func TestTheGlossaryLoads(t *testing.T) {
	terms, err := Glossary()
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) < 50 {
		t.Errorf("only %d terms", len(terms))
	}
	seen := map[string]bool{}
	for _, term := range terms {
		for _, w := range term.Words {
			if seen[w] {
				t.Errorf("%q is listed twice", w)
			}
			seen[w] = true
		}
	}
}
