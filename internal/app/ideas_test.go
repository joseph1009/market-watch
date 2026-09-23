package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// stubCompleter answers every call with the same text, or fails.
type stubCompleter struct {
	reply string
	err   error
}

func (s stubCompleter) Complete(context.Context, string, string) (string, model.Usage, error) {
	return s.reply, model.Usage{}, s.err
}

type stubVerifier map[string]string

func (v stubVerifier) Verify(_ context.Context, queries []discover.Query) ([]string, error) {
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = v[q.Ticker+"."+q.Exchange]
	}
	return out, nil
}

func withIdeas(a *App, research, verdicts stubCompleter) {
	a.Researcher = &ideas.Researcher{Completer: research, Verifier: stubVerifier{"RMBS.US": "RAMBUS INC"}}
	a.Judge = &ideas.Judge{Completer: verdicts}
}

var todaysBrief = model.Report{
	Overview: "Micron raised its outlook [1].",
	Cited:    []model.Article{{ID: "a1", Title: "Micron raises HBM outlook", URL: "https://example.com/1"}},
}

// A brief asked for with /now is a check, so its closer look stays with the
// owner -- and /share cannot pass it on either, since /share posts the last
// brief or analysis and a verdict is neither.
func TestACloserLookNotSharedStaysWithTheOwner(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	withIdeas(a,
		stubCompleter{reply: "Rambus|RMBS|US|connected|1|Its chips go into every HBM stack."},
		stubCompleter{reply: "=== RMBS\nVERDICT: BUY\nCONFIDENCE: medium\nCASE: HBM demand runs through it [1].\nNUMBERS: 38x earnings\nRISK: One customer is a fifth of sales."})

	brief := a.remember("the brief of Thu 10 Sep", []string{"the brief"})
	a.sendIdeas(context.Background(), &briefDone{sent: brief, rep: todaysBrief}, false)

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	for _, want := range []string{"Worth a closer look", "Rambus", "RMBS", "<b>BUY</b>", "medium confidence", "Connected to today's news"} {
		if !strings.Contains(owner, want) {
			t.Errorf("the owner's closer look is missing %q:\n%s", want, owner)
		}
	}
	if got := messagesTo(*sent, testChannel); len(got) != 0 {
		t.Fatalf("the channel was sent %q", got)
	}
	if len(a.Prefs().LastBrief) == 0 {
		t.Error("the closer look's messages were not recorded for clearing with the brief")
	}

	a.HandleMessage(context.Background(), message("/share"))
	channel := strings.Join(messagesTo(*sent, testChannel), "\n")
	if channel != "the brief" {
		t.Errorf("/share posted %q to the channel, want the brief and nothing of the verdicts", channel)
	}
}

// The daily run's closer look does reach the channel, and must never arrive
// there bare: the note saying a model wrote it, that nobody checked it and
// that it is not advice is the whole basis on which it is allowed out.
func TestTheDailyCloserLookReachesTheChannelUnderItsWarning(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	withIdeas(a,
		stubCompleter{reply: "Rambus|RMBS|US|connected|1|Its chips go into every HBM stack."},
		stubCompleter{reply: "=== RMBS\nVERDICT: BUY\nCONFIDENCE: medium\nCASE: HBM demand runs through it [1].\nNUMBERS: 38x earnings\nRISK: One customer is a fifth of sales."})

	a.sendIdeas(context.Background(), &briefDone{rep: todaysBrief}, true)

	channel := strings.Join(messagesTo(*sent, testChannel), "\n")
	for _, want := range []string{"Worth a closer look", "Rambus", "<b>BUY</b>", "not financial advice", "nobody checking its work", "someone licensed"} {
		if !strings.Contains(channel, want) {
			t.Errorf("the channel's closer look is missing %q:\n%s", want, channel)
		}
	}
	// The owner's copy is headed for the owner, not with the reader's warning.
	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	if !strings.Contains(owner, "/scorecard") || strings.Contains(owner, "not financial advice") {
		t.Errorf("the owner's copy took the channel's note:\n%s", owner)
	}
}

// Research that fails costs the section and nothing else: the brief has
// already gone, and nobody is sent a half-finished list.
func TestFailedResearchSendsNothing(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	withIdeas(a, stubCompleter{err: errors.New("claude: timed out")}, stubCompleter{})

	a.sendIdeas(context.Background(), &briefDone{rep: todaysBrief}, false)

	if len(*sent) != 0 {
		t.Errorf("sent %+v after the research failed", *sent)
	}
}

// A verdict reply that names nobody it was asked about leaves nothing to show,
// rather than a heading over an empty list.
func TestNoVerdictsMeansNoMessage(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	withIdeas(a,
		stubCompleter{reply: "Rambus|RMBS|US|connected|1|Its chips go into every HBM stack."},
		stubCompleter{reply: "I would rather not say."})

	a.sendIdeas(context.Background(), &briefDone{rep: todaysBrief}, false)

	if len(*sent) != 0 {
		t.Errorf("sent %+v with no verdicts", *sent)
	}
}

func TestTheScorecardCommandAnswersBeforeThereIsAnything(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	card, err := ideas.LoadScorecard(t.TempDir() + "/scorecard.json")
	if err != nil {
		t.Fatal(err)
	}
	a.Scorecard = card

	a.HandleMessage(context.Background(), message("/scorecard"))

	if reply := lastReply(t, *sent); !strings.Contains(reply, "No verdicts yet") {
		t.Errorf("reply = %q", reply)
	}
}

// chartServer answers the chart source with a rising history in one currency,
// ending on the day before the test clock so the last session is a finished
// one.
func chartServer(t *testing.T, currency string, sessions int) *prices.History {
	t.Helper()

	last := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	var stamps, closes []string
	for i := sessions - 1; i >= 0; i-- {
		day := last.AddDate(0, 0, -i)
		stamps = append(stamps, strconv.FormatInt(day.Unix(), 10))
		closes = append(closes, strconv.FormatFloat(100+float64(sessions-1-i), 'f', 2, 64))
	}
	body := fmt.Sprintf(`{"chart":{"result":[{"meta":{"currency":%q,"symbol":"TEST","longName":"Test"},"timestamp":[%s],"indicators":{"quote":[{"close":[%s],"open":[%s],"high":[%s],"low":[%s],"volume":[%s]}]}}],"error":null}}`,
		currency,
		strings.Join(stamps, ","),
		strings.Join(closes, ","), strings.Join(closes, ","),
		strings.Join(closes, ","), strings.Join(closes, ","),
		strings.Repeat("1000,", sessions-1)+"1000")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &prices.History{HTTP: server.Client(), URL: server.URL + "/"}
}

// A company outside the US has no quote on any keyed feed's free tier. It takes
// its price from the same daily history the averages come from, in the currency
// it actually trades in -- otherwise a verdict on a Hong Kong company is asked
// for with no price at all.
func TestAListingOutsideTheUSIsPricedFromItsHistory(t *testing.T) {
	a, _ := newTestApp(t)
	a.Quotes = &prices.Client{} // no key, as on a free tier
	a.Market = chartServer(t, "HKD", 60)

	idea, chart, facts := a.ideaFacts(context.Background(), model.Idea{
		Name: "Tencent", Ticker: "700", Exchange: "HK", Listed: "TENCENT HOLDINGS LTD",
	})

	if chart != "0700.HK" {
		t.Errorf("chart symbol = %q, want 0700.HK", chart)
	}
	if idea.Quote == nil {
		t.Fatal("a Hong Kong listing came back with no price")
	}
	if idea.Quote.Unit() != "HKD" {
		t.Errorf("price is in %q, want HKD", idea.Quote.Unit())
	}
	if idea.Quote.Price != 159 {
		t.Errorf("price = %v, want the last close of 159", idea.Quote.Price)
	}
	if idea.Trading == nil {
		t.Error("no trading history alongside the price")
	}
	if idea.Accounts {
		t.Error("a Hong Kong listing was marked as having SEC accounts")
	}
	if !strings.Contains(facts, "No SEC accounts were read for it") {
		t.Errorf("the fact sheet does not admit the accounts are missing:\n%s", facts)
	}
}

// The US keeps its live quote. The chart's last bar is a close, and on a
// session still running the feed knows something it does not.
func TestAUSListingKeepsItsLiveQuote(t *testing.T) {
	quotes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"c":94.2,"d":1.1,"dp":1.18,"h":95,"l":92,"pc":93.1,"t":1789000000}`))
	}))
	t.Cleanup(quotes.Close)

	a, _ := newTestApp(t)
	a.Quotes = &prices.Client{APIKey: "test", HTTP: quotes.Client(), URL: quotes.URL, Pause: time.Millisecond}
	a.Market = chartServer(t, "USD", 60)

	idea, _, _ := a.ideaFacts(context.Background(), model.Idea{
		Name: "Rambus", Ticker: "RMBS", Exchange: "US", Listed: "RAMBUS INC",
	})

	if idea.Quote == nil {
		t.Fatal("no price for a US listing")
	}
	if idea.Quote.Price != 94.2 {
		t.Errorf("price = %v, want 94.2 from the quote feed rather than the chart's close", idea.Quote.Price)
	}
	if idea.Quote.Unit() != "USD" {
		t.Errorf("price is in %q, want USD", idea.Quote.Unit())
	}
}
