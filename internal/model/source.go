package model

// Source is a news feed Market Watch polls. Weight ranks sources against each
// other when the same story shows up in several of them: higher wins.
type Source struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	URL     string `yaml:"url" json:"url"`
	Weight  int    `yaml:"weight" json:"weight"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}
