**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → **7 Backlog**

---

# Backlog

This page lists what has been agreed but not built. Each entry says what it
is, what it needs from you, and why it is worth doing.

## Waiting on you

- **Try the latest changes live.** The changes of 2 October 2026 are built
  but have not run on real data yet. They cover plain writing in every
  report, the reworked `/analyse` (the case for and against, where the
  company is heading, whether its figures hold up) and `/industry`'s look
  ahead. One `/analyse` and one `/industry` would show how they read. Each
  uses the Claude plan.
- **Push and deploy.** The commits since 1 October are on your machine only.
  Push them with `git push`, then deploy with `scripts/fly-deploy.sh`.

## Reliability

- **A weekly report on the sources.** A failed brief now sends you a message,
  and `/stats` names the feeds that fail often. But nothing tells you on its
  own; you have to ask. A weekly message would fix that.
- **Decide on the media feeds after a fortnight of search.** Search runs
  beside the feeds. `/stats` shows which cited stories search failed to find,
  listed by the source that carried them. If that list holds only government
  and company releases, the eighteen media feeds can be turned off. They are
  the part of the source list that breaks most. RUNBOOK.md, "News search",
  explains how to read the list.

## Brief quality

- **Judge the sorting and the review after a week.** The keywords are gone.
  An article now reaches a section in one of two ways. Either it names a
  followed company, or Opus read it against the sector's description in
  `config/sectors.yaml` and the review agreed. `/stats` reports how many
  articles were placed by judgment and how many the review moved, with five
  examples of each. Read those for a week.
  - If the placements are stories you want and the moves are corrections,
    the net is set right.
  - If they are a stretch, sharpen the descriptions, raise
    `MinPlacementRating`, or turn the review off with `REVIEW=false`.

  The same week shows whether the top-up earns its place. `/stats` shows how
  many stories rated 3 were offered to thin sections, how many the review
  kept, and five examples. If what it keeps reads as padding, set
  `ThinSection` in `internal/triage/topup.go` to 0. Thin sections will then
  disappear again, as they used to.

  Only once that is settled is it worth adding sector feeds, such as chip
  trade press, energy and shipping, drug development and freight. A sorting
  step that works makes extra sources cheap. One that doesn't turns them into
  noise. Test any new feed the way the 8-K feed was tested and dropped: after
  a fortnight, count how many of its articles were placed and how many were
  cited.
- **Prices outside the US, in the brief.** This is done for the closer look
  and for the new names. A listing on any of the checked exchanges takes its
  price from Yahoo's daily price data. That needs no key, and it gives the
  price in the currency the share actually trades in. `prices.Latest` turns
  the last two closes into the day's move.

  The paid services were tried first, with a real key, and are no use.
  Twelve Data's free tier refuses Hong Kong ("available starting with the Pro
  or Venture plan") and London ("the Grow or Venture plan"). It doesn't find
  Tokyo or Singapore at all. So `TWELVEDATA_API_KEY` was dropped.

  What is left is `collectPrices` in `internal/app/prices.go`. It reads US
  listings only, from the quote feed and then the charts for whatever the feed
  misses. It prices every company followed. Today all 98 tickers trade in New
  York, so nothing is missing. But a Tokyo or London listing added to
  `config/companies.yaml` would get no price, no moves line and no mover
  search. Giving each company an exchange, and spelling its symbol with
  `ChartSymbol`, would fix that.
- **Judge the weekly themes after a month.** Read four weeks of them. Ask
  whether the numbers really show each theme, and whether the early themes
  rest on figures rather than talk. Ask whether the picks sit in the part of
  their theme the market hasn't paid for yet, or are simply its leaders. Also
  check what a Monday costs the plan. The ledgers of the `look` runs in
  `relay/` have the sizes.
- **Judge the verdicts after three months.** By then `/scorecard` will hold
  enough of each kind to compare. If BUYs don't beat the index more often than
  not, or SELLs don't trail it, the verdicts add nothing, and they should
  change or stop. The scorecard's breakdowns show which source has done better
  (the weekly themes, the reactions, `/analyse` or the old news-led closer
  look), and whether high confidence has meant anything.
- **Promotion.** A command to move a company from "new names in the news"
  straight into a sector. `/watchlist add <sector> <ticker> <name>` already
  does this. The new command would just save typing.

## Verdicts

- **Learn from the scorecard.** Put aside on 2 October 2026. Today the
  scorecard only keeps score. It feeds back in only two ways: a company isn't
  picked twice within 8 weeks, and a verdict sees the one before it. Three
  options were offered:
  - show the judge each company's past outcome;
  - show the judge the record of each source and each kind of verdict;
  - add code rules, such as lowering the confidence of a group with a poor
    record, or showing fewer of it (the one recommended).

  All three need about 30 graded verdicts in a group first.
- **Backtests.** Also put aside on 2 October 2026. The parts of the closer
  look that use no model could be tested on the stored two years of prices.
  That covers the weekly screen's leaders and industries, and whether
  outsized moves carried on. No model calls would be needed.

## /analyse — further

- **Give it its tools back.** The analysis used to be written by an agent
  that could look up any figure the company files and calculate exactly.
  Today it works from a fixed table and does the sums in its head. That agent
  talked to the API directly, so it went when the API did. Claude Code can be
  given tools of our own through an MCP server (`--mcp-config`). The Go side
  could then offer `find_concepts`, `read_concept` and `compute` to the
  analysis call. Until then, every analysis works from the table.
- **Charts.** The trading history is described in words. A picture would
  carry more of it in less space, for example the price against its averages,
  or free cash flow against capital spending. Telegram takes images, but
  drawing one needs a Go plotting library.
- **Peers beside the company.** In one request, the SEC's frames API returns
  one figure for every filer for one period, such as every company's revenue
  for the second quarter. Set against the companies in the same industry
  code, this would let the analysis and the verdicts say whether a margin or
  a growth rate is high for the industry. Today they are told not to judge
  that.
- **Companies with no US listing.** The analysis reads SEC filings, so it
  can't cover Tencent, Keyence or anything else without a US listing. Japan's
  EDINET is a free XBRL service and would cover Tokyo. Hong Kong and mainland
  Europe need a paid provider.

## Relay runs

- **Smaller requests.** The general block, the section limits and the
  re-sorting of articles seen before have been dealt with. One cut is left.
  The sorting batches are still most of a brief's reading. Batching by source
  would put near-identical items together, which would make each batch
  quicker to answer.
- **Run each background request in a Fly Sprite.** Each `/analyse` or
  `/industry` would get a sprite of its own, a small throwaway machine Fly
  starts in about a second. Its Claude calls would run there, and the sprite
  is deleted when the request ends. The brief stays on the server. The
  server's 1 GB then only has to hold the brief, and nothing a request ran is
  left behind. The relay's calls suit this. Each one sends a prompt into
  `claude -p` and reads the reply, with web search as its only tool. Claude
  Code comes installed on every sprite.

  Sprites charge only for the CPU and memory used, by the second, and a
  deleted sprite costs nothing. `claude -p` mostly waits on Anthropic. One
  request should cost well under a cent, which is a guess until measured. It
  doesn't change how much of the Claude plan is used.

  What to settle when building it:
  - Use the Go SDK's streaming exec, not the plain HTTP exec. The plain one
    puts environment variables in the URL, and the Claude login is one.
  - Check the sprite's Claude Code version against the one the Dockerfile
    requires. If it's older, run that request on the server.
  - If the Sprites API fails or is slow, run the request on the server, as
    today.
  - Keep three at a time, the brief alone and one at a time past 85%. Those
    rules are about the plan, not memory.

  It needs from you: a token limited to Sprites, set as `SPRITES_TOKEN` on
  Fly, and a live test before it is deployed. The alternative is a 2 GB
  machine, at about $6 a month more.

## Done

- 3 October 2026, at the owner's request:
  - `/analyse` and `/industry` run in the background, three at a time, in
    the order asked. Other commands no longer wait behind them. Once any plan
    window is 85% used, the chat is warned and they run one at a time. A
    brief runs alone, with the rest waiting. When a chat's requests are done,
    it is told how much of the plan is used. A ticker the SEC doesn't know is
    answered at once, without waiting its turn.
  - A line under the brief's overview, "Across the market", shows the day's
    five biggest moves among US companies worth US$2bn or more, followed or
    not. They are the moves the closer look's reactions start from.
  - The sorting remembers its verdict on each article for eight days, so an
    article an earlier brief sorted isn't sent to Opus again. On 1 October
    about half the articles sorted were over a day old.
- 2 October 2026, at the owner's request:
  - Plain writing. Every report the models write follows one shared set of
    writing rules, and the rules that made sentences dense are gone.
  - `/analyse` is about the case for and against the company, not about
    whether to buy. It adds where the company is heading and whether its
    figures hold up. It keeps CAN SLIM's questions in mind, and sets each
    price move beside the S&P 500's. The verdict is short and comes last.
  - `/industry` also covers what people are saying, what is coming, and what
    follows from it.
  - A question the bot stops waiting for is deleted, so Telegram stops
    offering to answer it.
  - `/industry` lists more companies from more countries, and every ticker
    shows its country's flag.
  - Mainland China listings are checked under OpenFIGI's China code.
- 1 October 2026, at the owner's request: every stage moved to Opus 5.5. The
  brief became plainer, with about twice the room, emoji, highlighted figures
  and linked finance words. It added a look ahead at the week's economic
  releases (ForexFactory) and results (Nasdaq). The verdicts gained one-line
  notes, a check in two sources with web search, and a news search for each
  company. `/analyse` gained the latest quarters and its own search for recent
  news. `/industry` was added.
- Triage: a model rates and places every article before the limit is applied.
- Market levels from FRED: yields, the yield curve, the Fed's rate, the
  S&P 500 and the VIX.
- `SOURCE_LINKS=off|short|full`.
- The brief is written for a non-specialist, in bullets, with terms explained.
- `/analyse`, which asks which company. It reads and writes up the SEC
  filings of any filer, including foreign companies with a US listing, in
  their own currency. It shows the current year so far beside the full years,
  and the share price against them. It reads them with the method in
  `config/method.md`.
- What the share has done: returns over weeks and months, the 50-day and
  200-day averages, the year's high and low, the volume-weighted average
  price, volume against its averages, and volatility.
- What has been written lately: company news, kept only where a piece actually
  names the company, spread across days, and used as reported claims rather
  than filed facts.
- Deployed to Fly as `joseph-market-watch` (personal organisation,
  Singapore). Claude Code on the machine logs in with `CLAUDE_CODE_OAUTH_TOKEN`.
- The relay is the only way the service calls a model. Each call is a file,
  answered by Claude Code running without a screen, one process for each
  call. Every stage has used Opus 5.5 since 1 October 2026. A call can also be
  answered by hand. There is no API key and no API bill.
- The bot answers only the chats its owner allows.
- Worth a closer look, sent straight after the brief:
  - On Mondays, up to ten companies. They come from two years of prices for
    the whole US market (from Massive) and Singapore's thirty largest
    companies. The leaders are sorted into the themes driving them. Industries
    that are growing before their shares have caught up are found on the web.
    Each theme is researched for the part the market hasn't paid for yet. Its
    companies are judged against their theme's valuation and their own
    history, under two rules the code enforces.
  - Every day, up to three shares that moved far more than usual on the news,
    where the move and the news don't fit.
  - Each verdict rests on what `/analyse` reads. It goes to the channel with
    the daily brief, under a note saying a model wrote it and that it is not
    advice. `/now` keeps it to you.
  - `/scorecard` measures every verdict against the S&P 500 from the next
    open, once the verdict is a week old.
- More data behind the verdicts and `/analyse`. From Nasdaq: analysts'
  forecasts and how they have changed, price targets, results against
  forecasts, insider trades, short interest and fund holdings. From the SEC:
  the latest results release. From FRED: oil, gas, copper, the dollar, the
  credit spread and inflation expectations. For `/analyse`: two news searches
  of the company's last month (Tavily).
- A channel for other readers. The daily brief is posted there too, and
  `/share` posts the latest brief or analysis. Readers can only read.
- The watchlist as files. `config/sectors.yaml` describes each section,
  `config/companies.yaml` lists the companies followed, and
  `config/sources.yaml` lists the feeds. There are no keywords and no
  migrations. Changes made with `/watchlist` and `/sources` are kept on the
  server on top of the files, and `scripts/sync-from-fly.sh` writes them in.
- Sorting and a review, both by Opus:
  - an article belongs to at most two sectors;
  - the review moves what the sorting put in the wrong place;
  - a section with fewer than ten stories is filled from those rated 3, where
    the review agrees;
  - a section is written from at most 25 stories;
  - the general block keeps only stories rated 4 or 5;
  - a story that names a followed company but is rated 1 or 2 is not written
    about, which keeps broker notes, board appointments and listicles out.
- Every prompt is in one file, `config/prompts.md`. At start-up the program
  checks it for the markers that replies are read by.
- Citations: every claim carries a link to the article behind it.
- Repeats: stories that earlier briefs carried are marked, not reported again.
- Weekends: no brief on days the market was shut.
- New names in the news, with every ticker checked against its exchange.
- Share prices: the benchmark funds and every watchlist share. Each section
  is headed by its biggest moves. When a share moves well beyond the market, a
  search asks why, and its averages and range give context.
- Inflation from FRED beside the yields: the consumer price index and its
  core, year on year.
- Failure alerts: a brief that fails says so in the chat.
- `/stats`: what recent briefs found and did, kept on the data volume.
- CI: gofmt, vet, tests and a build on every push.

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → **7 Backlog**

That's the end of the path. Go back to the [README](../README.md), or to [4 Function by function](FUNCTIONS.md) to look something up.
