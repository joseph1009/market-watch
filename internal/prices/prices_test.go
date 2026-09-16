package prices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{APIKey: "test", HTTP: srv.Client(), URL: srv.URL, Pause: time.Millisecond}
}

func TestFetchReadsQuotes(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"c":212.17,"d":1.21,"dp":0.5736,"h":213.94,"l":211.16,"pc":210.96,"t":1789502400}`))
	})

	got, missed := c.Fetch(context.Background(), []string{"NVDA"})
	if len(missed) != 0 {
		t.Fatalf("missed %v", missed)
	}
	if len(got) != 1 || got[0].Symbol != "NVDA" || got[0].Price != 212.17 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Move() != "+0.6%" {
		t.Errorf("Move = %q, want +0.6%%", got[0].Move())
	}
	if got[0].AsOf.IsZero() {
		t.Error("the quote carries no timestamp")
	}
}

// Finnhub answers an unknown symbol with zeros rather than an error. A price of
// nothing is never right, and showing it would be worse than saying nothing.
func TestAQuoteOfZerosIsNotAPrice(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"c":0,"d":0,"dp":0,"h":0,"l":0,"pc":0,"t":0}`))
	})

	got, missed := c.Fetch(context.Background(), []string{"NOSUCH"})
	if len(got) != 0 {
		t.Errorf("a zero quote was kept: %+v", got)
	}
	if len(missed) != 1 || missed[0] != "NOSUCH" {
		t.Errorf("missed = %v, want the symbol reported", missed)
	}
}

// One symbol failing must not cost the rest of the list.
func TestOneFailureDoesNotStopTheRest(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("symbol") == "BAD" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"c":100,"d":-1,"dp":-1,"pc":101,"t":1789502400}`))
	})

	got, missed := c.Fetch(context.Background(), []string{"AAA", "BAD", "CCC"})
	if len(got) != 2 {
		t.Errorf("got %d quotes, want the two that worked", len(got))
	}
	if len(missed) != 1 || missed[0] != "BAD" {
		t.Errorf("missed = %v, want only BAD", missed)
	}
}

func TestMoveReadsAsAPersonWouldSayIt(t *testing.T) {
	tests := map[float64]string{
		1.42:  "+1.4%",
		-0.64: "-0.6%",
		0.01:  "flat",
		-0.02: "flat",
	}
	for percent, want := range tests {
		if got := (model.Quote{Percent: percent}).Move(); got != want {
			t.Errorf("Move(%.2f) = %q, want %q", percent, got, want)
		}
	}
}

// The rate limit is the scarce resource, so it is spent on the companies the
// day's news is actually about.
func TestMentionedPicksTheWatchedTickersInTheNews(t *testing.T) {
	articles := []model.Article{
		{Tickers: []string{"NVDA", "AMD"}},
		{Tickers: []string{"NVDA"}},
		{Tickers: []string{"TSLA"}},
	}

	got := Mentioned(articles, []string{"NVDA", "AMD", "MSFT"})
	if len(got) != 2 || got[0] != "NVDA" || got[1] != "AMD" {
		t.Errorf("got %v, want the watched tickers with news, once each", got)
	}
}

func TestDisabledWithoutAKey(t *testing.T) {
	var c *Client
	if c.Enabled() {
		t.Error("a nil client reports itself enabled")
	}
	if got, _ := (&Client{}).Fetch(context.Background(), []string{"NVDA"}); got != nil {
		t.Errorf("fetched %v without a key", got)
	}
}
