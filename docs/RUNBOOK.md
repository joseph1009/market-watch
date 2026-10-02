**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → **6 Running it** → [7 Backlog](TASKS.md)

---

# Running the brief and the analysis

Every model call the service makes goes through the **relay**. The call is
written to a file. Something answers it, and the answer is written beside it.
A ledger records what was asked and what came back. There is no other way to
call a model. There is no API key, and there are no direct API calls.

`RELAY_ANSWER` sets who answers:

| `RELAY_ANSWER` | Who answers | Waits for a person | Used for |
|---|---|---|---|
| `claude` (the default) | Claude Code, running without a screen, one fresh process for each call | No | The service as deployed, and any run from a terminal |
| `session` | Whoever writes the reply file, usually a Claude Code session using subagents | Yes | A run you want to watch or answer yourself |

Both use your Claude subscription, not API credit.

A **pre-written run** hands the delivery a reply written earlier. It asks
nothing and waits for nothing. It exists to check the layout and delivery.

## The stages

A brief makes four kinds of call. Its closer look makes up to four more. An
analysis makes one, and so does `/industry`.

| Stage | What it does | Prompt | Default model | Override | Reply format |
|---|---|---|---|---|---|
| `triage` | Rates every article from 1 to 5 and places it in up to two sectors | `triage.system` | Opus | `MODEL_TRIAGE` | `number\|rating\|watchlist ids` |
| `review` | Checks where the sorting put everything that could reach the brief, and moves what belongs elsewhere, **with web search** | `review.system` | Opus | `MODEL_REVIEW` | `number\|section ids`, one line for each move |
| `brief` | Writes the brief | `brief.system` | Opus | `MODEL_BRIEF` | `## OVERVIEW`, then `## SECTION: <id>` blocks |
| `names` | Names the companies the day was about that no watchlist follows | `discover.system` | Opus | `MODEL_NAMES` | `name\|ticker\|exchange\|article numbers\|what happened` |
| `themes` | Once a week, sorts the market's 150 leaders into the themes driving them | `themes.system` | Opus | `MODEL_THEMES` | `THEME:`, `DRIVER:`, `MEMBERS:` blocks |
| `scout` | Once a week, finds industries whose business is growing before their shares have caught up, **with web search** | `scout.system` | Opus | `MODEL_SCOUT` | `THEME:`, `DRIVER:`, `EVIDENCE:`, `MEMBERS:` blocks |
| `research` | Once a week for each theme, finds the part the market hasn't paid for and the companies in it, **with web search** | `research.system` | Opus | `MODEL_RESEARCH` | `DRIVING:`, `PRICED IN:`, `THE VALUE:`, then `name\|ticker\|exchange\|buy or sell\|why` |
| `verdicts` | Gives each company BUY, HOLD or SELL from the facts gathered for it, three companies a call. It checks each case in two independent sources, **with web search** | `verdicts.system` | Opus | `MODEL_VERDICTS` | `=== symbol` blocks of `VERDICT:`, `CONFIDENCE:`, `VALUE:` or `CHANGED:`/`MOVE:`/`REACTION:`, `CASE:`, `NUMBERS:`, `CHECKED:`, `RISK:` |
| `analysis` | Writes up one company for `/analyse`. It searches for the last fortnight's news and the company's plans itself, **with web search** | `analysis.system`, the method, and `analysis.related` | Opus | `MODEL_ANALYSIS` | Plain text with headings in capitals |
| `industry` | Explains an industry for `/industry`: how it fits together, where it is heading, and companies to look into, **with web search** | `industry.system` | Opus | `MODEL_INDUSTRY` | `### ` sub-headings over bullets, then `COMPANIES BY PART` and `part\|name\|ticker\|exchange\|why` |

The sorting sends the day's articles in batches of 60, two at a time
(`RELAY_CONCURRENCY`). When a person is answering, the batches hold 150 each,
so there are fewer files to deal with.

Every stage has used Opus 5.5 (`claude-opus-5-5`) since 1 October 2026, at the
owner's request. You can still move one stage to another model with its
`MODEL_` variable. The review is the check on the sorting's reading.
`REVIEW=false` turns the review off.

Only six stages have tools: `scout`, `research`, `review`, `verdicts`,
`analysis` and `industry`. Their tools are web search and reading pages,
nothing else. They get no shell, no files and no connectors. Each is told what
to search for:
- the review searches only to learn what an unfamiliar company does;
- the verdicts search to check a case in a second source;
- the analysis searches for the last fortnight's news, the company's plans and
  what is due in the next ninety days;
- the industry searches for the industry's shape and where it is heading.

Every other stage has no tools at all. A subagent that answers one of these
six by hand needs web access too.

## The prompts

Every instruction sent to a model is in
[config/prompts.md](../config/prompts.md), one section for each `=== id ===`
line. The analysis also gets [config/method.md](../config/method.md), the
house method for reading accounts.

Every prompt whose words the reader sees also gets the `writing` section added
to its end. That section holds the plain-writing rules: one idea a sentence,
everyday words, and each term explained.

You can edit the wording without touching Go. What must survive an edit are
the markers the replies are read by: `## OVERVIEW`, `## SECTION:`, the lines
split by `|`, and the rating format. The program checks for them at start-up
and refuses to run without them. So a broken prompt fails at once, not at
delivery.

`PROMPT_FILE=/path/to/prompts.md` makes it use another copy, without a
rebuild.

To see exactly what a stage was sent, open its request file in the run's
folder. It holds the whole call: the `===== SYSTEM =====` block, then the
`===== PROMPT =====` block.

## Where a run's files go

Each brief and each analysis gets its own folder under `RELAY_DIR`, which is
`DATA_DIR/relay` unless you change it. The last forty are kept.

```
data/relay/20260922-203000-brief/
  ledger.md               what was asked, what was answered, by which model, how big
  01-triage-request.txt   a call
  01-triage-reply.txt     its answer
  ...
  05-brief-request.txt
  05-brief-reply.txt
  06-names-request.txt
  06-names-reply.txt
data/relay/20260922-211500-analysis-mu/
  ledger.md
  01-analysis-request.txt
  01-analysis-reply.txt
```

In the ledger, `- [ ]` is a call waiting for an answer and `- [x]` is one that
was answered. `- [!]` is one that failed or was given up, with the reason.

## The latest run's data

The relay keeps the model calls. `DATA_DIR/cache` keeps everything else that
the latest run of each kind was made from, and what it sent. So you can work
out a change from the last run's data instead of making a new run. A run
writes under `.running/` and replaces its kind's folder when it finishes, so
each folder only ever holds the latest one. If two analyses overlap, the one
that finished last is kept. A run cut short stays under `.running/` until the
next run of its kind starts.

```
data/cache/brief/
  run.json              kind, when it started and finished, why it failed, the files
  prices.json           every followed share's price
  movers.json           the shares that moved well beyond the market
  filings.json          the SEC filings, as articles
  search.json           what the news searches found, and the credits they used
  collected.json        every article that arrived, the ones kept, the ones the limit cut
  review.json           what the review moved
  articles.json         what the brief was written from, rated and placed
  levels.json           the market levels block
  trends.json           the movers' price history
  report.json           the brief, as read from the model's reply
  messages.html         the messages as sent
  model/                every model call's request and reply, as in the relay
data/cache/analysis/
  run.json              "subject" is the ticker
  snapshot.json         the accounts, business, filings, news, forecasts, results release
  related.json          the companies to read beside it, as checked
  messages.html
  model/
data/cache/recommendations/
  run.json
  look.json             the brief's articles, and whether it was the scheduled run
  backdrop.json         commodities, the dollar, interest rates
  leaders.json          on the weekly run: the market's leaders, as measured
  industries.json       the popular and early industries, as measured
  themes.json           the themes sorted and scouted, with their figures
  research.json         what each theme's research found
  moves.json            the day's largest moves against each share's usual
  reactions.json        those the brief's articles explain, to be judged
  facts.json            the facts each company was judged on, with its
                        valuation (facts-2.json, verdicts-2.json: the second
                        round, on the weekly run)
  verdicts.json         every verdict, shown or not
  shown.json            what was shown
  messages.html
  model/
```

A closer look sent straight after a brief (`--once --share`) still writes its
own folder. Its model calls go to `recommendations/model/`, not the brief's.
Every password and key in the settings is scrubbed out of what is written.

To bring the server's copy down to your machine, run this from the root of
the repository:

```
scripts/sync-cache-from-fly.sh                  # all three
scripts/sync-cache-from-fly.sh analysis         # or one or two of them
```

Each folder fetched replaces your local one. A local run writes to your local
`data/cache` the same way.

## Running one by hand

To send a brief, answered by Claude Code, to the chat:

```
go run ./cmd/market-watch --once
```

The tests can do the same. They are also how you run an analysis:

```
LIVE_BRIEF=1 go test ./internal/app -run TestLiveBrief -v -timeout 1h
LIVE_ANALYSIS=MU go test ./internal/app -run TestLiveAnalysis -v -timeout 1h
```

`go run ./cmd/market-watch --check` checks the bot, the feeds and the search
key, and that Claude Code is installed. It also shows which model will answer
each stage. It asks no model anything and spends no search credits.

## Answering a run yourself

Set `RELAY_ANSWER=session`, and each call waits for its reply file:

```
LIVE_BRIEF=1 RELAY_ANSWER=session go test ./internal/app -run TestLiveBrief -v -timeout 3h
```

Add `RELAY_AT=08:30` for a **prepared run**. You start it the night before. It
waits until 08:30, gathers the news, and leaves its requests waiting for the
next time a session is open. Set `-timeout` long enough to cover the wait.

The rule is: **the answering session never reads a large request itself.** A
day's sorting is 40KB a batch, and the brief's request is the largest of all.
If the main session reads those, it has no room left for the work.

1. Read `ledger.md` in the newest run folder. It says which stages are waiting
   and how large each one is.
2. For each waiting request, start one subagent on the model that stage would
   get (Opus 5.5 for every stage). Tell it to:
   - read that one request file and nothing else;
   - write the reply file in the format the request's own
     `===== SYSTEM =====` block asks for;
   - add one line to `ledger.md` saying what it did; and
   - report back in a single line.
3. Batches can be answered at the same time, since they don't depend on each
   other.
4. When a stage's reply lands, the run carries on by itself within two
   seconds.

Each request's own system block defines its reply format. That is why a
subagent needs no other context.

### When the session's context is compacted

On a long run it will be, and the design expects that.

- **Everything is on disk.** Each request and reply is a file, written before
  the next stage starts. Compaction loses the reasoning, never the files.
- **The ledger is the memory.** After a compaction, an interruption or a
  restart, read `ledger.md`.
- **Leave a short note, not a transcript.** A subagent that has just rated 150
  headlines should leave one line behind, such as "rated 150, 12 at 4 or
  higher, mostly Fed and oil". It should not summarise each one.
- **Compact between stages, not during one.** The safe moment is after a reply
  is written and before the next request appears.

If the answering session dies, you lose nothing but time, because the Go
process is still waiting for its reply file. Open a new session, read the
ledger, and carry on.

## A pre-written run

```
CANNED_REPLY=analysis.txt FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestCannedAnalysis -v
```

This sends an analysis written earlier through the real `/analyse` delivery.
It uses real filings, prices, layout and chat, but makes no model call.

## Logging in

On a desktop where you have run `/login` in Claude Code, you need nothing
more. On a server there is nobody to log in, so:

1. On your own machine, run `claude setup-token`. It opens the browser, then
   prints a token that lasts a year.
2. Set that token as `CLAUDE_CODE_OAUTH_TOKEN` where the service runs.

The token uses your subscription. Its calls count against your plan's usage
limits, the same limits your own use of Claude Code draws on. It is for your
use only. The bot answers only your own chats, so nobody else's messages can
use it.

`ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` are never passed to Claude
Code, even when they are set. Otherwise Claude Code would use them instead of
the subscription.

## Worth a closer look

A second message follows the daily brief, straight after it. It has two
parts. Both start from numbers rather than from the day's news, and both leave
out the companies you follow, since the brief covers those.

**Every Monday: the week's picks.** Up to ten companies, under the themes they
were found in, each with a verdict. There are fewer when fewer hold up. It
never adds more just to reach a number.

1. **The market's history** (`internal/market`). Two years of daily prices for
   every US listing come from Massive (`MASSIVE_API_KEY`) and are kept in
   `data/market/`. Nasdaq's list says what each symbol is, how much it is
   worth and its industry. The first fill is about 500 requests at five a
   minute, which takes two hours. It runs in the background from start-up.
   After that it is one request a day. Splits are read from Massive and
   applied, so a two-for-one split doesn't look like a crash. Singapore's
   thirty largest companies (`config/singapore.yaml`, the Straits Times Index)
   are read from the chart source and measured beside them, in US dollars.
2. **The leaders.** The starting field is every company worth at least
   US$2bn, with a share price of at least US$5, US$20m traded on an average
   day, and thirteen months of history. Funds, blank-cheque companies and
   other kinds of security are left out. A company must still be above its
   200-day average. Its rise must not have come in one day, or stopped dead at
   a takeover price. Each one left is then ranked on four returns against the
   S&P 500: two years, the year without its last month, six months, and the
   year's return for each unit of its swing. The best 150 by their average rank
   go forward. Singapore's companies don't have to meet the size limits.
3. **The industries.** These use every eligible company in each industry, not
   only the leaders.
   - *Popular* industries are ranked on their typical member's six- and
     twelve-month returns against the index, the share above their 200-day
     average, the money going in against a year ago, and how often the recent
     briefs' headlines named them.
   - *Early* industries are the ones whose year still lags the typical
     industry's. They are ranked on their three-month return, the rise over
     the month in the share above their 50-day average, the money going in
     against the six months before, and the headlines.
4. **The themes.** Opus (`themes`) sorts the leaders into up to three themes,
   by what is driving them rather than by the exchange's labels. Opus with the
   web (`scout`) looks for up to two industries whose business is growing in
   ways it can measure before their shares have caught up. The evidence can be
   order books, shipments, capacity, contracted demand, or a policy with money
   behind it. It looks among the early industries or wherever its research
   leads. It never picks one of the week's popular themes again.
5. **The research** (`research`, Opus with the web, two themes at a time). For
   each theme it finds what drives it, which parts the market has already paid
   for, and which part it hasn't and why. It then proposes up to four companies
   in that part as BUY candidates, or one priced beyond what its numbers
   support as a SELL. Only US and Singapore listings are allowed, and every
   ticker is checked against its exchange. A theme carried over from last week
   gets last week's research to build on. A company picked in the last eight
   weeks is proposed again only if something important has changed.
6. **Facts and valuation**, for up to sixteen companies across the themes.
   Each gets what `/analyse` reads (see below). Each also gets its multiples:
   price to earnings, price to sales, and company value to operating profit.
   These come from Nasdaq's market value and the last twelve months of filed
   figures in US dollars. They are set against the median of the rest of its
   theme, whose best members' accounts are read for it, and against its own
   last five year ends. Then the warning signs are checked:
   - more than 40% above its 200-day average;
   - above the average analyst price target;
   - insiders selling a net quarter of a percent of the shares in three months;
   - a tenth of the shares sold short.
7. **Verdicts**, described below. **Two rules belong to the code, not the
   model.**
   - A company cannot be a BUY if it costs more than its theme on every measure
     and its growth doesn't make up for it. Making up for it means its price to
     sales for each point of growth is lower than the theme's. The same applies
     if it costs more on every measure and shows two or more warning signs. A
     BUY given to such a company is turned into a HOLD.
   - A company whose accounts could not be read gets low confidence at most.
     None of Singapore's can be read.

   HOLDs are not shown. A company given the same verdict in the last eight
   weeks is not written up again.

The message ends with **the earlier picks**. Each pick from the last eight
weeks is listed with how far it has moved the way it was called, against the
S&P 500, from the first open after the call. The week is saved to
`data/themes.json`. So the themes run once a week, on the first scheduled run
whose history is full enough, and the service remembers what each theme's
research said.

**Every day: the reactions.** Up to three shares that moved at least three
times their usual daily move in the last session, on at least twice their
usual trading, where the brief's articles say why. Results come first, then
the largest moves against their usual. Up to six are judged. A reaction that
fitted its news is a HOLD and isn't shown. A day with no reactions sends
nothing.

**Facts** are gathered for every company, four at a time:
- a year of daily prices from the chart source, and the last session's move;
- for an SEC filer, what `/analyse` reads: five years of accounts, the business
  description and recent filings, what its price implies, and the latest
  results release, cut to 7,000 characters;
- the news feed's stories about it, and one Tavily news search;
- what analysts expect, and what insiders, short sellers and funds have done
  (from Nasdaq, `CONSENSUS`).

A company with no SEC filings is judged on its price and trading alone, and
the message says so.

**Verdicts** (`verdicts`, Opus with web search, three companies a call, two
calls at once) give BUY, HOLD or SELL over twelve months, with a confidence.
Each verdict gives the case, what is coming, the lever in the accounts it
depends on, the two to four numbers that decide it, and the biggest risk. It
checks the claim its case rests on in two independent sources. A theme pick
adds its price against its theme and its own history. A reaction adds what the
news changed, how far the share moved, and whether the move overreacted,
underreacted or fitted the news. The oil price, the dollar and the cost of
money are shown above them all. BUY and SELL mean at least five percentage
points better or worse than the S&P 500 over the twelve months, in US dollars.

The closer look starts as soon as the brief is sent. A day's reactions take
five to ten minutes. A Monday with the themes takes forty or so, so it still
arrives before the US open. The channel gets the brief and the closer look
together, as one post with a button to each page, once the closer look is done.
So the channel's brief arrives those few minutes later than yours. A restart or
a deploy during the closer look loses that day's closer look and the channel's
post. After `/now` or `--once`, the closer look follows at once, with the
reactions only. The week's themes run only on the scheduled run.
`IDEAS=false` turns the closer look off.

To see the numbers the themes start from without asking a model, run
`LIVE_MARKET=1 go test ./internal/app -run TestLiveMarket -v -timeout 4h`. It
fills `data/market` on your machine and logs the leaders, the industries and
the day's moves. `LIVE_THEMES=1 go test ./internal/app -run TestLiveThemes -v
-timeout 2h` runs a whole week's themes with the models. It sends them to your
chat alone, and doesn't save the week.

**Cost.** Each company judged costs one Tavily credit, so up to six a day and
up to sixteen more on a Monday. The scout, the research and the verdicts also
search the web, but with Claude's own web search, which uses the plan rather
than Tavily. A day is up to two verdict calls. A Monday adds a themes call, a
scout, five research calls and six more verdict calls, all on Opus. The ledger
of a `look` run in `relay/` has the actual sizes.

**Nasdaq is unofficial.** Its figures come from the addresses Nasdaq's own
website reads, with no key and no agreement, like the chart source. They can
change or refuse without notice. `market-watch --check` asks for one company's
forecasts and says whether they came back. `CONSENSUS=false` stops asking, and
the verdicts and analyses carry on without them.

**It goes to the channel with the daily brief.** It doesn't go through
`/share`, which only ever posts a brief or an analysis. It doesn't go with a
brief asked for by `/now` either. A verdict reaches the channel on the
scheduled run, or with `--once --share`, or not at all.

It was owner-only until 23 September 2026, and the reason still calls for
care. Publishing buy and sell calls to other people is investment advice to
others. In Singapore that can be a regulated activity. Anthropic's usage policy
asks two things of AI-written advice given to others: that a person reviews it,
and that readers are told a model wrote it.

Posting automatically gives up the first. The second is carried by the note at
the top of the channel's copy (`channelNote` in `internal/telegram/ideas.go`).
It says that a model wrote it, that nobody checked it, that it is often wrong,
and that it is not a recommendation to act. That note is the whole reason the
section is allowed out, and a test checks that it is there. **If you ever
remove it, make the section owner-only again.**

If you want to review it yourself without losing the channel, the change is
small. Have `sendIdeas` deliver to you alone, and let `/share` pass on the
closer look. Then nothing reaches readers until you have read it.

**`/scorecard`** says how past verdicts have done. Every verdict is written to
`scorecard.json` on the data volume. It is measured from the first price after
it was given, which is the next session's open, for the share and for the S&P
500 fund alike. The verdicts are written before the US open, from the night's
news. Measuring from the close before would credit them with the move that news
makes at the open, which nobody reading them could have caught. Once that
session has happened, `/scorecard` writes the entry price down beside the
verdict.

Once a verdict is a week old, it is scored. The score is how the share moved
against the index since, in US dollars. So a Tokyo share that rose while the
yen fell counts the way a dollar investor would have seen it. The BUYs and
SELLs are also split by confidence and by how the company was found, to show
whether either one tells better calls from worse ones.

A BUY promises to beat the index by five points over the year. It counts as on
course while it is ahead by at least that pace. That is five points a year,
scaled to the time passed, so about 0.4 points after a month. A SELL works the
same way, behind the index. The verdicts are twelve-month calls, so read the
scorecard for a pattern over months, not for any one company.

## Changing the watchlist

What the brief follows lives in three files in [config/](../config/). The
whole service reads them, and they are built into it:

- [sectors.yaml](../config/sectors.yaml) lists the sections, in order, each
  described in plain words. The sorting and the review judge an article
  against the description. The news search uses its first sentence. So the
  first sentence names the sector's ground. The sentences after it say what
  else belongs there, and what belongs in another section instead.
- [companies.yaml](../config/companies.yaml) lists the companies, by sector.
  Each has its ticker and the names headlines use. Some have `match: ticker`
  or `match: name`, where one of the two is an ordinary word. The comments at
  the top explain how matching works. The comments beside Target, UPS, Arm and
  Morgan Stanley say why each is set the way it is.
- [sources.yaml](../config/sources.yaml) lists the feeds, their weights, and
  which are switched off and why.

There are no keywords. A story that names no followed company reaches a
section because the sorting read it and judged that it belonged there.

**From your computer.** Edit the file, then commit and deploy. There is
nothing to migrate, because the service reads the file as it is.

**From Telegram**, without a deploy:

```
/watchlist add industrials-defense PLTR Palantir
/watchlist add semis-ai Tokyo Electron        (no ticker: followed by name)
/watchlist remove consumer-retail TGT
/watchlist edits                              (what has changed here)
/watchlist reset                              (drop every change made here)
/sources off yahoo-finance
```

A ticker given without a name takes the name the SEC files it under, without
the "Inc" and "Corp". These changes are kept on the server's disk, on top of
the files. `/watchlist` marks the companies added this way.

**Bringing Telegram changes into the files.** Run this from the root of the
repository:

```
scripts/sync-from-fly.sh
```

It copies the server's data into `./data`. Then it writes the changes into
`config/companies.yaml` and `config/sources.yaml`, touching only the lines they
change. Read them with `git diff config/`, then commit and deploy. When the
service next starts, it sees that the files now say what its changes said, and
drops those changes. So the list kept on the server only ever holds what the
files don't say yet. You never have to edit the files by hand to catch up with
Telegram.

## News search

When `TAVILY_API_KEY` is set, each brief searches for the news as well as
reading the feeds. It runs three general searches (markets, the economy, Asia)
and one for each sector, worded from the first sentence of its description.
Each search is limited to the publications listed in
`internal/search/outlets.go`. The results join the feeds before duplicates are
removed. They are then rated, ranked and cited like any other article. Their
source ids start with `web:`, as in `web:reuters.com`.

Each brief also searches for why a share moved, when a watchlist share moved
at least three percentage points more than the S&P 500 fund. The search reads
like "Why did McDonald's (MCD) shares fall?". There are at most five a brief,
the biggest moves first. To find them, every watchlist share is priced at the
start of the run. That takes about two minutes at Finnhub's free pace, and runs
alongside the feeds. The log line `searched movers` names them. The same prices
give each section its line of biggest moves under the heading.

**What a search returns.** Tavily sends back a short passage from each
article, chosen as the most relevant part. It doesn't send the whole page or a
summary written by a model. The service keeps the headline and the first 400
characters of that passage.

**Cost.** Each search costs one credit. In practice:
- a brief uses about 20 (fifteen searches, plus up to five for movers);
- the closer look uses one for each company it judges, so up to six a day and
  up to sixteen more on a Monday;
- each `/analyse` uses two, searching the company's last month;
- each `/now` costs about as much as a brief.

On 2 October 2026 that came to 32 credits in the day: the brief's 20, the
closer look's 6 and three analyses at 2 each. A month of weekdays comes to
about 640, before `/now` and `/analyse`. The free plan gives 1,000 a month.

`market-watch --check` proves the key without spending a credit. It shows
Tavily's count of credits used, but that count runs late: on 23 September it
still read 0 after 27 searches. `/stats` shows the credits each brief spent, as
each search reported them. When the month's credits run out, the log says so
and the brief carries on from the feeds. If that keeps happening, `/stats`
lists `tavily-search` among the sources that fail often.

**What it is for.** The media feeds are the part of the source list that
breaks. Several outlets worth reading (Reuters, Bloomberg, the WSJ, Nikkei
Asia) have no feed at all. Search doesn't replace the government and company
feeds. Those carry the release itself. Search only finds articles about it,
later, if anyone wrote one.

**Deciding whether it can replace the media feeds.** Both run side by side,
and `/stats` has a "News search" block. After two weeks, read its last line,
"Cited stories search did not find, by source".

- If it names only government and company sources (`bls-*`, `fed-*`,
  `fedreg-*`, `fda-*`, `dod-*`, `eia-*`, `sec-*`, and the newsrooms), search
  is finding everything the brief uses from the media feeds. Turn those feeds
  off one at a time with `/sources off <id>`.
- If media feeds keep appearing (`cnbc-*`, `ft-home`, `cna-*`,
  `straitstimes-business` and so on), search is missing stories the brief
  relies on. Keep those feeds, or add their outlets to `outlets.go` if they
  are missing from it.

The first comparison, on 23 September 2026, found that the two mostly found
different things. Of the past day's 387 stories, 120 came from search alone,
246 from the feeds alone and 21 from both. Search found 8 of CNBC's 30 stories
and none of the Guardian's 27. That count includes stories the brief never
uses. That is why the decision rests on citations over a fortnight, not on one
day's overlap.

The first brief written with search, on 24 September 2026, was compared with
one written from the feeds alone, from the same news. With search it cited 109
stories against 90, and 41 of them were found by search alone. It also caught a
wrong figure the feeds had carried. They gave Disney+ at $27.50 a month, where
Bloomberg and Deadline both said $21.49.

Two things were fixed after it. First, four of those 41 were pages rather than
stories: a section front, a live blog, and two programme recordings. Search now
skips pages like those. Second, with more to choose from, the brief dropped
Singapore's inflation figure, which both feed-only briefs had carried. Its
instructions now say Singapore's own news keeps its place. That news comes from
the Straits Times and CNA feeds, not from search, so keep those feeds even if
the rest of the media feeds go.

To run that comparison again on your machine, which spends about fifteen
credits and sends nothing:

    SEARCH_LIVE=1 go test ./internal/search -run TestLive -v -timeout 5m

## A channel for other readers

Other people can read the brief in a Telegram channel. The bot posts there,
and readers can only read. The bot still takes commands from your own chats
alone, so the channel shares the brief and nothing else.

What goes to the channel:

- **The daily brief and its closer look**, together as one post, once the
  closer look is done. `--once --share` does the same for a brief run by hand.
- **`/share`** posts whichever of your brief or analysis arrived last. A brief
  from `/now` or `--once`, and every `/analyse`, stays in your chat until you
  share it, so you can read it first. The bot remembers only the latest one,
  and forgets it on a restart. An analysis ends with a short verdict. The
  channel's copy carries the same warning as the closer look: a model wrote it,
  nobody checked it, and it is not advice.

In the channel, only the first message of each post makes a sound. The rest
arrive silently. Nothing is deleted there. `REPLACE_PREVIOUS` clears your chat
only, so readers keep every day's brief.

Readers can't ask the bot for anything, and that is on purpose. Every brief and
analysis runs on your Claude subscription. Anthropic's terms don't allow other
people's requests to be routed through a Pro or Max plan. A brief written for
you and then posted is your own use. An analysis that someone else asked for
would not be. If readers ever need to run their own, that needs an API key,
paid for each call.

To set one up:

1. Create the channel in Telegram. A private one, with an invite link, is
   simplest.
2. Add the bot as an admin with the right to post messages.
3. Find the channel's id. Open the channel at <https://web.telegram.org/a/>.
   The address ends in `#-100` and a string of digits, and that whole number
   is the id. If it doesn't start with `-100`, put `-100` in front of the
   digits.
4. Put it in `.env` as `TELEGRAM_CHANNEL_ID=-100...`. On Fly, run
   `scripts/fly-deploy.sh` again, which sends it as a secret.
5. `--check` says `channel ok` with the channel's title, or says what is
   missing.

## Other chats that may send commands

The owner's chat (`TELEGRAM_MASTER_CHAT_ID`) is where the brief, the closer
look and every failure report go. It alone can use every command. You can let
in other chats of your own too, such as your own bots. List their ids,
separated by commas:

| Setting | May use |
|---|---|
| `TELEGRAM_COMMAND_CHATS` | `/analyse`, `/industry`, `/help`, `/stats`, `/usage`, `/scorecard`, `/schedule` |
| `TELEGRAM_CONTROL_CHATS` | all of those, plus `/now`, `/watchlist` and `/sources` |

- Each reply goes to the chat that asked. A `/now` from a control chat is
  written and delivered to that chat alone. Your copy of the day's brief, the
  channel, the closer look, and what the next brief counts as already covered
  are all left as they were.
- `/start`, `/clear` and `/share` stay with the owner. An analysis or industry
  that another chat asked for is not what `/share` posts.
- A listed chat that sends a command it may not use is told where that
  command works. A chat that isn't listed still gets no reply at all.
- These must be your own chats. Every command runs on your Claude plan, which
  is for your own use only (see "A channel for other readers").
- A bot only receives another bot's messages if both have turned on
  bot-to-bot messaging (Telegram's Bot API 10.0, May 2026).

Put the ids in `.env`, and on Fly run `scripts/fly-deploy.sh` again, which
sends them as secrets. A bot's chat id with this bot is the bot's own user id.
That is the number before the colon in its token.

## Deploying to Fly

The image includes Claude Code, installed from Anthropic's signed apt
repository. It runs every call through Claude Code, just as on a desktop. It
logs in with `CLAUDE_CODE_OAUTH_TOKEN`.

1. Install flyctl and log in with `fly auth login`.
2. On your own machine, run `claude setup-token`, and put the token in `.env`
   as `CLAUDE_CODE_OAUTH_TOKEN=...`.
3. Make sure `.env` has `TELEGRAM_MASTER_CHAT_ID`. Its earlier name,
   `TELEGRAM_CHAT_ID`, also works. It fixes which chat is the owner's. Without
   it, whoever sends `/start` first becomes the owner.
4. Stop any other copy of the service. Two copies reading one bot token fight
   over every message, and two schedulers send two briefs. A local `--once` or
   `LIVE_BRIEF` run is fine. A `market-watch` left running is not, and neither
   is a container left over from testing the image (`docker ps` shows one). On
   the server, `telegram poll failed ... 409 Conflict` in the logs every ten
   seconds means another copy is running somewhere.
5. From the root of the repository, run `scripts/fly-deploy.sh`. It creates
   the app and its volume if they are missing. It sets the secrets from `.env`
   without printing them, deploys, and runs `--check` on the machine.

To deploy again later, run the same script.

The machine serves one thing to the internet: the pages the summaries link
to, at `https://joseph-market-watch.fly.dev/r/<id>` (`PAGES_URL` and
`[http_service]` in [fly.toml](../fly.toml)). Anything else gets "not found".
A new app also needs public addresses before the pages can be reached:
`fly ips allocate-v6` and `fly ips allocate-v4 --shared`.
`auto_stop_machines = "off"` in fly.toml stops Fly's proxy from shutting the
machine down, and the scheduler with it, when nobody is reading a page.

To go back to full messages, remove `PAGES_URL` from fly.toml and deploy. The
`[http_service]` block can go too. Pages are kept in `/data/pages` and deleted
after 30 days. Deleting one by hand (`rm /data/pages/<id>.html`) takes it down
at once.

### Moving the data down

`scripts/sync-from-fly.sh` copies the volume's files into `./data`, so a local
run starts where the server is. It also writes the Telegram changes into
`config/` (see "Changing the watchlist" above). Anything it replaces is kept in
`data/.backup/`.

`scripts/sync-cache-from-fly.sh` copies the latest runs' data into
`./data/cache` (see "The latest run's data" above).

### Moving the data up

A new volume starts empty. The lists come from `config/`, but the service knows
nothing of what earlier briefs covered, so its first brief may repeat them. To
carry your local record over:

```
fly ssh sftp shell -a joseph-market-watch
put data/prefs.yaml /data/prefs.yaml
put data/covered.json /data/covered.json
put data/runs.json /data/runs.json
put data/candidates.json /data/candidates.json
```

Then give the files to the service's user and restart:

```
fly ssh console -a joseph-market-watch -C "chown 65532:65532 /data/prefs.yaml /data/covered.json /data/runs.json /data/candidates.json"
fly apps restart joseph-market-watch
```

### Looking at a run on the server

```
fly ssh console -a joseph-market-watch -C "ls /data/relay"
fly ssh console -a joseph-market-watch -C "cat /data/relay/<run>/ledger.md"
fly logs -a joseph-market-watch
```

On Windows, `fly logs --no-tail` can hang. Put `timeout 100` in front of it.

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → **6 Running it** → [7 Backlog](TASKS.md)

**Next:** [7 Backlog](TASKS.md), what isn't built yet and what is known to be weak.
