package consensus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListingsReadTheScreener(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/screener/stocks" || r.URL.Query().Get("download") != "true" {
			t.Errorf("asked for %s", r.URL)
		}
		w.Write([]byte(`{"data":{"rows":[
			{"symbol":"A","name":"Agilent Technologies Inc. Common Stock","marketCap":"48728583125.00","country":"United States","industry":"Biotechnology: Laboratory Analytical Instruments","sector":"Industrials"},
			{"symbol":"BRK/B","name":"Berkshire Hathaway Inc.","marketCap":"1,000,000,000,000","country":"United States","industry":"Property-Casualty Insurers","sector":"Finance"},
			{"symbol":"NEW","name":"Just Listed","marketCap":"","country":"","industry":"","sector":""}]}}`))
	}))
	defer srv.Close()

	got, err := (&Client{HTTP: srv.Client(), BaseURL: srv.URL}).Listings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].MarketCap != 48728583125 || got[0].Industry != "Biotechnology: Laboratory Analytical Instruments" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Symbol != "BRK.B" || got[1].MarketCap != 1e12 {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].MarketCap != 0 {
		t.Errorf("an empty value = %v", got[2].MarketCap)
	}
}
