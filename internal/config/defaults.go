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
			{ID: "fed-press", Name: "Federal Reserve Press Releases", Weight: 10, Enabled: true,
				URL: "https://www.federalreserve.gov/feeds/press_all.xml"},
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
		},
		Groups: []model.Group{
			{
				ID:       "semis-ai",
				Name:     "Semiconductors & AI",
				Tickers:  []string{"NVDA", "AMD", "AVGO", "TSM", "ASML", "MU", "INTC", "ARM"},
				Keywords: []string{"semiconductor", "chipmaker", "AI chip", "GPU", "foundry", "data center"},
			},
			{
				ID:       "big-tech",
				Name:     "Big Tech",
				Tickers:  []string{"AAPL", "MSFT", "GOOGL", "AMZN", "META", "TSLA", "NFLX"},
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
