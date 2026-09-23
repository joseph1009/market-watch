package model

import "testing"

func TestReferencedIsWhatTheProseCites(t *testing.T) {
	cited := []Article{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	r := Report{
		Overview: "Oil rose [3], and rose again [3][1].",
		Sections: []Section{{Body: "Chips fell [2]. A stray [9] and a year [2026] point at nothing."}},
		Cited:    cited,
	}

	got := r.Referenced()
	var ids []string
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	// Each once, in numbering order; d was offered and never cited, and the
	// numbers past the end are not articles.
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("Referenced = %v, want [a b c]", ids)
	}
}
