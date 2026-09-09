package feed

import (
	"context"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/model"
)

// candidate is a feed being evaluated before it earns a place in defaults.go.
type candidate struct {
	category string
	id       string
	url      string
}

// candidates are the feeds proposed to broaden coverage. Checking them through
// the real fetcher answers the question that matters -- does this parse with
// our code -- rather than merely whether the host returns 200.
var candidates = []candidate{
	// Primary: SEC per-company, filtered by CIK. The default 8-K feed is the
	// unfiltered "getcurrent" firehose; these return one company's filings.
	{"sec-company", "sec-nvda", "https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=0001045810&type=8-K&dateb=&owner=include&count=10&output=atom"},
	{"sec-company", "sec-aapl", "https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=0000320193&type=8-K&dateb=&owner=include&count=10&output=atom"},
	{"sec-company", "sec-nvda-all", "https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=0001045810&type=&dateb=&owner=include&count=20&output=atom"},

	// Primary: macro. press_monetary is the narrow cut of the Fed feed we
	// already have -- press_all mixes in enforcement actions the model called
	// out as having "no market read".
	{"macro", "fed-monetary", "https://www.federalreserve.gov/feeds/press_monetary.xml"},
	{"macro", "fed-speeches", "https://www.federalreserve.gov/feeds/speeches.xml"},
	{"macro", "fed-testimony", "https://www.federalreserve.gov/feeds/testimony.xml"},
	{"macro", "bls-releases", "https://www.bls.gov/feed/bls_latest.rss"},
	{"macro", "bea-news", "https://apps.bea.gov/rss/rss.xml"},
	// home.treasury.gov answered 404 or hung on every path tried
	// (/rss/press.xml, /news/press-releases/{feed,rss}, /system/feeds/...).
	// Kept as a record so the next attempt starts somewhere new.
	{"macro", "treasury-press", "https://home.treasury.gov/news/press-releases/feed"},

	// Geopolitics and export controls.
	{"geopolitics", "federal-register-export", "https://www.federalregister.gov/api/v1/documents.rss?conditions%5Bterm%5D=export+control"},
	{"geopolitics", "ustr", "https://ustr.gov/rss.xml"},
	{"geopolitics", "state-press", "https://www.state.gov/rss-feed/press-releases/feed/"},

	// Company IR / newsroom.
	{"company-ir", "nvidia-news", "https://nvidianews.nvidia.com/releases.xml"},
	{"company-ir", "apple-newsroom", "https://www.apple.com/newsroom/rss-feed.rss"},
	{"company-ir", "microsoft-news", "https://news.microsoft.com/feed/"},
	{"company-ir", "google-blog", "https://blog.google/rss/"},
	{"company-ir", "meta-news", "https://about.fb.com/news/feed/"},
	{"company-ir", "intel-news", "https://newsroom.intel.com/feed/"},
	{"company-ir", "amd-news", "https://ir.amd.com/rss/news-releases.xml"},

	// Semiconductor and tech trade press.
	{"semis-press", "tomshardware", "https://www.tomshardware.com/feeds/all"},
	{"semis-press", "semianalysis", "https://semianalysis.com/feed/"},
	{"semis-press", "eetimes", "https://www.eetimes.com/feed/"},
	{"semis-press", "theregister", "https://www.theregister.com/headlines.atom"},

	// Additional market news, to widen corroboration.
	// CNBC's feed ids do not mean what their names suggest: 20910258 is already
	// our markets feed, 10000664 returns the same midday-movers copy, and
	// 10001147 ("earnings") returned sports coverage. Only the tech id checked
	// out, and all four overlap heavily with feeds already enabled.
	{"news", "cnbc-tech", "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=19854910"},
	{"news", "seekingalpha", "https://seekingalpha.com/feed.xml"},
	{"news", "ap-business", "https://apnews.com/hub/business.rss"},
	{"news", "ft-home", "https://www.ft.com/rss/home"},
	{"news", "businesswire-tech", "https://feed.businesswire.com/rss/home/?rss=G1QFDERJXkJeEFpRVQ=="},
}

// TestCandidateFeeds is a survey, not an assertion: it reports what each
// proposed feed does so the results can be read before anything is added to
// the defaults. It never fails on a dead candidate -- that is the finding.
//
//	MARKET_WATCH_CANDIDATES=1 go test ./internal/feed -run TestCandidateFeeds -v
func TestCandidateFeeds(t *testing.T) {
	if os.Getenv("MARKET_WATCH_CANDIDATES") == "" {
		t.Skip("set MARKET_WATCH_CANDIDATES=1 to survey proposed feeds")
	}
	if err := config.LoadDotEnv("../../.env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sources := make([]model.Source, len(candidates))
	for i, c := range candidates {
		sources[i] = model.Source{ID: c.id, Name: c.id, URL: c.url}
	}

	concurrency := 4 // gentle: SEC publishes a rate limit and others throttle
	timeout := 25 * time.Second
	// A timeout under concurrency says little about a feed. Rechecking one at a
	// time, with more patience, separates a slow host from a dead one.
	if os.Getenv("MARKET_WATCH_CANDIDATES_SLOW") != "" {
		concurrency, timeout = 1, 60*time.Second
	}

	fetcher := &Fetcher{
		Client:      &http.Client{Timeout: timeout},
		UserAgent:   os.Getenv("USER_AGENT"),
		Concurrency: concurrency,
	}

	articles, errs := fetcher.Fetch(ctx, sources)

	counts := make(map[string]int, len(sources))
	sample := make(map[string]string, len(sources))
	for _, a := range articles {
		counts[a.SourceID]++
		if sample[a.SourceID] == "" {
			sample[a.SourceID] = a.Title
		}
	}
	failed := make(map[string]string, len(errs))
	for _, e := range errs {
		failed[e.SourceID] = e.Err.Error()
	}

	var working, broken, empty []string
	for _, c := range candidates {
		switch {
		case failed[c.id] != "":
			broken = append(broken, c.category+"/"+c.id+": "+failed[c.id])
		case counts[c.id] == 0:
			empty = append(empty, c.category+"/"+c.id)
		default:
			working = append(working, c.category+"/"+c.id)
			t.Logf("OK   %-14s %-24s %3d items | %s", c.category, c.id, counts[c.id], truncate(sample[c.id], 70))
		}
	}
	sort.Strings(broken)
	for _, b := range broken {
		t.Logf("FAIL %s", b)
	}
	for _, e := range empty {
		t.Logf("EMPTY %s (parsed, no items)", e)
	}
	t.Logf("summary: %d working, %d broken, %d empty, of %d candidates",
		len(working), len(broken), len(empty), len(candidates))
}
