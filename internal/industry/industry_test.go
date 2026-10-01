package industry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/model"
)

type fakeCompleter struct {
	reply          string
	system, prompt string
}

func (f *fakeCompleter) Complete(_ context.Context, system, prompt string) (string, model.Usage, error) {
	f.system, f.prompt = system, prompt
	return f.reply, model.Usage{InputTokens: 10, OutputTokens: 20}, nil
}

type fakeVerifier map[string]string // TICKER|EXCHANGE -> registered name

func (f fakeVerifier) Verify(_ context.Context, queries []discover.Query) ([]string, error) {
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = f[q.Ticker+"|"+q.Exchange]
	}
	return out, nil
}

// The explanation comes back as prose and a list of companies under the
// part of the industry each belongs to; a company the exchange does not
// confirm, or one with no ticker, is not shown.
func TestAnIndustryIsExplainedPartByPart(t *testing.T) {
	c := &fakeCompleter{reply: `### 🧭 The big picture
- Robots are machines that sense, decide and move.

### ⚙️ Motion parts
- Gears and motors that make the arms move.

COMPANIES BY PART
part|name|ticker|exchange|why
Motion parts|Harmonic Drive Systems|6324|JP|precision gears in most robot arms
Motion parts|Made Up Robotics|MUR|US|invented
Robot makers|Fanuc|6954|JP|the largest maker of factory robots
Software|Some Startup|?|?|private`}
	v := fakeVerifier{"6324|JP": "HARMONIC DRIVE SYSTEMS INC", "6954|JP": "FANUC CORP"}
	e := &Explainer{Completer: c, Verifier: v, Now: func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }}

	got, err := e.Explain(context.Background(), "  robotics ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != "robotics" || !strings.Contains(c.prompt, "robotics") || !strings.Contains(c.prompt, "Thursday 1 October 2026") {
		t.Errorf("topic %q, prompt %q", got.Topic, c.prompt)
	}
	if !strings.Contains(c.system, Marker) {
		t.Error("the system prompt does not ask for the list")
	}
	if strings.Contains(got.Text, Marker) || !strings.Contains(got.Text, "### ⚙️ Motion parts") {
		t.Errorf("prose = %q", got.Text)
	}
	if len(got.Companies) != 2 || got.Companies[0].Part != "Motion parts" || got.Companies[0].Symbol() != "6324.JP" ||
		got.Companies[1].Name != "Fanuc" || got.Companies[1].Listed != "FANUC CORP" {
		t.Errorf("companies = %+v", got.Companies)
	}
}
