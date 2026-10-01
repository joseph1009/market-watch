package config

import (
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/joseph1009/market-watch/internal/model"
)

//go:embed glossary.yaml
var glossaryYAML []byte

// Glossary is the terms in glossary.yaml, which the brief links to an
// explanation.
func Glossary() ([]model.Term, error) {
	var out []model.Term
	if err := yaml.Unmarshal(glossaryYAML, &out); err != nil {
		return nil, fmt.Errorf("glossary.yaml: %w", err)
	}
	for i, t := range out {
		if !strings.HasPrefix(t.URL, "https://") || len(t.Words) == 0 {
			return nil, fmt.Errorf("glossary.yaml: entry %d needs an https url and at least one word", i+1)
		}
	}
	return out, nil
}
