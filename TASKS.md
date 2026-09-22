# Backlog

What is agreed but not built. Each entry says what it is, what it needs from
you, and why it is worth doing.

## Waiting on you

- **Push the repository.** Every commit is still local only.

## Reliability

- **Weekly source health.** A failed brief now messages you, and `/stats` names
  feeds that fail regularly, but nothing volunteers it: you have to ask. A
  weekly message would close that.
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
- **Prices outside the US.** Every keyed free tier turned out to be US-only:
  Twelve Data answers London with "available starting with the Grow plan" and
  does not resolve Hong Kong or Tokyo at all. A company quoted elsewhere
  therefore has no move in the brief.
  Since then the daily-history source added for `/analyse` (Yahoo's charting
  endpoint, in `internal/prices/history.go`) has proved to answer for Taipei,
  Hong Kong and Tokyo in the local currency, keyless. A close against the
  previous close is a day's move, so the brief's gap could be filled from it
  without a paid plan. What it is not is a live quote: the brief would be saying
  what a share closed at rather than what it is trading at now, and the source
  is undocumented and can refuse without notice. Worth doing, with that said
  plainly in the brief, before spending $20-80 a month on a vendor.
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
- Share prices: benchmark funds and the companies in today's news.
- Failure alerts: a brief that fails says so in the chat.
- `/stats`: what recent briefs found and did, kept on the data volume.
- CI: gofmt, vet, tests and build on every push.
