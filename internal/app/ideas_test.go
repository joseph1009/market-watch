package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/discover"
	"github.com/joseph1009/market-watch/internal/feed"
	"github.com/joseph1009/market-watch/internal/ideas"
	"github.com/joseph1009/market-watch/internal/market"
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

// fakeMarket fills the app's market store with sessions ending the day before
// the test clock, and keeps Nasdaq's list beside it, fresh, so nothing is
// read from the network. Each symbol trades at 100 on a million shares a
// day, with a small daily wobble; moves are the last session's, as fractions,
// on five times the usual trading.
func fakeMarket(t *testing.T, a *App, sessions int, listings []market.Listing, moves map[string]float64, trend map[string]float64) {
	t.Helper()
	a.MarketStore = &market.Store{Dir: a.marketDir()}
	last := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	var days []time.Time
	for day := last; len(days) < sessions; day = day.AddDate(0, 0, -1) {
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday {
			days = append([]time.Time{day}, days...)
		}
	}
	symbols := []string{market.Benchmark}
	for _, l := range listings {
		symbols = append(symbols, l.Symbol)
	}
	for i, day := range days {
		bars := map[string]market.Bar{}
		for _, sym := range symbols {
			price := 100 * math.Pow(1+trend[sym], float64(i)) * (1 + 0.01*math.Sin(float64(i)))
			volume := 1e6
			if move, ok := moves[sym]; ok && i == len(days)-1 {
				prev := 100 * math.Pow(1+trend[sym], float64(i-1)) * (1 + 0.01*math.Sin(float64(i-1)))
				price, volume = prev*(1+move), 5e6
			}
			bars[sym] = market.Bar{Open: price, Close: price, Volume: volume}
		}
		if err := a.MarketStore.Save(day, day.Add(20*time.Hour), bars); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(listingsCache{Fetched: a.now(), Listings: listings})
	if err := os.WriteFile(filepath.Join(a.marketDir(), "listings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// rambusMoved is a market in which Rambus rose 15% on the last session, on
// five times its usual trading, and the brief carried why.
func rambusMoved(t *testing.T, a *App, verdicts stubCompleter) {
	t.Helper()
	fakeMarket(t, a, 70, []market.Listing{
		{Symbol: "RMBS", Name: "Rambus Inc. Common Stock", MarketCap: 8e9, Industry: "Semiconductors"},
		{Symbol: "CALM", Name: "Calm Holdings Inc. Common Stock", MarketCap: 8e9},
	}, map[string]float64{"RMBS": 0.15}, nil)
	a.Judge = &ideas.Judge{Completer: verdicts}
}

var todaysBrief = model.Report{
	Overview: "Rambus won an HBM order [1].",
	Cited: []model.Article{{ID: "a1", Title: "Rambus soars on HBM win", URL: "https://example.com/1",
		Published: time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC)}},
}

const rambusBuy = "=== RMBS\nVERDICT: BUY\nCONFIDENCE: medium\nCHANGED: A $200m order, a tenth of a year's sales [1].\nREACTION: Underreacted: the order is worth more.\nCASE: HBM demand runs through it [1].\nNUMBERS: 38x earnings\nRISK: One customer is a fifth of sales."

// A brief asked for with /now is a check, so its closer look stays with the
// owner -- and /share cannot pass it on either, since /share posts the last
// brief or analysis and a verdict is neither.
func TestACloserLookNotSharedStaysWithTheOwner(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	rambusMoved(t, a, stubCompleter{reply: rambusBuy})

	a.remember("the brief of Thu 10 Sep", []string{"the brief"})
	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false, false))

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	// No accounts could be read here, so the verdict is held to low
	// confidence whatever it said.
	for _, want := range []string{"Reacting to the news", "Rambus", "RMBS", "<b>BUY</b>", "low confidence", "underreacted"} {
		if !strings.Contains(owner, want) {
			t.Errorf("the owner's closer look is missing %q:\n%s", want, owner)
		}
	}
	if strings.Contains(owner, "Calm") {
		t.Errorf("a share that did not move was judged:\n%s", owner)
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
	rambusMoved(t, a, stubCompleter{reply: rambusBuy})

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, true, false))

	channel := strings.Join(messagesTo(*sent, testChannel), "\n")
	for _, want := range []string{"Reacting to the news", "Rambus", "<b>BUY</b>", "not financial advice", "nobody checking its work", "someone licensed"} {
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

// A followed company is the brief's to cover, and a move no article explains
// cannot be judged against its news: neither is sent for a verdict.
func TestFollowedAndUnexplainedMovesAreNotJudged(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.prefs.Groups = []model.Group{{ID: "semis", Name: "Semis", Companies: []model.Company{{Symbol: "RMBS", Name: "Rambus"}}}}
	fakeMarket(t, a, 70, []market.Listing{
		{Symbol: "RMBS", Name: "Rambus Inc. Common Stock", MarketCap: 8e9},
		{Symbol: "QUIET", Name: "Quiet Industries Inc. Common Stock", MarketCap: 8e9},
	}, map[string]float64{"RMBS": 0.15, "QUIET": -0.12}, nil)
	var calls []string
	a.Judge = &ideas.Judge{Completer: askedFor{reply: rambusBuy, mu: &sync.Mutex{}, calls: &calls}}

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false, false))

	if len(calls) != 0 || len(*sent) != 0 {
		t.Errorf("verdicts asked for %q, sent %+v", calls, *sent)
	}
}

// A reaction that matched its news is a HOLD, and a day of them sends
// nothing, rather than a heading over an empty list.
func TestADayOfMatchedMovesSendsNothing(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	rambusMoved(t, a, stubCompleter{reply: "=== RMBS\nVERDICT: HOLD\nCONFIDENCE: medium\nREACTION: Matched."})

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false, false))

	if len(*sent) != 0 {
		t.Errorf("sent %+v with nothing to show", *sent)
	}
}

// A failed verdict costs the section and nothing else: the brief has already
// gone, and nobody is sent a half-finished list.
func TestFailedVerdictsSendNothing(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	rambusMoved(t, a, stubCompleter{err: errors.New("claude: timed out")})

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false, false))

	if len(*sent) != 0 {
		t.Errorf("sent %+v after the verdicts failed", *sent)
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

	idea, sh := a.ideaFacts(context.Background(), model.Idea{
		Name: "Tencent", Ticker: "700", Exchange: "HK", Listed: "TENCENT HOLDINGS LTD",
	})
	chart, facts := sh.chart, sh.facts

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

	idea, _ := a.ideaFacts(context.Background(), model.Idea{
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

// The daily brief is sent at once and its closer look LookDelay later: queued on
// the data volume, where a restart in the wait does not lose it, and sent to
// the owner and the channel once it falls due.
func TestTheDailyCloserLookFollowsTheBriefAfterItsDelay(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	now := a.Now()

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Rambus soars on HBM win</title><link>https://feed.example/rmbs</link>
<description>Rambus won an order.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	defer feedSrv.Close()
	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nRambus soared on an HBM order [1].\n"}}
	rambusMoved(t, a, stubCompleter{reply: rambusBuy})

	if err := a.publishScheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messagesTo(*sent, 4242), "\n"); !strings.Contains(got, "Rambus soared") || strings.Contains(got, "Reacting to the news") {
		t.Fatalf("after the brief the owner has:\n%s", got)
	}
	lk, err := a.pendingLook()
	if err != nil || lk == nil || !lk.Due.Equal(now.Add(LookDelay)) || !lk.Share || !lk.Scheduled || len(lk.Cited) != 1 {
		t.Fatalf("queued %+v (err %v), want the brief's look due after LookDelay, for the channel too", lk, err)
	}

	// Not yet due: nothing is sent, and the wait is until it is, or the poll.
	a.Now = func() time.Time { return now.Add(LookDelay - time.Minute) }
	if wait := a.sendDueLook(context.Background()); wait != lookPoll {
		t.Errorf("a minute early, wait %v", wait)
	}
	a.Now = func() time.Time { return now.Add(LookDelay + time.Minute) }
	a.sendDueLook(context.Background())

	for _, chat := range []int64{4242, testChannel} {
		if got := strings.Join(messagesTo(*sent, chat), "\n"); !strings.Contains(got, "Reacting to the news") || !strings.Contains(got, "Rambus") {
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
	rambusMoved(t, a, stubCompleter{reply: rambusBuy})
	lk := lookFrom(todaysBrief, false, false)
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
