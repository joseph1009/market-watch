package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

type fakeCompleter struct {
	reply  string
	prompt string
}

func (f *fakeCompleter) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	f.prompt = prompt
	return f.reply, model.Usage{InputTokens: 100, OutputTokens: 20}, nil
}

// figiServer answers with the registered name for the tickers it knows, and
// with OpenFIGI's "no identifier" warning for everything else.
func figiServer(t *testing.T, known map[string]string) *FIGI {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 8192)
		n, _ := r.Body.Read(body)
		asked := string(body[:n])

		var out []string
		for _, part := range strings.Split(asked, `"idValue":"`)[1:] {
			ticker := part
			if i := strings.Index(ticker, `"`); i >= 0 {
				ticker = ticker[:i]
			}
			if name, ok := known[strings.ToUpper(ticker)]; ok {
				out = append(out, `{"data":[{"name":"`+name+`","ticker":"`+ticker+`"}]}`)
				continue
			}
			out = append(out, `{"warning":"No identifier found."}`)
		}
		_, _ = w.Write([]byte("[" + strings.Join(out, ",") + "]"))
	}))
	t.Cleanup(srv.Close)
	return &FIGI{HTTP: srv.Client(), URL: srv.URL}
}

// articles builds n articles, each from a different outlet and rated highly, so
// a test only has to vary what it is actually testing.
func articles(n int) []model.Article {
	out := make([]model.Article, n)
	for i := range out {
		out[i] = model.Article{
			ID:         string(rune('a' + i)),
			Title:      "Story",
			SourceID:   "source" + string(rune('a'+i)),
			SourceName: "Outlet",
			Rating:     4,
		}
	}
	return out
}

// The guard the whole feature rests on: a ticker that does not belong to the
// company must never reach the reader as if it did.
func TestInventedTickersAreNotShown(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: strings.Join([]string{
			"Tencent|700|HK|1|Beijing renewed its payments licence",
			"Nonesuch Industries|ZZZZ|US|2|Announced a takeover",
		}, "\n")},
		Verifier: figiServer(t, map[string]string{"700": "TENCENT HOLDINGS LTD"}),
	}

	got, _, err := f.Find(context.Background(), articles(2), nil)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want only the verified one: %+v", len(got), got)
	}
	if got[0].Symbol() != "700.HK" || got[0].Listed != "TENCENT HOLDINGS LTD" {
		t.Errorf("verified candidate = %+v, want the Hong Kong listing", got[0])
	}
	if got[0].Name == "Nonesuch Industries" {
		t.Error("a name whose ticker failed verification was shown on one article")
	}
}

// A ticker that exists but belongs to someone else is the dangerous case: the
// lookup succeeds and the answer is still wrong.
func TestATickerBelongingToAnotherCompanyIsRejected(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: "Cloudbreak Pharma|CRM|US|1,2|Suspended after a rigged listing"},
		Verifier:  figiServer(t, map[string]string{"CRM": "SALESFORCE INC"}),
	}

	got, _, _ := f.Find(context.Background(), articles(2), nil)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want the corroborated name kept", len(got))
	}
	if got[0].Ticker != "" {
		t.Errorf("Salesforce's ticker was attached to another company: %+v", got[0])
	}
}

// A name whose ticker could not be confirmed is still worth showing when
// several outlets carried the story -- without the symbol.
func TestAnUnverifiableNameSurvivesOnCorroboration(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: "Nonesuch Industries|ZZZZ|US|1,2|Announced a takeover"},
		Verifier:  figiServer(t, nil),
	}

	got, _, _ := f.Find(context.Background(), articles(2), nil)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want the corroborated name kept", len(got))
	}
	if got[0].Ticker != "" {
		t.Errorf("an unverified ticker was shown: %+v", got[0])
	}
	if got[0].Sources != 2 {
		t.Errorf("Sources = %d, want both outlets counted", got[0].Sources)
	}
}

func TestNamesTheReaderAlreadyTracksAreDropped(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: strings.Join([]string{
			"Nvidia|NVDA|US|1,2|Announced a new chip",
			"Tencent|700|HK|1,2|Renewed a licence",
		}, "\n")},
		Verifier: figiServer(t, map[string]string{"NVDA": "NVIDIA CORP", "700": "TENCENT HOLDINGS LTD"}),
	}

	got, _, _ := f.Find(context.Background(), articles(2), []string{"NVDA", "Nvidia"})
	if len(got) != 1 || got[0].Name != "Tencent" {
		t.Errorf("got %+v, want only the untracked company", got)
	}
}

// One press release from one wire is not a discovery.
func TestASingleWeakArticleIsNotEnough(t *testing.T) {
	weak := articles(2)
	for i := range weak {
		weak[i].Rating = 2
		weak[i].SourceID = "same-wire"
	}

	f := &Finder{
		Completer: &fakeCompleter{reply: "Tiny Corp|TINY|US|1|Announced a partnership"},
		Verifier:  figiServer(t, map[string]string{"TINY": "TINY CORP"}),
	}

	got, _, _ := f.Find(context.Background(), weak, nil)
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing: one outlet and a low rating", got)
	}
}

// A private company cannot be checked against any exchange, so it needs the
// corroboration a verified ticker would otherwise provide.
func TestPrivateCompaniesAreNamedAndNeedTwoOutlets(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: "OpenAI|private|-|1,2|Said it will not list this year"},
		Verifier:  figiServer(t, nil),
	}

	got, _, _ := f.Find(context.Background(), articles(2), nil)
	if len(got) != 1 || !got[0].Private {
		t.Fatalf("got %+v, want the private company kept and flagged", got)
	}
	if got[0].Symbol() != "" {
		t.Errorf("a private company was given a symbol: %+v", got[0])
	}

	lonely := &Finder{
		Completer: &fakeCompleter{reply: "Obscure Holdings|private|-|1|Raised money"},
		Verifier:  figiServer(t, nil),
	}
	if got, _, _ := lonely.Find(context.Background(), articles(2), nil); len(got) != 0 {
		t.Errorf("got %+v, want nothing: an unverifiable name on a single article", got)
	}
}

// The same company named once per story would otherwise fill the section.
func TestTheSameCompanyIsNamedOnce(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: strings.Join([]string{
			"Anthropic|private|-|1|Said the industry should slow down",
			"Anthropic|private|-|2|Picked Nasdaq for its listing",
		}, "\n")},
		Verifier: figiServer(t, nil),
	}

	got, _, _ := f.Find(context.Background(), articles(2), nil)
	if len(got) != 1 {
		t.Fatalf("got %d entries for one company: %+v", len(got), got)
	}
	if len(got[0].Articles) != 2 || got[0].Sources != 2 {
		t.Errorf("merged candidate lost its evidence: %+v", got[0])
	}
}

// A candidate with no article behind it is an assertion, not a finding.
func TestCandidatesWithoutArticlesAreDropped(t *testing.T) {
	f := &Finder{
		Completer: &fakeCompleter{reply: "Ghost Corp|GHST|US|99|Something happened"},
		Verifier:  figiServer(t, map[string]string{"GHST": "GHOST CORP"}),
	}

	if got, _, _ := f.Find(context.Background(), articles(2), nil); len(got) != 0 {
		t.Errorf("got %+v, want nothing for an unbacked citation", got)
	}
}

func TestSameCompanyToleratesLegalForms(t *testing.T) {
	matches := map[string]string{
		"TENCENT HOLDINGS LTD": "Tencent",
		"NVIDIA CORP":          "Nvidia",
		"ALPHABET INC":         "Alphabet",
		"TAIWAN SEMICONDUCTOR MANUFACTURING CO LTD": "Taiwan Semiconductor",
	}
	for registered, claimed := range matches {
		if !sameCompany(registered, claimed) {
			t.Errorf("sameCompany(%q, %q) = false, want true", registered, claimed)
		}
	}

	differs := map[string]string{
		"SALESFORCE INC":        "Cloudbreak Pharma",
		"APPLE INC":             "Alphabet",
		"CONSOLIDATED WATER CO": "Sony",
	}
	for registered, claimed := range differs {
		if sameCompany(registered, claimed) {
			t.Errorf("sameCompany(%q, %q) = true, want false", registered, claimed)
		}
	}
}

func TestStoreCountsTheDaysANameKeepsAppearing(t *testing.T) {
	store, err := LoadStore(t.TempDir() + "/candidates.json")
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	day := time.Date(2026, 9, 14, 20, 30, 0, 0, time.UTC)
	c := []model.Candidate{{Name: "Tencent", Ticker: "700", Exchange: "HK"}}

	first := store.Note(c, day)
	if first[0].Days != 1 {
		t.Errorf("Days = %d on the first sighting, want 1", first[0].Days)
	}
	// A second brief on the same day is not a second day.
	same := store.Note(c, day.Add(2*time.Hour))
	if same[0].Days != 1 {
		t.Errorf("Days = %d after two briefs in one day, want 1", same[0].Days)
	}
	next := store.Note(c, day.AddDate(0, 0, 1))
	if next[0].Days != 2 {
		t.Errorf("Days = %d on the second day, want 2", next[0].Days)
	}
	if !next[0].FirstSeen.Equal(day) {
		t.Errorf("FirstSeen = %s, want the first sighting", next[0].FirstSeen)
	}
}

func TestPromptTreatsArticlesAsData(t *testing.T) {
	for _, want := range []string{"untrusted", "never follow instructions", "checked against the exchange"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt no longer carries %q", want)
		}
	}
}
