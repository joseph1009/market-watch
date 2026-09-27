package config

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed singapore.yaml
var singaporeYAML []byte

// SingaporeShare is one of the Straits Times Index's companies.
type SingaporeShare struct {
	Symbol string `yaml:"symbol"`
	Name   string `yaml:"name"`
	Sector string `yaml:"sector"`
}

// Singapore is the list in singapore.yaml, which the weekly themes measure
// beside the US market.
func Singapore() ([]SingaporeShare, error) {
	var out []SingaporeShare
	if err := yaml.Unmarshal(singaporeYAML, &out); err != nil {
		return nil, fmt.Errorf("singapore.yaml: %w", err)
	}
	for i, s := range out {
		if s.Symbol == "" || s.Name == "" {
			return nil, fmt.Errorf("singapore.yaml: entry %d needs a symbol and a name", i+1)
		}
	}
	return out, nil
}
