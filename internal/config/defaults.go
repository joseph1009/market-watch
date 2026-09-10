package config

import "github.com/joseph1009/market-watch/internal/model"

// DefaultPrefs seeds a fresh install.
//
// Weight ranks sources when the same story appears in several, and now also
// ranks what survives the cut: primary agency releases outrank wire coverage,
// which outranks aggregators and commentary.
//
// Every URL here was checked against the live network before being added. The
// ones that failed are recorded as disabled entries rather than deleted, so the
// next attempt starts from what is already known not to work.
func DefaultPrefs() *Prefs {
	return &Prefs{
		SchemaVersion: CurrentSchemaVersion,
		Sources: []model.Source{
			// --- Filings ----------------------------------------------------
			{ID: "sec-8k", Name: "SEC EDGAR 8-K", Weight: 10, Enabled: true,
				URL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&company=&dateb=&owner=include&count=40&output=atom"},
			{ID: "sec-press", Name: "SEC Press Releases", Weight: 9, Enabled: true,
				URL: "https://www.sec.gov/news/pressreleases.rss"},

			// --- Monetary policy --------------------------------------------
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

			// --- Economic data ----------------------------------------------
			// The BLS feeds are monthly by nature, so most days they carry
			// nothing inside the staleness window. That is correct: a CPI
			// release is news on release day and history a week later.
			{ID: "bea-news", Name: "Bureau of Economic Analysis", Weight: 10, Enabled: true,
				URL: "https://apps.bea.gov/rss/rss.xml"},
			{ID: "bls-cpi", Name: "BLS Consumer Prices", Weight: 10, Enabled: true,
				URL: "https://www.bls.gov/feed/cpi.rss"},
			{ID: "bls-employment", Name: "BLS Employment Situation", Weight: 10, Enabled: true,
				URL: "https://www.bls.gov/feed/empsit.rss"},
			{ID: "bls-ppi", Name: "BLS Producer Prices", Weight: 10, Enabled: true,
				URL: "https://www.bls.gov/feed/ppi.rss"},
			{ID: "bls-jolts", Name: "BLS Job Openings", Weight: 9, Enabled: true,
				URL: "https://www.bls.gov/feed/jolts.rss"},
			{ID: "census-indicators", Name: "Census Economic Indicators", Weight: 10, Enabled: true,
				URL: "https://www.census.gov/economic-indicators/indicator.xml"},

			// --- Trade, sanctions and export controls -----------------------
			{ID: "ustr", Name: "US Trade Representative", Weight: 9, Enabled: true,
				URL: "https://ustr.gov/rss.xml"},
			// Filtered to the issuing agency rather than a keyword search: a
			// term search on "export control" returned mostly paperwork
			// notices, while the agency's own docket is the action itself.
			{ID: "fedreg-bis", Name: "Federal Register: Export Controls", Weight: 10, Enabled: true,
				URL: "https://www.federalregister.gov/api/v1/documents.rss?conditions%5Bagencies%5D%5B%5D=industry-and-security-bureau"},
			{ID: "fedreg-ofac", Name: "Federal Register: Sanctions", Weight: 10, Enabled: true,
				URL: "https://www.federalregister.gov/api/v1/documents.rss?conditions%5Bagencies%5D%5B%5D=foreign-assets-control-office"},

			// --- Energy ------------------------------------------------------
			{ID: "eia-today", Name: "EIA Today in Energy", Weight: 10, Enabled: true,
				URL: "https://www.eia.gov/rss/todayinenergy.xml"},
			{ID: "eia-press", Name: "EIA Press Releases", Weight: 10, Enabled: true,
				URL: "https://www.eia.gov/rss/press_rss.xml"},
			{ID: "oilprice", Name: "OilPrice.com", Weight: 5, Enabled: true,
				URL: "https://oilprice.com/rss/main"},

			// --- Healthcare ---------------------------------------------------
			{ID: "fda-press", Name: "FDA Press Releases", Weight: 10, Enabled: true,
				URL: "https://www.fda.gov/about-fda/contact-fda/stay-informed/rss-feeds/press-releases/rss.xml"},
			{ID: "fda-drugs", Name: "FDA Drug Updates", Weight: 9, Enabled: true,
				URL: "https://www.fda.gov/about-fda/contact-fda/stay-informed/rss-feeds/drugs/rss.xml"},
			{ID: "fda-medwatch", Name: "FDA Safety Alerts", Weight: 8, Enabled: true,
				URL: "https://www.fda.gov/about-fda/contact-fda/stay-informed/rss-feeds/medwatch/rss.xml"},

			// --- Industrials and defense ---------------------------------------
			// ContentType 400 is the daily contract award list; the general
			// newsroom types carry base life and exercises, which have no
			// market read.
			{ID: "dod-contracts", Name: "Defense Contract Awards", Weight: 9, Enabled: true,
				URL: "https://www.defense.gov/DesktopModules/ArticleCS/RSS.ashx?ContentType=400&Site=945&max=25"},
			{ID: "dod-releases", Name: "Defense Department Releases", Weight: 7, Enabled: true,
				URL: "https://www.defense.gov/DesktopModules/ArticleCS/RSS.ashx?ContentType=9&Site=945&max=25"},

			// --- Crypto -------------------------------------------------------
			{ID: "cftc-press", Name: "CFTC Press Releases", Weight: 8, Enabled: true,
				URL: "https://www.cftc.gov/RSS/RSSGP/rssgp.xml"},
			{ID: "coindesk", Name: "CoinDesk", Weight: 6, Enabled: true,
				URL: "https://www.coindesk.com/arc/outboundfeeds/rss/"},

			// --- Financial press ----------------------------------------------
			{ID: "cnbc-top", Name: "CNBC Top News", Weight: 8, Enabled: true,
				URL: "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=100003114"},
			{ID: "cnbc-markets", Name: "CNBC Markets", Weight: 8, Enabled: true,
				URL: "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=20910258"},
			{ID: "cnbc-tech", Name: "CNBC Technology", Weight: 8, Enabled: true,
				URL: "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=19854910"},
			{ID: "yahoo-finance", Name: "Yahoo Finance", Weight: 6, Enabled: true,
				URL: "https://finance.yahoo.com/news/rssindex"},
			{ID: "marketwatch-top", Name: "MarketWatch Top Stories", Weight: 6, Enabled: true,
				URL: "https://feeds.marketwatch.com/marketwatch/topstories/"},
			{ID: "investing-news", Name: "Investing.com News", Weight: 4, Enabled: true,
				URL: "https://www.investing.com/rss/news.rss"},
			// Disabled 2026-09-09: the endpoint now sits behind bot protection
			// and resets the connection mid-stream for any non-browser client.
			{ID: "nasdaq-markets", Name: "Nasdaq Markets", Weight: 5, Enabled: false,
				URL: "https://www.nasdaq.com/feed/rssoutbound?category=Markets"},

			// --- Company newsrooms ---------------------------------------------
			// Chosen individually rather than as a category: these three publish
			// business and product news, while Apple's, Google's and
			// Microsoft's newsroom feeds carry consumer marketing ("new
			// football features in Search") that would only add noise.
			{ID: "nvidia-news", Name: "NVIDIA Newsroom", Weight: 9, Enabled: true,
				URL: "https://nvidianews.nvidia.com/releases.xml"},
			{ID: "amd-news", Name: "AMD Newsroom", Weight: 9, Enabled: true,
				URL: "https://ir.amd.com/rss/news-releases.xml"},
			{ID: "meta-news", Name: "Meta Newsroom", Weight: 9, Enabled: true,
				URL: "https://about.fb.com/news/feed/"},

			{ID: "semianalysis", Name: "SemiAnalysis", Weight: 7, Enabled: true,
				URL: "https://semianalysis.com/feed/"},

			// --- Checked and unavailable ----------------------------------------
			// Recorded so the next attempt does not repeat the search. FDIC,
			// OCC, NIH, CMS, NHTSA, USDA, FAA, FERC, OPEC, IEA and Treasury all
			// answered 403 or 404 on every RSS path tried on 2026-09-10.
			// Autos and consumer therefore have no dedicated feed and depend on
			// the general financial press.
			{ID: "nhtsa", Name: "NHTSA (unavailable)", Weight: 8, Enabled: false,
				URL: "https://www.nhtsa.gov/rss/press-releases"},
			{ID: "fdic", Name: "FDIC (unavailable)", Weight: 9, Enabled: false,
				URL: "https://www.fdic.gov/news/press-releases/rss.xml"},
			{ID: "treasury", Name: "US Treasury (unavailable)", Weight: 10, Enabled: false,
				URL: "https://home.treasury.gov/news/press-releases/feed"},
		},

		// Watchlists. Tickers match case-sensitively as standalone symbols,
		// names case-insensitively on word boundaries, keywords the same as
		// names. Keywords are kept deliberately specific: a broad word like
		// "bank" or "capital" matches most of the feed and stops the watchlist
		// meaning anything.
		Groups: []model.Group{
			{
				ID:   "semis-ai",
				Name: "Semiconductors & AI",
				Tickers: []string{
					"NVDA", "AMD", "AVGO", "TSM", "ASML", "MU", "INTC", "ARM",
					"QCOM", "MRVL", "AMAT", "LRCX", "KLAC", "GFS", "SMCI", "ANET",
				},
				// "Arm" is deliberately absent as a name: lowercased it matches
				// the body part in ordinary prose, which the symbol does not.
				Names: []string{
					"Nvidia", "Advanced Micro Devices", "Broadcom", "TSMC",
					"Taiwan Semiconductor", "Micron", "Intel", "GlobalFoundries",
					"Qualcomm", "Marvell", "Applied Materials", "Lam Research",
					"KLA", "Supermicro", "Arista", "SK Hynix", "Samsung", "OpenAI",
				},
				Keywords: []string{
					"semiconductor", "chipmaker", "AI chip", "GPU", "foundry",
					"data center", "HBM", "CoWoS", "advanced packaging", "EUV",
					"semiconductor equipment", "AI accelerator", "custom silicon",
					// "wafer yield", not "yield": bare "yield" collides with
					// Treasury yields and would tag every rates story.
					"wafer", "wafer yield", "chip capacity",
				},
			},
			{
				ID:      "big-tech",
				Name:    "Big Tech",
				Tickers: []string{"AAPL", "MSFT", "GOOGL", "AMZN", "META", "NFLX"},
				Names: []string{
					"Apple", "Microsoft", "Alphabet", "Google", "Amazon",
					"Meta", "Facebook", "Instagram", "Netflix",
				},
				Keywords: []string{"antitrust", "cloud revenue", "earnings guidance"},
			},
			{
				ID:      "energy",
				Name:    "Energy",
				Tickers: []string{"XOM", "CVX", "COP", "EOG", "OXY", "SLB", "HAL"},
				Names: []string{
					"Exxon", "ExxonMobil", "Chevron", "ConocoPhillips",
					"Occidental", "Schlumberger", "Halliburton", "Saudi Aramco",
				},
				Keywords: []string{
					"crude oil", "Brent", "WTI", "natural gas", "LNG", "OPEC",
					"refinery", "refining", "oil production", "drilling",
					"gasoline prices", "uranium", "oil inventories",
				},
			},
			{
				ID:   "financials",
				Name: "Financials & Banks",
				// C and V are Citigroup and Visa. They only match when written
				// as a symbol -- "$C" or "(NYSE: C)" -- because bare single
				// letters match C-suite and V-shaped recovery.
				Tickers: []string{"JPM", "BAC", "GS", "MS", "WFC", "BLK", "SCHW", "V", "C"},
				Names: []string{
					"JPMorgan", "Bank of America", "Goldman Sachs", "Morgan Stanley",
					"Wells Fargo", "BlackRock", "Charles Schwab", "Visa", "Citigroup", "Citi",
				},
				Keywords: []string{
					"net interest margin", "commercial real estate", "consumer credit",
					"loan losses", "bank earnings", "deposit growth", "Basel",
					"FDIC", "credit spreads", "yield curve",
				},
			},
			{
				ID:      "healthcare",
				Name:    "Healthcare & Pharma",
				Tickers: []string{"LLY", "JNJ", "UNH", "ABBV", "MRK", "PFE", "NVO", "ISRG"},
				Names: []string{
					"Eli Lilly", "Johnson & Johnson", "UnitedHealth", "AbbVie",
					"Merck", "Pfizer", "Novo Nordisk", "Intuitive Surgical", "Moderna",
				},
				Keywords: []string{
					"FDA approval", "drug approval", "clinical trial", "GLP-1",
					"obesity drug", "Medicare", "drug pricing", "biotech",
					"medical device", "phase 3", "recall",
				},
			},
			{
				ID:      "industrials-defense",
				Name:    "Industrials & Defense",
				Tickers: []string{"CAT", "DE", "GE", "HON", "RTX", "LMT", "BA", "UPS", "FDX", "NOC"},
				Names: []string{
					"Caterpillar", "Deere", "GE Aerospace", "Honeywell", "RTX",
					"Raytheon", "Lockheed Martin", "Northrop Grumman", "Boeing",
					"UPS", "FedEx",
				},
				Keywords: []string{
					"defense contract", "defense spending", "aerospace",
					"factory orders", "durable goods", "industrial production",
					"machinery orders", "freight volumes",
				},
			},
			{
				ID:      "consumer-retail",
				Name:    "Consumer & Retail",
				Tickers: []string{"WMT", "COST", "HD", "LOW", "MCD", "NKE", "SBUX", "KO", "PEP", "TGT"},
				Names: []string{
					"Walmart", "Costco", "Home Depot", "Lowe's", "McDonald's",
					"Nike", "Starbucks", "Coca-Cola", "PepsiCo", "Target",
				},
				Keywords: []string{
					"retail sales", "consumer spending", "consumer confidence",
					"same-store sales", "discretionary spending", "holiday sales",
				},
			},
			{
				ID:      "autos-ev",
				Name:    "Autos & EV",
				Tickers: []string{"TSLA", "GM", "RIVN", "LCID", "F"},
				// Tesla sits here rather than in Big Tech: it matches both, and
				// one section reads better than the same story told twice.
				Names: []string{"Tesla", "General Motors", "Ford", "Rivian", "Lucid", "BYD"},
				Keywords: []string{
					"electric vehicle", "EV sales", "vehicle sales", "auto production",
					"battery plant", "autonomous driving", "robotaxi", "auto loans",
				},
			},
			{
				ID:      "crypto",
				Name:    "Crypto & Digital Assets",
				Tickers: []string{"COIN", "MSTR", "MARA", "RIOT"},
				Names: []string{
					"Coinbase", "MicroStrategy", "Marathon Digital", "Riot Platforms", "Tether",
				},
				Keywords: []string{
					"Bitcoin", "Ethereum", "stablecoin", "digital asset",
					"crypto ETF", "crypto regulation", "blockchain", "spot ETF",
				},
			},
			{
				ID:   "macro-rates",
				Name: "Macro & Rates",
				Keywords: []string{
					"Federal Reserve", "FOMC", "interest rate", "rate cut", "rate hike",
					"inflation", "CPI", "PCE", "PPI", "JOLTS", "jobs report",
					"nonfarm payrolls", "Treasury yield", "recession", "GDP",
					"unemployment rate", "wage growth",
				},
			},
			{
				ID:   "geopolitics-trade",
				Name: "Geopolitics & Trade",
				// Themes only, no tickers. Listing the semiconductor names here
				// would tag every chip earnings story as geopolitics and make
				// this section a duplicate of Semiconductors & AI.
				Keywords: []string{
					"US-China", "Taiwan", "tariff", "tariffs", "export control",
					"export controls", "sanctions", "trade restrictions",
					"CHIPS Act", "entity list", "Section 301", "trade war",
					"supply chain", "Strait of Hormuz",
				},
			},
		},
	}
}
