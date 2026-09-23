package search

import "testing"

// The addresses and titles are the ones the first brief written with search
// cited (2026-09-24), and the pages the earlier trial searches returned.
func TestIsStoryKeepsReportsAndDropsPagesAboutMany(t *testing.T) {
	tests := []struct {
		link, title string
		want        bool
	}{
		// Reports, in each outlet's address style.
		{"https://www.bloomberg.com/news/articles/2026-09-23/us-treasury-five-year-yields-breach-5-for-first-time-since-2007", "US Treasury Five-Year Yields Breach 5% for First Time Since 2007", true},
		{"https://www.reuters.com/business/finance/swiss-upper-house-backs-90-cet1-capital-backing-ubs-foreign-units-2026-09-23/", "Swiss upper house vote deals blow to UBS", true},
		{"https://www.ft.com/content/d1090476-7be6-4bba-ae2a-f417499e820a?syn-25a6b1a6=1", "Nvidia-backed Nscale buried ByteDance deal in push to $35bn IPO", true},
		{"https://www.bbc.com/news/articles/c6p3kxpp8lezo", "Trump reveals millions of dollars' worth of share deals", true},
		{"https://www.bbc.com/news/articles/cvgkqrzpwmxo", "An id with no digit in it", true},
		{"https://apnews.com/article/taiwan-military-drills-us-arms-sales-a6763308f8d63f3e569145e00595e9f2", "Taiwan is watching closely as Xi and Trump meet", true},
		{"https://www.barrons.com/articles/palantir-stock-ai-faa-air-traffic-control-373cce03", "Palantir Could Be Big Winner as Air Traffic Control Goes AI", true},
		// A clip about one story is a report.
		{"https://www.cnbc.com/video/2026/09/23/treasury-sells-70-billion-in-5-year-note.html", "Treasury sells $70 billion in 5-year note", true},
		// A headline that carries a date says more than the date.
		{"https://www.cnbc.com/video/2026/09/23/wednesday-september-23-2026-the-club.html", "Wednesday, September 23, 2026: The Club sees building momentum in industrials", true},

		// Pages about many.
		{"https://www.digitimes.com/topic/semiconductors", "DIGITIMES Semiconductors news", false},
		{"https://www.digitimes.com/topic/semiconductors/ic_manufacturing", "DIGITIMES Semiconductors: IC manufacturing news", false},
		{"https://www.marketwatch.com/investing/stock/tsm?eafs_enabled=false", "TSM Stock Price", false},
		{"https://www.reuters.com/markets/", "Markets", false},
		{"https://www.cnbc.com/", "CNBC", false},
		{"https://www.theguardian.com/business/live/2026/sep/23/oil-prices-fall-us-iran-talks-stock-markets-saudi-pipeline-latest", "UK economic outlook brighter as new government measures will", false},
		{"https://www.cnbc.com/quotes/NVDA", "NVDA: NVIDIA Corp", false},
		// A programme: a show's name and a date.
		{"https://www.cnbc.com/video/2026/09/23/post-market-wrap-september-23-2026.html", "Post Market Wrap: September 23, 2026", false},
		{"https://www.cnbc.com/video/2026/09/22/cnbc-markets-now-september-22-2026.html", "CNBC Markets Now: September 22, 2026", false},
		{"https://www.cnbc.com/2026/09/23/cctv-script-23/09/26.html", "CCTV Script 23/09/26", false},
	}
	for _, tt := range tests {
		if got := isStory(tt.link, tt.title); got != tt.want {
			t.Errorf("isStory(%s, %q) = %v, want %v", tt.link, tt.title, got, tt.want)
		}
	}
}

func TestIsProgrammeNeedsADate(t *testing.T) {
	for _, title := range []string{"Squawk Box", "Morning Bid", "Llama 3.1.2 released", "Fed holds"} {
		if isProgramme(title) {
			t.Errorf("isProgramme(%q) = true for a title with no date", title)
		}
	}
}
