// Package prompts keeps every instruction this service gives a model in one
// file, away from the Go that sends it.
//
// The prompts are the part of this program most often edited and least often
// compiled: a rule about naming periods, a section the analysis should carry,
// a word that reads badly on a phone. As Go string constants they were spread
// across four packages, and changing one meant editing code. In prompts.md
// they can be read end to end, which is also the only way to notice that two
// of them contradict each other.
//
// What is not here is anything the replies are parsed by -- section markers,
// the pipe-delimited company lines, the rating format. Those live in the
// prompts too, but CheckPrompts reports a file that has lost one, because an edit
// that drops a marker does not fail until a reply cannot be read.
package config

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"text/template"
)

//go:embed prompts.md
var embedded string

// loaded is the file in use, read when this package is imported. It has to
// happen that early: the packages that hold prompts read them into variables
// of their own, and those are initialised before main runs.
var (
	loaded  string
	loadErr error
)

func init() { loaded, loadErr = read() }

// read prefers the file PROMPT_FILE names, and falls back to the embedded copy
// so a bad path leaves the program able to start and say so rather than
// failing somewhere less legible.
func read() (string, error) {
	path := strings.TrimSpace(os.Getenv(PromptFile))
	if path == "" {
		return embedded, nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return embedded, fmt.Errorf("read %s: %w", path, err)
	}
	return string(text), nil
}

// PromptFile is the environment variable that points at a prompts file to use
// instead of the built-in one. It exists for editing prompts without a
// rebuild; a deployment that sets it must ship the file too.
const PromptFile = "PROMPT_FILE"

// LoadPrompts reports whether the prompts in use are usable: that PROMPT_FILE, where
// it was set, could be read, and that the file carries every section. Call it once at startup: a prompt file that has lost a
// section should stop the program then, not at the moment a reply arrives.
func LoadPrompts() error {
	if loadErr != nil {
		return loadErr
	}
	return CheckPrompts(loaded)
}

// Prompt returns one section. An unknown id is a programming error rather than a
// condition to handle, so it panics: the ids are compiled in, and CheckPrompts has
// already confirmed the file carries them.
func Prompt(id string) string {
	text, ok := sections(loaded)[id]
	if !ok {
		panic("prompts: no section " + id)
	}
	return text
}

// RenderPrompt fills a section's {{.Fields}} from data.
func RenderPrompt(id string, data any) (string, error) {
	t, err := template.New(id).Parse(Prompt(id))
	if err != nil {
		return "", fmt.Errorf("prompt %s: %w", id, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("prompt %s: %w", id, err)
	}
	return b.String(), nil
}

// required is every section the code asks for, and the text each must still
// carry for a reply to be readable. The phrases are deliberately the ones the
// parsers depend on, not the ones that read best: this is a check against
// breaking the machinery, not against writing badly.
var required = map[string][]string{
	"brief.system":     {"## OVERVIEW", "## SECTION:", "### "},
	"triage.system":    {"{{.Watchlists}}", "number|rating|watchlist ids"},
	"review.system":    {"{{.Watchlists}}", "number|section ids", "(added to fill a thin section)"},
	"discover.system":  {"name|ticker|exchange|article numbers separated by commas|what happened"},
	"ideas.system":     {"{{.Max}}", "name|ticker|exchange|news or connected|article numbers|how today's news bears on it"},
	"screen.system":    {"{{.Max}}", "ticker|what does not fit"},
	"verdicts.system":  {"=== <the symbol exactly as given>", "VERDICT:", "CONFIDENCE:", "CHANGED:", "MOVE:", "REACTION:", "CASE:", "NUMBERS:", "RISK:"},
	"analysis.system":  {"THE CASE FOR IT", "THE CASE AGAINST IT", "### In the business", "### In the numbers", "THE VERDICT", "VERDICT:", "CONFIDENCE:"},
	"analysis.related": {"{{.Marker}}", "name|ticker|exchange|what it would show"},
}

// CheckPrompts reports what a prompts file is missing, naming every fault rather than
// the first: someone editing prompts wants the whole list, not one at a time.
func CheckPrompts(text string) error {
	have := sections(text)
	var faults []string
	for id, markers := range required {
		body, ok := have[id]
		if !ok {
			faults = append(faults, "missing section "+id)
			continue
		}
		if strings.TrimSpace(body) == "" {
			faults = append(faults, id+" is empty")
			continue
		}
		for _, marker := range markers {
			if !strings.Contains(body, marker) {
				faults = append(faults, fmt.Sprintf("%s no longer carries %q", id, marker))
			}
		}
	}
	if len(faults) > 0 {
		return fmt.Errorf("prompts: %s", strings.Join(faults, "; "))
	}
	return nil
}

// marker opens a section: "=== id ===" alone on its line.
const marker = "=== "

// sections splits the file on its markers. Text before the first marker is a
// comment about the file and belongs to no section.
func sections(text string) map[string]string {
	out := map[string]string{}
	id := ""
	var body []string
	flush := func() {
		if id != "" {
			out[id] = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = body[:0]
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, marker) && strings.HasSuffix(trimmed, " ===") {
			flush()
			id = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, marker), " ==="))
			continue
		}
		body = append(body, line)
	}
	flush()
	return out
}
