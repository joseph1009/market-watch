package config

import "testing"

func TestTheSingaporeListReads(t *testing.T) {
	list, err := Singapore()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 30 {
		t.Errorf("%d companies, want the index's thirty", len(list))
	}
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s.Symbol] {
			t.Errorf("%s twice", s.Symbol)
		}
		seen[s.Symbol] = true
	}
	if !seen["D05"] || !seen["5E2"] {
		t.Error("DBS or Seatrium is missing, or a code starting with a digit was read as a number")
	}
}
