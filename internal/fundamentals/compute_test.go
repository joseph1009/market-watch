package fundamentals

import (
	"math"
	"testing"
)

func TestComputeArithmetic(t *testing.T) {
	cases := []struct {
		expression string
		want       float64
	}{
		{"2 + 3 * 4", 14},
		{"(2 + 3) * 4", 20},
		{"10 / 4", 2.5},
		{"-3 + 1", -2},
		{"2 ^ 3 ^ 2", 512}, // right-associative, as written on paper
		{"1.2bn / 400m", 3},
		{"43,500 / 1,000", 43.5},
		{"12%", 0.12},
		{"growth(100, 125)", 25},
		{"cagr(100, 200, 3)", 25.992104989487318},
		{"avg(2, 4, 9)", 5},
		{"max(1, 7, 3) - min(1, 7, 3)", 6},
		{"round(2.34567, 2)", 2.35},
		{"abs(0 - 4)", 4},
	}
	for _, c := range cases {
		got, err := Compute(c.expression)
		if err != nil {
			t.Errorf("Compute(%q) errored: %v", c.expression, err)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Compute(%q) = %v, want %v", c.expression, got, c.want)
		}
	}
}

// A ratio the model would otherwise have done in its head. Cash conversion is
// the check the method leans on hardest, so it is the one worth pinning.
func TestComputeCashConversion(t *testing.T) {
	got, err := Compute("15.4bn / 11.8bn")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1.3050847457627118) > 1e-9 {
		t.Fatalf("cash conversion = %v", got)
	}
	if s := Number(got); s != "1.3051" {
		t.Fatalf("formatted as %q", s)
	}
}

func TestComputeRejectsNonsense(t *testing.T) {
	cases := []string{
		"",
		"1 / 0",
		"(1 + 2",
		"2 +",
		"revenue / profit",
		"sqrt(0 - 4)",
		"growth(0, 5)",
		"cagr(100, 200, 0)",
		"3 & 4",
		"max()",
		"round(1, 99)",
	}
	for _, c := range cases {
		if got, err := Compute(c); err == nil {
			t.Errorf("Compute(%q) = %v, want an error", c, got)
		}
	}
}

// A percentage change from a loss is arithmetic without meaning, and the model
// is more likely to ask for it than a person would be.
func TestComputeRefusesGrowthFromALoss(t *testing.T) {
	if _, err := Compute("growth(-500m, 250m)"); err == nil {
		t.Fatal("growth from a negative base should be refused")
	}
}

func TestNumberFormatting(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1234567.891, "1,234,567.89"},
		{-98765.4, "-98,765.4"},
		{1.3050847, "1.3051"},
		{0.0723, "0.0723"},
		{21, "21"},
		{-0.5, "-0.5"},
	}
	for _, c := range cases {
		if got := Number(c.in); got != c.want {
			t.Errorf("Number(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The comma is a thousands separator inside a number and an argument separator
// between two of them, and the parser has to tell them apart by shape alone.
func TestComputeCommasAreThousandsNotArguments(t *testing.T) {
	got, err := Compute("max(1,500, 900)")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1500 {
		t.Fatalf("max(1,500, 900) = %v, want 1500", got)
	}
}
