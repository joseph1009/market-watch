package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/report"
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

	a.remember("the brief of Thu 10 Sep", []string{"the brief"})
	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, true))

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

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

// The daily brief is sent at once and its closer look an hour later: queued on
// the data volume, where a restart in that hour does not lose it, and sent to
// the owner and the channel once it falls due.
func TestTheDailyCloserLookFollowsTheBriefByAnHour(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	now := a.Now()

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Micron raises HBM outlook</title><link>https://feed.example/mu</link>
<description>Micron raised it.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()
	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nMicron raised its outlook [1].\n"}}
	withIdeas(a,
		stubCompleter{reply: "Rambus|RMBS|US|connected|1|Its chips go into every HBM stack."},
		stubCompleter{reply: "=== RMBS\nVERDICT: BUY\nCONFIDENCE: medium\nCASE: HBM demand runs through it [1].\nNUMBERS: 38x earnings\nRISK: One customer is a fifth of sales."})

	if err := a.publishScheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messagesTo(*sent, 4242), "\n"); !strings.Contains(got, "Micron raised its outlook") || strings.Contains(got, "Worth a closer look") {
		t.Fatalf("after the brief the owner has:\n%s", got)
	}
	lk, err := a.pendingLook()
	if err != nil || lk == nil || !lk.Due.Equal(now.Add(LookDelay)) || !lk.Share || len(lk.Cited) != 1 {
		t.Fatalf("queued %+v (err %v), want the brief's look due in an hour, for the channel too", lk, err)
	}

	// Not yet due: nothing is sent, and the wait is until it is, or the poll.
	a.Now = func() time.Time { return now.Add(59 * time.Minute) }
	if wait := a.sendDueLook(context.Background()); wait != lookPoll {
		t.Errorf("a minute early, wait %v", wait)
	}
	a.Now = func() time.Time { return now.Add(LookDelay + time.Minute) }
	a.sendDueLook(context.Background())

	for _, chat := range []int64{4242, testChannel} {
		if got := strings.Join(messagesTo(*sent, chat), "\n"); !strings.Contains(got, "Worth a closer look") || !strings.Contains(got, "Rambus") {
			t.Errorf("chat %d has no closer look:\n%s", chat, got)
		}
	}
	if lk, _ := a.pendingLook(); lk != nil {
		t.Error("the look was sent and is still queued")
	}
}

// A look that waited through most of a day, the process being down, is
// dropped rather than sent: yesterday's verdicts are not news.
func TestAStaleCloserLookIsDropped(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	withIdeas(a,
		stubCompleter{reply: "Rambus|RMBS|US|connected|1|Its chips go into every HBM stack."},
		stubCompleter{reply: "=== RMBS\nVERDICT: BUY\nCONFIDENCE: medium"})
	lk := lookFrom(todaysBrief, false)
	lk.Due = a.now().Add(-lookStale - time.Minute)
	if err := a.queueLook(lk); err != nil {
		t.Fatal(err)
	}
	a.sendDueLook(context.Background())
	if len(*sent) != 0 {
		t.Errorf("sent %+v", *sent)
	}
	if lk, _ := a.pendingLook(); lk != nil {
		t.Error("the stale look is still queued")
	}
}

// The screen's choices among the followed companies join the new names; a
// followed company judged HOLD is left out, and a followed BUY leads.
func TestAFollowedHoldIsLeftOutOfTheCloserLook(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.prefs.Groups = []model.Group{{ID: "semis", Name: "Semis", Companies: []model.Company{
		{Symbol: "MU", Name: "Micron"}, {Symbol: "NVDA", Name: "Nvidia"},
	}}}
	withIdeas(a,
		stubCompleter{reply: "Rambus|RMBS|US|connected|1|Its chips go into every HBM stack."},
		stubCompleter{reply: "=== MU\nVERDICT: BUY\nCONFIDENCE: high\nREACTION: underreacted, the outlook rose and the price fell.\n" +
			"=== NVDA\nVERDICT: HOLD\nCONFIDENCE: medium\n" +
			"=== RMBS\nVERDICT: HOLD\nCONFIDENCE: low"})
	a.Screener = &ideas.Screener{Completer: stubCompleter{reply: "MU|Forecasts up while the share fell.\nNVDA|Big run."}}

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	for _, want := range []string{"Companies you follow", "Micron", "underreacted", "Rambus", "<b>HOLD</b> · low confidence"} {
		if !strings.Contains(owner, want) {
			t.Errorf("the closer look is missing %q:\n%s", want, owner)
		}
	}
	if strings.Contains(owner, "Nvidia") {
		t.Errorf("a followed HOLD was shown:\n%s", owner)
	}
}

// askedFor records which companies each verdict call was asked about.
type askedFor struct {
	reply string
	mu    *sync.Mutex
	calls *[]string
}

func (s askedFor) Complete(_ context.Context, _, prompt string) (string, model.Usage, error) {
	var symbols []string
	for _, line := range strings.Split(prompt, "\n") {
		if sym, ok := strings.CutPrefix(line, "=== "); ok {
			symbols = append(symbols, sym)
		}
	}
	s.mu.Lock()
	*s.calls = append(*s.calls, strings.Join(symbols, " "))
	s.mu.Unlock()
	return s.reply, model.Usage{}, nil
}

// Twenty are shown every day. The research's names past those needed stand
// by, and a followed HOLD, which is not shown, is replaced by the first of
// them -- judged in a round of its own, so a spare is judged only when it is
// used.
func TestAHiddenHoldIsReplacedSoTwentyAreShown(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.prefs.Groups = []model.Group{{ID: "semis", Name: "Semis", Companies: []model.Company{
		{Symbol: "MU", Name: "Micron"}, {Symbol: "NVDA", Name: "Nvidia"},
	}}}

	// Twenty new names, QAA to QAT, best first; every one a HOLD, which is
	// shown for a new name.
	var research, verdicts strings.Builder
	verified := stubVerifier{}
	var names []string
	for i := range ideas.LookSize {
		sym := fmt.Sprintf("Q%c%c", 'A'+i/26, 'A'+i%26)
		names = append(names, sym)
		fmt.Fprintf(&research, "Company %s|%s|US|news|1|In the news.\n", sym, sym)
		fmt.Fprintf(&verdicts, "=== %s\nVERDICT: HOLD\nCONFIDENCE: low\n", sym)
		verified[sym+".US"] = "COMPANY " + sym
	}
	verdicts.WriteString("=== MU\nVERDICT: BUY\nCONFIDENCE: high\n=== NVDA\nVERDICT: HOLD\nCONFIDENCE: medium\n")

	var calls []string
	a.Researcher = &ideas.Researcher{Completer: stubCompleter{reply: research.String()}, Verifier: verified}
	a.Judge = &ideas.Judge{Completer: askedFor{reply: verdicts.String(), mu: &sync.Mutex{}, calls: &calls}}
	a.Screener = &ideas.Screener{Completer: stubCompleter{reply: "MU|Forecasts up while the share fell.\nNVDA|Big run."}}

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	shown := strings.Count(owner, "<code>Q") + strings.Count(owner, "<code>MU</code>")
	if shown != ideas.LookSize {
		t.Errorf("%d companies shown, want %d:\n%s", shown, ideas.LookSize, owner)
	}
	// Two followed and eighteen new are judged first; Nvidia's HOLD leaves a
	// place, which the nineteenth takes. The twentieth is never judged.
	stand, last := names[18], names[19]
	if !strings.Contains(owner, "<code>"+stand+"</code>") || strings.Contains(owner, "Nvidia") {
		t.Errorf("the stand-in %s did not take the followed HOLD's place:\n%s", stand, owner)
	}
	if len(calls) == 0 {
		t.Fatal("no verdicts were asked for")
	}
	if calls[len(calls)-1] != stand {
		t.Errorf("the last verdict call was for %q, want the stand-in alone; calls: %q", calls[len(calls)-1], calls)
	}
	for _, c := range calls {
		if strings.Contains(c, last) {
			t.Errorf("%s was judged though no place was left for it; calls: %q", last, calls)
		}
	}
}
