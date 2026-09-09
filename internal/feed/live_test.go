package feed

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
)

// Feeds rot: they move, change format, or start refusing our User-Agent, and
// nothing in the unit tests would notice. This checks the real defaults against
// the real network, so it is opt-in rather than part of every run.
//
//	MARKET_WATCH_LIVE=1 go test ./internal/feed -run TestLive -v
func TestLiveDefaultSourcesStillParse(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to check the default feeds against the network")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sources := config.DefaultPrefs().EnabledSources()
	// SEC EDGAR needs a contact address, so the check runs with the same
	// USER_AGENT the deployment uses rather than the contact-free default.
	f := &Fetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: os.Getenv("USER_AGENT"),
	}

	articles, errs := f.Fetch(ctx, sources)
	for _, e := range errs {
		t.Errorf("source %s (%s) failed: %v", e.SourceID, e.SourceName, e.Err)
	}

	// Per-source yield matters more than the total: one prolific feed could
	// otherwise hide a source that now returns an empty document.
	counts := make(map[string]int, len(sources))
	for _, a := range articles {
		counts[a.SourceID]++
	}
	for _, s := range sources {
		if counts[s.ID] == 0 {
			t.Errorf("source %s (%s) parsed but yielded no articles", s.ID, s.Name)
			continue
		}
		t.Logf("%-16s %3d articles", s.ID, counts[s.ID])
	}
}
