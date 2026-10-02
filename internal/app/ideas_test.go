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
	"github.com/joseph1009/market-watch/internal/pages"
	"github.com/joseph1009/market-watch/internal/prices"
	"github.com/joseph1009/market-watch/internal/report"
	"github.com/joseph1009/market-watch/internal/telegram"
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
	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

	handle(a, message("/share"))
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

	look := a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))
	a.shareBrief(context.Background(), a.remember("the brief of Thu 10 Sep", []string{"one"}), look)

	channel := strings.Join(messagesTo(*sent, testChannel), "\n")
	for _, want := range []string{"Reacting to the news", "Rambus", "<b>BUY</b>", "AI-written, unchecked, not advice."} {
		if !strings.Contains(channel, want) {
			t.Errorf("the channel's closer look is missing %q:\n%s", want, channel)
		}
	}
	// The owner's copy is headed for the owner, not with the reader's warning.
	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	if !strings.Contains(owner, "at least 5 percentage points over 12 months") || strings.Contains(owner, "not advice") {
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

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

	a.sendIdeas(context.Background(), lookFrom(todaysBrief, false))

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

	handle(a, message("/scorecard"))

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

// publishRambus runs the daily brief on a day Rambus moved, with pages on or
// off.
func publishRambus(t *testing.T, withPages bool) (*App, *[]sentMessage, *keptPages) {
	t.Helper()
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	kept := &keptPages{}
	if withPages {
		a.Pages = kept
	}

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Stub</title>
<item><title>Rambus soars on HBM win</title><link>https://feed.example/rmbs</link>
<description>Rambus won an order.</description><pubDate>Thu, 10 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`))
	}))
	t.Cleanup(feedSrv.Close)
	a.prefs.Sources = []model.Source{{ID: "stub-feed", Name: "Stub", URL: feedSrv.URL, Weight: 8, Enabled: true}}
	a.Fetcher = &feed.Fetcher{Client: feedSrv.Client(), Now: a.Now}
	a.Generator = &report.Generator{Completer: briefStub{reply: "## OVERVIEW\nRambus soared on an HBM order [1].\n\n## IN SHORT\n- Rambus soared on an HBM order.\n"}}
	rambusMoved(t, a, stubCompleter{reply: rambusBuy})

	if err := a.publishScheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, sent, kept
}

// The daily run gives the owner the brief and then the closer look, and the
// channel the two as one post, once the closer look is done: both summaries,
// a button to each one's page, and the closer look's warning (2026-10-01).
func TestTheChannelGetsTheBriefAndCloserLookAsOnePost(t *testing.T) {
	_, sent, kept := publishRambus(t, true)

	owner := messagesTo(*sent, 4242)
	if len(owner) != 2 || !strings.Contains(owner[0], "Rambus soared") || !strings.Contains(owner[1], "Reacting to the news") {
		t.Fatalf("the owner has %q, want the brief and then the closer look", owner)
	}

	var posts []sentMessage
	for _, m := range *sent {
		if m.ChatID == testChannel {
			posts = append(posts, m)
		}
	}
	if len(posts) != 1 {
		t.Fatalf("the channel got %d posts, want one", len(posts))
	}
	post := posts[0]
	for _, want := range []string{"Rambus soared", "Reacting to the news", "<b>BUY</b>", "AI-written, unchecked, not advice."} {
		if !strings.Contains(post.Text, want) {
			t.Errorf("the post is missing %q:\n%s", want, post.Text)
		}
	}
	if strings.Index(post.Text, "Rambus soared") > strings.Index(post.Text, "Reacting to the news") {
		t.Errorf("the closer look comes before the brief:\n%s", post.Text)
	}
	rows := post.ReplyMarkup.Rows
	if len(rows) != 2 || !strings.Contains(rows[0][0].Text, "brief") || !strings.Contains(rows[1][0].Text, "cases") {
		t.Fatalf("buttons = %+v, want the brief's then the closer look's", rows)
	}
	if rows[0][0].URL == rows[1][0].URL {
		t.Errorf("both buttons open %s", rows[0][0].URL)
	}
	// The owner's two pages, then the channel's two.
	if len(kept.pages) != 4 || !strings.Contains(pages.Fragment(kept.pages[3]), "AI-written, unchecked, not advice.") {
		t.Errorf("the channel's closer look page does not carry its note: %d pages", len(kept.pages))
	}
}

// Without pages the channel gets what it always did: the brief in full, then
// the closer look in full.
func TestWithoutPagesTheChannelGetsBothInFull(t *testing.T) {
	_, sent, _ := publishRambus(t, false)

	channel := messagesTo(*sent, testChannel)
	joined := strings.Join(channel, "\n")
	if !strings.Contains(joined, "Rambus soared") || !strings.Contains(joined, "AI-written, unchecked, not advice.") {
		t.Fatalf("the channel got:\n%s", joined)
	}
	if strings.Contains(channel[0], "Reacting to the news") {
		t.Errorf("the closer look came first:\n%s", joined)
	}
	for _, m := range *sent {
		if len(m.ReplyMarkup.Rows) > 0 {
			t.Errorf("a button with pages off: %+v", m)
		}
	}
}

// Summaries too long for one message together go as two posts, each with
// its own button, rather than one cut short.
func TestSummariesTooLongTogetherGoAsTwoPosts(t *testing.T) {
	a, sent := newTestApp(t)
	a.Pages = &keptPages{}
	long := strings.Repeat("word ", 500)
	brief := outgoing{title: "Brief", messages: []string{"brief"}, summary: telegram.Summary{Text: "brief " + long, Button: "Read the brief"}}
	look := outgoing{title: "Look", messages: []string{"look"}, summary: telegram.Summary{Text: "look " + long, Button: "Read the cases"}}

	if _, err := a.sendTogether(context.Background(), testChannel, brief, look); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 2 || len((*sent)[0].ReplyMarkup.Rows) != 1 || len((*sent)[1].ReplyMarkup.Rows) != 1 {
		t.Fatalf("sent %d posts, want two with a button each", len(*sent))
	}
	if !strings.HasPrefix((*sent)[0].Text, "brief") {
		t.Errorf("the brief did not go first")
	}
}

// A /share while the closer look was being researched posted the brief by
// itself. The closer look then goes alone, not with a second brief.
func TestACloserLookAfterAnEarlyShareGoesAlone(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.Cfg.TelegramChannelID = testChannel
	d := a.remember("the brief of Thu 10 Sep", []string{"the brief"})
	handle(a, message("/share"))
	*sent = nil

	a.shareBrief(context.Background(), d, &outgoing{messages: []string{"the closer look"}})

	if got := messagesTo(*sent, testChannel); len(got) != 1 || got[0] != "the closer look" {
		t.Errorf("the channel got %q, want the closer look alone", got)
	}
	if got := messagesTo(*sent, 4242); len(got) != 0 {
		t.Errorf("the owner was told %q", got)
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
