package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/prices"
)

// A new name in the news is priced wherever it is listed. The quote feed knows
// the US ones; the rest take the last close from the daily history, in the
// currency they trade in.
func TestEveryNewNameIsPricedWhereverItIsListed(t *testing.T) {
	quotes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"c":5.42,"d":0.45,"dp":9.1,"h":5.5,"l":4.9,"pc":4.97,"t":1789000000}`))
	}))
	t.Cleanup(quotes.Close)

	a, _ := newTestApp(t)
	a.Quotes = &prices.Client{APIKey: "test", HTTP: quotes.Client(), URL: quotes.URL, Pause: time.Millisecond}
	a.Market = chartServer(t, "HKD", 60)

	out := a.priceCandidates(context.Background(), []model.Candidate{
		{Name: "Grab", Ticker: "GRAB", Exchange: "US"},
		{Name: "Tencent", Ticker: "700", Exchange: "HK"},
		{Name: "Atome", Private: true},
	})

	if out[0].Quote == nil || out[0].Quote.Price != 5.42 {
		t.Errorf("the US name was not priced from the quote feed: %+v", out[0].Quote)
	}
	if out[1].Quote == nil {
		t.Fatal("the Hong Kong name came back with no price")
	}
	if out[1].Quote.Price != 159 {
		t.Errorf("price = %v, want the last close of 159", out[1].Quote.Price)
	}
	if out[1].Quote.Unit() != "HKD" {
		t.Errorf("the Hong Kong price is in %q, want HKD", out[1].Quote.Unit())
	}
	if out[2].Quote != nil {
		t.Errorf("a private company was given a price: %+v", out[2].Quote)
	}
}

// Without a Finnhub key the brief used to price nothing at all. The chart
// source needs no key, so the names listed elsewhere keep their move.
func TestNamesOutsideTheUSArePricedWithNoQuoteKey(t *testing.T) {
	a, _ := newTestApp(t)
	a.Quotes = &prices.Client{} // no key, as on a free run
	a.Market = chartServer(t, "JPY", 60)

	out := a.priceCandidates(context.Background(), []model.Candidate{
		{Name: "Toyota", Ticker: "7203", Exchange: "JP"},
		{Name: "Grab", Ticker: "GRAB", Exchange: "US"},
	})

	if out[0].Quote == nil {
		t.Fatal("the Tokyo name was not priced without a quote key")
	}
	if out[0].Quote.Unit() != "JPY" {
		t.Errorf("the Tokyo price is in %q, want JPY", out[0].Quote.Unit())
	}
	if out[1].Quote != nil {
		t.Errorf("a US name was priced with no key: %+v", out[1].Quote)
	}
}
