# Backlog

What is agreed but not built. Each entry says what it is, what it needs from
you, and why it is worth doing.

## Waiting on you

- **Push the repository.** Every commit is still local only.
- **Deploy news search.** Built and checked locally, not yet deployed.
  `scripts/fly-deploy.sh` now sends `TAVILY_API_KEY` from `.env` as a Fly
  secret along with the others.

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

- **Judge the wider watchlists after a week.** Each watchlist now describes its
  sector in a sentence, so an article is placed by what it bears on rather than
  by the words it happens to contain. `/stats` reports how many were placed that
  way and shows five of them. Read those for a week. If they are stories you
  would want, the net is set right; if they are stretches, tighten the sentences
  or raise `MinPlacementRating`. Only once that is settled is it worth adding
  sector feeds -- chip trade press, energy and shipping, drug development,
  freight -- since a gate that works makes extra sources cheap and a gate that
  does not makes them mush. The test for any new feed is the one that condemned
  the 8-K firehose: after a fortnight, how many of its articles were placed, and
  how many were cited.
- **Prices outside the US, in the brief.** Done for the closer look. A listing
  on any of the fourteen exchanges now takes its price from the daily-history
  source -- Yahoo's charting endpoint, keyless, in the currency the share
  actually trades in -- with `prices.Latest` turning the last two closes into a
  day's move. The keyed vendors were checked against a live key first and are
  no use: Twelve Data's free tier answers Hong Kong with "available starting
  with the Pro or Venture plan", London with "the Grow or Venture plan", and
  does not resolve Tokyo or Singapore at all. `TWELVEDATA_API_KEY` was dropped
  rather than left as a promise the tier cannot keep.
  The new names now do the same and show the move beside the ticker. What is
  left is `collectPrices` in `internal/app/prices.go`, which still reads the US
  quote feed alone. It prices every watchlist share, and today all 95 trade in
  New York, so nothing is missing; but a Tokyo or London listing added to a
  watchlist would go without a price, a moves line or a mover search. The same
  two-source split would fix it, and the chart source needs no pacing.
- **Judge the verdicts after three months.** `/scorecard` will by then hold a
  few hundred. If BUY is not ahead of the index more often than not, or SELL
  not behind it, the verdicts are adding nothing the index would not, and
  should change or stop. Worth checking separately: the companies found in
  the news against the connected ones.
- **Promotion.** A command to move a name from "new names in the news" straight
  into a watchlist. `/watchlist add <group> <ticker>` already does the work; this
  would just save the typing.

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

- Triage: a small model rates and places every article before the cap.
- Market levels from FRED: yields, the curve, fed funds, S&P 500, VIX.
- `SOURCE_LINKS=off|short|full`.
- The brief written for a non-specialist, with terms explained, in bullets.
- `/analyse <ticker>`: SEC filings read and written up, any SEC filer
  including foreign ones with a US listing, in their own currency, with the
  current year so far beside the full years, and the share price against them,
  read with the method in `internal/fundamentals/method.md`.
- What the share has done: returns over weeks and months, fifty and two-hundred
  day averages, the year's high and low, volume-weighted average price, volume
  against its averages, and volatility.
- What has been written lately: company news, filtered to pieces that actually
  name the company, spread across days, and used as reported claims rather than
  filed facts.
- Deployed to Fly as `joseph-market-watch` (personal organisation, Singapore),
  with Claude Code on the machine logged in by `CLAUDE_CODE_OAUTH_TOKEN`.
- The relay is the only way the service calls a model. Each call is a file
  answered by Claude Code headless, one process per call, Haiku to sort and
  spot names and Opus to write, or answered by hand with subagents. No API
  key, no API spend.
- The bot answers only the chat that registered it.
- Worth a closer look: after the brief, up to six companies the news bears on,
  found with web search, each with a buy, hold or sell verdict from its trading
  and SEC accounts. It goes to the channel with the daily brief, under a note
  saying a model wrote it and that it is not advice; `/now` keeps it to you.
  `/scorecard` measures every verdict against the S&P 500 once it is a week
  old.
- A channel for other readers: the daily brief is posted there too, and
  `/share` posts the latest brief or analysis. Readers can only read.
- Watchlists as sectors: each one says in a sentence what it covers, an article
  belongs to at most two of them, a section is written from at most 25, and the
  general block keeps only what was rated 4 or 5.
- Keyword matches are rated too: one rated 1 or 2 is not written about, which
  keeps broker notes, board appointments and listicles out of the sections.
- Every prompt in one file, checked at startup for the markers its replies are
  parsed by.
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
