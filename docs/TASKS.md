**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → **7 Backlog**

---

# Backlog

What is agreed but not built. Each entry says what it is, what it needs from
you, and why it is worth doing.

## Waiting on you

- **Push the repository.** Every commit is still local only. GitHub holds only
  an old first commit from before the history was regrouped, so the first push
  replaces it: `git push --force-with-lease`.
- **Judge Monday's run.** Everything built is deployed (27 September 2026).
  The first weekly themes on the new code run on Monday 28 September; read
  them with `scripts/sync-cache-from-fly.sh recommendations`.

## Reliability

- **Weekly source health.** A failed brief now messages you, and `/stats` names
  feeds that fail regularly, but nothing volunteers it: you have to ask. A
  weekly message would close that.
- **Decide on the media feeds after a fortnight of search.** Search runs beside
  the feeds, and `/stats` shows which cited stories it failed to find, by the
  source that carried them. If that list holds only government and company
  releases, the eighteen media feeds can be turned off, and with them the part
  of the source list that breaks. RUNBOOK.md, "News search", says how to read
  it.
- **`marketwatch-top` returns HTTP 400.** One feed of 44, failing consistently.
  Worth replacing or turning off.

## Brief quality

- **Judge the sorting and the review after a week.** The keywords are gone:
  an article reaches a section by naming a followed company, or because Opus
  read it against the sector's description in `config/sectors.yaml` and the
  review agreed. `/stats` reports how many were placed by judgment and how many
  the review moved, with five of each. Read those for a week. If the placements
  are stories you would want and the moves are corrections, the net is set
  right; if they are stretches, sharpen the descriptions, raise
  `MinPlacementRating`, or turn the review off with `REVIEW=false`. The same
  week says whether the top-up earns its place: `/stats` shows how many
  stories rated 3 were offered to thin sections, how many the review kept, and
  five of them. If what it keeps reads as padding, set `ThinSection` in
  `internal/triage/topup.go` to 0 and the thin sections go back to
  disappearing. Only once that is settled is it worth adding
  sector feeds -- chip trade press, energy and shipping, drug development,
  freight -- since a gate that works makes extra sources cheap and a gate that
  does not makes them mush. The test for any new feed is the one that condemned
  the 8-K firehose: after a fortnight, how many of its articles were placed, and
  how many were cited.
- **Prices outside the US, in the brief.** Done for the closer look. A listing
  on any of the checked exchanges now takes its price from the daily-history
  source -- Yahoo's charting endpoint, keyless, in the currency the share
  actually trades in -- with `prices.Latest` turning the last two closes into a
  day's move. The keyed vendors were checked against a live key first and are
  no use: Twelve Data's free tier answers Hong Kong with "available starting
  with the Pro or Venture plan", London with "the Grow or Venture plan", and
  does not resolve Tokyo or Singapore at all. `TWELVEDATA_API_KEY` was dropped
  rather than left as a promise the tier cannot keep.
  The new names now do the same and show the move beside the ticker. What is
  left is `collectPrices` in `internal/app/prices.go`, which reads US listings
  only: the quote feed, and the charts for whatever that misses. It prices
  every company followed, and today all 95 tickers trade in New York, so
  nothing is missing; but a Tokyo or London listing added to
  `config/companies.yaml` would go without a price, a moves line or a mover
  search. Giving companies an exchange and spelling it with `ChartSymbol`
  would fix it.
- **Judge the weekly themes after a month.** Read four weeks of them for
  whether the themes are ones the numbers really show, whether the early ones
  are backed by figures rather than talk, and whether the picks sit in the
  unpriced part of their theme or simply are its leaders. And what a Monday
  costs the plan: the `look` runs' ledgers in `relay/` have the sizes.
- **Judge the verdicts after three months.** `/scorecard` will by then hold
  enough of each kind to compare. If BUY is not ahead of the index more often
  than not, or SELL not behind it, the verdicts are adding nothing the index
  would not, and should change or stop. Its breakdowns say which of the
  weekly themes, the reactions, `/analyse` and the old news-led closer look
  have done better, and whether high confidence has meant anything.
- **Promotion.** A command to move a name from "new names in the news" straight
  into a sector. `/watchlist add <sector> <ticker> <name>` already does the
  work; this would just save the typing.
- **Market-wide movers in the brief.** The market's history already names the
  day's outsized moves for the closer look's reactions. A line of them in the
  overview -- the biggest moves among companies of some size, followed or not
  -- would give the brief the same view, for no extra request.
- **Tickers for the companies followed by name.** Morgan Stanley, Moderna and
  Spotify are followed by name alone, which leaves them without a price, a
  moves line or filings. Their tickers were left out because MS and SPOT match
  ordinary words; `match: name` in `config/companies.yaml` now keeps a ticker
  out of matching while still pricing it, so they could have one.

## /analyse — further

- **Its tools back.** The analysis used to be written by an agent that could
  look up any figure the company files and calculate exactly, rather than work
  from the fixed table and do the arithmetic in its head. It spoke to the API
  directly and was removed with it. Claude Code can be given tools of our own
  through an MCP server (`--mcp-config`), so the Go side could serve
  `find_concepts`, `read_concept` and `compute` to the analysis call. Until
  then, every analysis works from the table, as every relay analysis so far
  has.
- **Charts.** The trading history is described in words. A picture of price
  against its averages, or free cash flow against capital spending, would carry
  more of it in less space. Telegram takes images; drawing one means a Go
  plotting library.
- **Peers beside the company.** The SEC's frames API returns one figure for
  every filer for one period in a single request -- every company's revenue
  for the second quarter, say. Set against the companies in the same SIC code,
  it would let the analysis and the verdicts say whether a margin or a growth
  rate is high for the industry, which today they are told not to judge.
- **Companies with no US listing.** The analysis reads SEC filings, so Tencent,
  Keyence and anything without a US listing are not covered. Japan's EDINET is a
  free XBRL API and would cover Tokyo; Hong Kong and mainland Europe need a paid
  vendor.

## Relay runs

- **Smaller requests.** The general block and the section caps have been dealt
  with; two cuts are left. The sorting pass re-rates stories earlier briefs
  already carried, when yesterday's rating would do. And a relay brief is still
  four sorting batches of 40KB, which is the bulk of the reading: batching by
  source, so the near-identical items arrive together, would make each one
  quicker to answer.

## Done

- 1 October 2026, at the owner's request: every stage on Opus 5.5; the brief
  in plainer English with about twice the room, emoji, highlighted figures and
  linked jargon; a look ahead at the week's releases (ForexFactory) and
  results (Nasdaq); one-line notes on the verdicts; verdicts checked in two
  sources with web search and a news search per company; `/analyse` with the
  latest quarters and its own search for recent news; and `/industry`.

- Triage: a small model rates and places every article before the cap.
- Market levels from FRED: yields, the curve, fed funds, S&P 500, VIX.
- `SOURCE_LINKS=off|short|full`.
- The brief written for a non-specialist, with terms explained, in bullets.
- `/analyse` (asks which company): SEC filings read and written up, any SEC filer
  including foreign ones with a US listing, in their own currency, with the
  current year so far beside the full years, and the share price against them,
  read with the method in `config/method.md`.
- What the share has done: returns over weeks and months, fifty and two-hundred
  day averages, the year's high and low, volume-weighted average price, volume
  against its averages, and volatility.
- What has been written lately: company news, filtered to pieces that actually
  name the company, spread across days, and used as reported claims rather than
  filed facts.
- Deployed to Fly as `joseph-market-watch` (personal organisation, Singapore),
  with Claude Code on the machine logged in by `CLAUDE_CODE_OAUTH_TOKEN`.
- The relay is the only way the service calls a model. Each call is a file
  answered by Claude Code headless, one process per call -- Opus 5.5 for
  every stage since 1 October 2026 -- or answered by hand with
  subagents. No API
  key, no API spend.
- The bot answers only the chat that registered it.
- Worth a closer look: about half an hour after the brief. On Mondays up to
  ten companies found from two years of the whole US market's prices (Massive)
  and Singapore's thirty largest: the leaders sorted into the themes driving
  them, industries growing before their shares found on the web, each theme
  researched for the part the market has not paid for, and the companies in
  it judged against their theme's valuation and their own history, under two
  rules the code enforces. Every day up to three shares that moved far beyond
  their usual on the news, where the move and the news do not fit. Each
  verdict rests on what `/analyse` reads. It goes to the channel with the daily
  brief, under a note saying a model wrote it and that it is not advice; `/now`
  keeps it to you. `/scorecard` measures every verdict against the S&P 500
  from the next open once it is a week old.
- More data behind the verdicts and `/analyse`: analysts' forecasts and their
  revisions, price targets, results against forecast, insider trades, short
  interest and fund holdings (Nasdaq); the latest results release (SEC); oil,
  gas, copper, the dollar, the credit spread and inflation expectations (FRED);
  and for `/analyse`, two news searches of the company's last month (Tavily).
- A channel for other readers: the daily brief is posted there too, and
  `/share` posts the latest brief or analysis. Readers can only read.
- The watchlist as files: `config/sectors.yaml` describes each section,
  `config/companies.yaml` lists the companies followed, `config/sources.yaml`
  the feeds. No keywords, no migrations: `/watchlist` and `/sources` changes
  are kept on the server on top of the files, and `scripts/sync-from-fly.sh`
  writes them in.
- Sorting and a review, both Opus: an article belongs to at most two sectors,
  the review moves what the sorting misplaced, a section with fewer than ten
  stories is filled from those rated 3 where the review agrees, a section is
  written from at most 25, and the general block keeps only what was rated 4
  or 5. A name match
  rated 1 or 2 is not written about, which keeps broker notes, board
  appointments and listicles out of the sections.
- Every prompt in one file, `config/prompts.md`, checked at startup for the
  markers its replies are parsed by.
- Citations: every claim carries a link to the article behind it.
- Repeats: stories earlier briefs carried are marked, not reported again.
- Weekends: no brief on days the market was shut.
- New names in the news, with every ticker checked against the exchange.
- Share prices: benchmark funds and every watchlist share, each section headed
  by its biggest moves, and a search for why when a share moves well beyond the
  market, with its averages and range for context.
- Inflation from FRED beside the yields: the consumer price index and its core,
  year on year.
- Failure alerts: a brief that fails says so in the chat.
- `/stats`: what recent briefs found and did, kept on the data volume.
- CI: gofmt, vet, tests and build on every push.

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → **7 Backlog**

That's the end of the path. Back to the [README](../README.md), or to [4 Function by function](FUNCTIONS.md) to look something up.
