package model

// Term is a piece of jargon and the page that explains it: the brief links
// the first of its words in each section to the page.
type Term struct {
	URL   string   `yaml:"url"`
	Words []string `yaml:"words"`
}
