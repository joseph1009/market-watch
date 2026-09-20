package prompts

import (
	"strings"
	"testing"
)

// The file that ships has to be usable, or the service starts and then fails
// at the first reply it cannot read.
func TestTheEmbeddedFileIsComplete(t *testing.T) {
	if err := Check(embedded); err != nil {
		t.Fatalf("the built-in prompts file is not usable: %v", err)
	}
	for id := range required {
		if strings.TrimSpace(Get(id)) == "" {
			t.Errorf("section %s is empty", id)
		}
	}
}

// A prompt edited past the markers the replies are parsed by is the failure
// this guards against: the brief still reads well and nothing can be split out
// of it.
func TestCheckNamesEveryFault(t *testing.T) {
	file := `=== brief.system ===
Write a brief. No markers at all.

=== triage.system ===
{{.Watchlists}} and the line format: number|rating|watchlist ids separated by commas

=== discover.system ===
name|ticker|exchange|article numbers separated by commas|what happened

=== analysis.system ===
THE CASE FOR IT, THE CASE AGAINST IT, COMPANIES TO READ NEXT TO IT, name|ticker|exchange|what it would show
`
	err := Check(file)
	if err == nil {
		t.Fatal("a file with a section missing and a marker dropped passed the check")
	}
	for _, want := range []string{"## OVERVIEW", "missing section analysis.agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the fault list does not mention %q:\n%v", want, err)
		}
	}
}

func TestRenderFillsTheWatchlists(t *testing.T) {
	got, err := Render("triage.system", struct{ Watchlists string }{"- energy: Energy"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "- energy: Energy") {
		t.Errorf("the watchlists were not written in:\n%s", got)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("a slot was left unfilled:\n%s", got)
	}
}

// Sections are split on their own marker lines, and the note at the top of the
// file belongs to none of them.
func TestSectionsIgnoreThePreamble(t *testing.T) {
	got := sections("a note about the file\n\n=== one ===\nfirst\n\n=== two ===\nsecond\n")
	if len(got) != 2 || got["one"] != "first" || got["two"] != "second" {
		t.Errorf("sections read as %#v", got)
	}
}
