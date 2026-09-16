package fundamentals

import (
	"context"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/discover"
)

type fakeVerifier struct {
	known map[string]string
	err   error
}

func (f fakeVerifier) Verify(_ context.Context, queries []discover.Query) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = f.known[strings.ToUpper(q.Ticker)]
	}
	return out, nil
}

func TestSplitRelatedSeparatesTheListFromTheProse(t *testing.T) {
	text := strings.Join([]string{
		"THE CASE AGAINST IT",
		"",
		"- Cycle risk - revenue fell 49.5% in the year to 31 August 2023.",
		"",
		RelatedMarker,
		"Samsung Electronics|005930|KS|The other half of the memory duopoly",
		"SK hynix|000660|KS|Its closest competitor on high-bandwidth memory",
		"Applied Materials|AMAT|US|Sells it the equipment; its orders lead this spending",
	}, "\n")

	prose, related := SplitRelated(text)
	if strings.Contains(prose, RelatedMarker) || strings.Contains(prose, "005930") {
		t.Errorf("the list was left in the prose:\n%s", prose)
	}
	if !strings.Contains(prose, "Cycle risk") {
		t.Errorf("the analysis was lost:\n%s", prose)
	}
	if len(related) != 3 {
		t.Fatalf("got %d related companies, want 3: %+v", len(related), related)
	}
	if related[0].Symbol() != "005930.KS" {
		t.Errorf("Symbol = %q, want the exchange named", related[0].Symbol())
	}
	if related[2].Symbol() != "AMAT" {
		t.Errorf("a US listing should need no suffix: %q", related[2].Symbol())
	}
}

func TestSplitRelatedWithoutASectionLeavesTheTextAlone(t *testing.T) {
	prose, related := SplitRelated("THE BUSINESS\n\n- What it makes - memory.")
	if related != nil {
		t.Errorf("invented %d companies", len(related))
	}
	if !strings.Contains(prose, "memory") {
		t.Error("the prose was damaged")
	}
}

// The point of this list is to name something the reader can go and look at, so
// a name whose symbol cannot be confirmed serves nothing and is dropped.
func TestVerifyRelatedDropsWhatItCannotConfirm(t *testing.T) {
	related := []Related{
		{Name: "Samsung Electronics", Ticker: "005930", Exchange: "KS", Why: "The other half of the duopoly"},
		{Name: "Nonesuch Memory", Ticker: "ZZZZ", Exchange: "US", Why: "Invented"},
		{Name: "Applied Materials", Ticker: "CRM", Exchange: "US", Why: "Wrong symbol for the name"},
		{Name: "Some Private Firm", Why: "No ticker at all"},
	}
	v := fakeVerifier{known: map[string]string{
		"005930": "SAMSUNG ELECTRONICS CO LTD",
		"CRM":    "SALESFORCE INC",
	}}

	got := VerifyRelated(context.Background(), v, related)
	if len(got) != 1 {
		t.Fatalf("got %d, want only the confirmed one: %+v", len(got), got)
	}
	if got[0].Name != "Samsung Electronics" || got[0].Listed != "SAMSUNG ELECTRONICS CO LTD" {
		t.Errorf("wrong company kept: %+v", got[0])
	}
}

// If the check itself fails, nothing is shown: an unverified ticker is exactly
// what this section exists to prevent.
func TestVerifyRelatedShowsNothingWhenTheCheckFails(t *testing.T) {
	related := []Related{{Name: "Samsung", Ticker: "005930", Exchange: "KS", Why: "Peer"}}
	if got := VerifyRelated(context.Background(), fakeVerifier{err: context.DeadlineExceeded}, related); got != nil {
		t.Errorf("showed %d unverified companies", len(got))
	}
}

func TestRelatedInstructionNamesTheFormatAndTheCheck(t *testing.T) {
	instruction := RelatedFor(Snapshot{Company: "MICRON TECHNOLOGY INC"})
	for _, want := range []string{
		RelatedMarker,
		"name|ticker|exchange|what it would show",
		"checked against the exchange",
		"Do not list MICRON TECHNOLOGY INC itself",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("the instruction is missing %q:\n%s", want, instruction)
		}
	}
}
