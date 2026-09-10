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
//
// Official agency feeds are preferred over trade press throughout: a regulator
// announcing something is the event, where a publication writing about it is a
// report of the event.
var candidates = []candidate{
	// --- Energy ------------------------------------------------------------
	{"energy", "eia-today", "https://www.eia.gov/rss/todayinenergy.xml"},
	{"energy", "eia-press", "https://www.eia.gov/rss/press_rss.xml"},
	{"energy", "opec-press", "https://www.opec.org/opec_web/en/rss/press_releases.xml"},
	{"energy", "iea-news", "https://www.iea.org/rss/news"},
	{"energy", "oilprice", "https://oilprice.com/rss/main"},
	{"energy", "ferc-news", "https://www.ferc.gov/news-events/news/rss.xml"},

	// --- Financials & banks -------------------------------------------------
	{"financials", "fdic-press", "https://www.fdic.gov/news/press-releases/rss.xml"},
	{"financials", "occ-news", "https://www.occ.gov/rss/occ_media_releases.xml"},
	{"financials", "bis-press", "https://www.bis.org/list/press_rss.xml"},
	{"financials", "cfpb-news", "https://www.consumerfinance.gov/about-us/newsroom/feed/"},
	{"financials", "sec-press", "https://www.sec.gov/news/pressreleases.rss"},

	// --- Healthcare & pharma ------------------------------------------------
	{"healthcare", "fda-press", "https://www.fda.gov/about-fda/contact-fda/stay-informed/rss-feeds/press-releases/rss.xml"},
	{"healthcare", "fda-drugs", "https://www.fda.gov/about-fda/contact-fda/stay-informed/rss-feeds/drugs/rss.xml"},
	{"healthcare", "fda-medwatch", "https://www.fda.gov/about-fda/contact-fda/stay-informed/rss-feeds/medwatch/rss.xml"},
	{"healthcare", "nih-news", "https://www.nih.gov/news-events/news-releases/feed.xml"},
	{"healthcare", "cms-newsroom", "https://www.cms.gov/newsroom/rss.xml"},

	// --- Industrials & defense ----------------------------------------------
	{"industrials", "dod-contracts", "https://www.defense.gov/DesktopModules/ArticleCS/RSS.ashx?ContentType=800&Site=945&max=25"},
	{"industrials", "dod-releases", "https://www.defense.gov/DesktopModules/ArticleCS/RSS.ashx?ContentType=1&Site=945&max=25"},
	{"industrials", "census-indicators", "https://www.census.gov/economic-indicators/indicator.xml"},
	{"industrials", "faa-news", "https://www.faa.gov/newsroom/rss"},

	// --- Consumer & retail --------------------------------------------------
	{"consumer", "census-retail", "https://www.census.gov/economic-indicators/retail.xml"},
	{"consumer", "usda-news", "https://www.usda.gov/rss/latest-releases.xml"},

	// --- Autos & EV ---------------------------------------------------------
	{"autos", "nhtsa-press", "https://www.nhtsa.gov/rss/press-releases"},
	{"autos", "nhtsa-recalls", "https://www.nhtsa.gov/rss/recalls"},

	// --- Crypto & digital assets --------------------------------------------
	{"crypto", "coindesk", "https://www.coindesk.com/arc/outboundfeeds/rss/"},
	{"crypto", "cftc-press", "https://www.cftc.gov/RSS/RSSGP/rssgp.xml"},
	{"crypto", "sec-litigation", "https://www.sec.gov/rss/litigation/litreleases.xml"},

	// --- Geopolitics & trade ------------------------------------------------
	{"geopolitics", "commerce-bis", "https://www.bis.doc.gov/index.php/all-articles?format=feed&type=rss"},
	{"geopolitics", "commerce-news", "https://www.commerce.gov/feeds/news"},
	{"geopolitics", "federal-register-bis", "https://www.federalregister.gov/api/v1/documents.rss?conditions%5Bagencies%5D%5B%5D=industry-and-security-bureau"},
	{"geopolitics", "federal-register-ofac", "https://www.federalregister.gov/api/v1/documents.rss?conditions%5Bagencies%5D%5B%5D=foreign-assets-control-office"},

	// --- Macro, still missing a working Treasury feed -----------------------
	{"macro", "bls-cpi", "https://www.bls.gov/feed/cpi.rss"},
	{"macro", "bls-employment", "https://www.bls.gov/feed/empsit.rss"},
	{"macro", "bls-ppi", "https://www.bls.gov/feed/ppi.rss"},
	{"macro", "bls-jolts", "https://www.bls.gov/feed/jolts.rss"},
	{"macro", "treasury-press", "https://home.treasury.gov/news/press-releases/feed"},

	// --- International and Asia-facing press --------------------------------
	// The reader is in Singapore and the corpus is almost entirely US outlets,
	// so an Asian session or a European one reaches the brief only after a US
	// desk decides to cover it.
	{"world", "cna-latest", "https://www.channelnewsasia.com/api/v1/rss-outbound-feed?_format=xml"},
	{"world", "cna-business", "https://www.channelnewsasia.com/rssfeeds/8395954"},
	{"world", "straitstimes-business", "https://www.straitstimes.com/news/business/rss.xml"},
	{"world", "aljazeera", "https://www.aljazeera.com/xml/rss/all.xml"},
	{"world", "bbc-business", "https://feeds.bbci.co.uk/news/business/rss.xml"},
	{"world", "guardian-business", "https://www.theguardian.com/uk/business/rss"},
	{"world", "nikkei-asia", "https://asia.nikkei.com/rss/feed/nar"},
	{"world", "scmp-business", "https://www.scmp.com/rss/92/feed"},
	{"world", "dw-business", "https://rss.dw.com/rdf/rss-en-bus"},
	{"world", "france24-business", "https://www.france24.com/en/business/rss"},
	{"world", "skynews-business", "https://feeds.skynews.com/feeds/rss/business.xml"},
	{"world", "ft-home", "https://www.ft.com/rss/home"},
	{"world", "economist-finance", "https://www.economist.com/finance-and-economics/rss.xml"},
	{"world", "japantimes-business", "https://www.japantimes.co.jp/news_category/business/feed/"},

	// --- second-pass alternates for the highest-value misses ----------------
	{"financials", "fdic-alt", "https://www.fdic.gov/rss/press-releases.xml"},
	{"financials", "occ-alt", "https://www.occ.treas.gov/rss/occ_news_releases.xml"},
	{"financials", "fed-h8", "https://www.federalreserve.gov/feeds/h8.xml"},
	{"financials", "fed-enforcement", "https://www.federalreserve.gov/feeds/press_enforcement.xml"},
	{"industrials", "dod-contracts-alt", "https://www.defense.gov/DesktopModules/ArticleCS/RSS.ashx?ContentType=400&Site=945&max=25"},
	{"industrials", "dod-contracts-alt2", "https://www.defense.gov/DesktopModules/ArticleCS/RSS.ashx?ContentType=9&Site=945&max=25"},
	{"autos", "nhtsa-alt", "https://www.nhtsa.gov/rss.xml"},
	{"energy", "iea-alt", "https://www.iea.org/news/rss"},
	{"consumer", "census-alt", "https://www.census.gov/newsroom/press-releases.xml"},
	{"macro", "fed-h15", "https://www.federalreserve.gov/feeds/h15.xml"},
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

	ctx, cancel := context.WithTimeout(ctx(t), 5*time.Minute)
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
	newest := make(map[string]time.Time, len(sources))
	for _, a := range articles {
		counts[a.SourceID]++
		if sample[a.SourceID] == "" {
			sample[a.SourceID] = a.Title
		}
		if a.Published.After(newest[a.SourceID]) {
			newest[a.SourceID] = a.Published
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
			// Age matters as much as parsing: several agencies publish an
			// archive rather than a news feed, which is only visible here.
			age := time.Since(newest[c.id]).Round(time.Hour)
			t.Logf("OK   %-12s %-24s %3d items  newest %6s ago | %s",
				c.category, c.id, counts[c.id], age, truncate(sample[c.id], 62))
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

func ctx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}
