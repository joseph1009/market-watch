package config

import "github.com/joseph1009/market-watch/internal/model"

// DefaultPrefs seeds a fresh install. Weight ranks sources when the same story
// appears in several: primary filings and central-bank releases outrank wire
// coverage, which outranks aggregators and commentary.
func DefaultPrefs() *Prefs {
	return &Prefs{
		Sources: []model.Source{
			{ID: "sec-8k", Name: "SEC EDGAR 8-K", Weight: 10, Enabled: true,
				URL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&company=&dateb=&owner=include&count=40&output=atom"},
			// Disabled 2026-09-10 in favour of the three narrow Fed feeds below.
			// press_all mixes monetary policy with enforcement actions and
			// application approvals; the summarizer read a day of it and wrote
			// "routine enforcement actions and application approvals with no
			// market read", which is tokens spent to say nothing.
			{ID: "fed-press", Name: "Federal Reserve Press Releases", Weight: 10, Enabled: false,
				URL: "https://www.federalreserve.gov/feeds/press_all.xml"},
			{ID: "fed-monetary", Name: "Federal Reserve Monetary Policy", Weight: 10, Enabled: true,
				URL: "https://www.federalreserve.gov/feeds/press_monetary.xml"},
			// Speeches and testimony are where officials say what they think
			// before it reaches the wires. The briefs have been quoting Waller
			// and Hammack secondhand through CNBC; this is the primary text.
			{ID: "fed-speeches", Name: "Federal Reserve Speeches", Weight: 9, Enabled: true,
				URL: "https://www.federalreserve.gov/feeds/speeches.xml"},
			{ID: "fed-testimony", Name: "Federal Reserve Testimony", Weight: 9, Enabled: true,
				URL: "https://www.federalreserve.gov/feeds/testimony.xml"},
			{ID: "bea-news", Name: "Bureau of Economic Analysis", Weight: 10, Enabled: true,
				URL: "https://apps.bea.gov/rss/rss.xml"},
			{ID: "ustr", Name: "US Trade Representative", Weight: 9, Enabled: true,
				URL: "https://ustr.gov/rss.xml"},
			{ID: "cnbc-top", Name: "CNBC Top News", Weight: 8, Enabled: true,
				URL: "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=100003114"},
			{ID: "cnbc-markets", Name: "CNBC Markets", Weight: 8, Enabled: true,
				URL: "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=20910258"},
			{ID: "yahoo-finance", Name: "Yahoo Finance", Weight: 6, Enabled: true,
				URL: "https://finance.yahoo.com/news/rssindex"},
			{ID: "marketwatch-top", Name: "MarketWatch Top Stories", Weight: 6, Enabled: true,
				URL: "https://feeds.marketwatch.com/marketwatch/topstories/"},
			// Disabled 2026-09-09: the endpoint now sits behind bot protection and
			// resets the connection mid-stream for any non-browser client. Kept
			// here so it can be switched back on if that changes.
			{ID: "nasdaq-markets", Name: "Nasdaq Markets", Weight: 5, Enabled: false,
				URL: "https://www.nasdaq.com/feed/rssoutbound?category=Markets"},
			{ID: "investing-news", Name: "Investing.com News", Weight: 4, Enabled: true,
				URL: "https://www.investing.com/rss/news.rss"},
			{ID: "cnbc-tech", Name: "CNBC Technology", Weight: 8, Enabled: true,
				URL: "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=19854910"},

			// Company newsrooms, chosen individually rather than as a category:
			// these three publish business and product news, while Apple's,
			// Google's and Microsoft's newsroom feeds carry consumer marketing
			// ("new football features in Search") that would only add noise.
			{ID: "nvidia-news", Name: "NVIDIA Newsroom", Weight: 9, Enabled: true,
				URL: "https://nvidianews.nvidia.com/releases.xml"},
			{ID: "amd-news", Name: "AMD Newsroom", Weight: 9, Enabled: true,
				URL: "https://ir.amd.com/rss/news-releases.xml"},
			{ID: "meta-news", Name: "Meta Newsroom", Weight: 9, Enabled: true,
				URL: "https://about.fb.com/news/feed/"},

			{ID: "semianalysis", Name: "SemiAnalysis", Weight: 7, Enabled: true,
				URL: "https://semianalysis.com/feed/"},
		},
		Groups: []model.Group{
			{
				ID:      "semis-ai",
				Name:    "Semiconductors & AI",
				Tickers: []string{"NVDA", "AMD", "AVGO", "TSM", "ASML", "MU", "INTC", "ARM"},
				// "Arm" is deliberately absent: as a name it matches the body
				// part in ordinary prose, which the uppercase ticker does not.
				Names: []string{
					"Nvidia", "Broadcom", "TSMC", "Taiwan Semiconductor",
					"Micron", "Intel", "GlobalFoundries", "Qualcomm", "OpenAI",
				},
				Keywords: []string{"semiconductor", "chipmaker", "AI chip", "GPU", "foundry", "data center"},
			},
			{
				ID:      "big-tech",
				Name:    "Big Tech",
				Tickers: []string{"AAPL", "MSFT", "GOOGL", "AMZN", "META", "TSLA", "NFLX"},
				Names: []string{
					"Apple", "Microsoft", "Alphabet", "Google", "Amazon",
					"Meta", "Facebook", "Instagram", "Tesla", "Netflix",
				},
				Keywords: []string{"antitrust", "cloud revenue", "earnings guidance"},
			},
			{
				ID:   "macro-rates",
				Name: "Macro & Rates",
				Keywords: []string{
					"Federal Reserve", "FOMC", "interest rate", "rate cut", "rate hike",
					"inflation", "CPI", "PCE", "jobs report", "nonfarm payrolls",
					"Treasury yield", "recession", "GDP",
				},
			},
		},
	}
}
