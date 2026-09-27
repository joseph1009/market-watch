# Running the brief and the analysis

Every model call the service makes goes through the **relay**. The call is
written to a file, answered, and the answer written beside it, with a ledger
saying what was asked and what came back. There is no other path: no API key,
no direct API calls.

Who answers is set by `RELAY_ANSWER`:

| `RELAY_ANSWER` | Who answers | Waits for a person | Used for |
|---|---|---|---|
| `claude` (default) | Claude Code, run headless, one fresh process per call | No | The service as deployed, and any run from a terminal |
| `session` | Whoever writes the reply file, usually a Claude Code session using subagents | Yes | A run you want to watch or answer yourself |

Both draw on your Claude subscription, not on API credit.

A **pre-written run** hands the delivery a reply written earlier, asks nothing
and waits for nothing. It exists to check formatting and delivery.

## The stages

A brief makes four kinds of call, its closer look three more, and an analysis
one:

| Stage | What it does | Prompt | Default model | Override | Reply format |
|---|---|---|---|---|---|
| `triage` | Rates every article 1-5 and places it in up to two sectors | `triage.system` | Sonnet | `MODEL_TRIAGE` | `number\|rating\|watchlist ids` |
| `review` | Checks where the sorting put everything that could reach the brief, and moves what belongs elsewhere, **with web search** | `review.system` | Sonnet | `MODEL_REVIEW` | `number\|section ids`, one line per move |
| `brief` | Writes the brief | `brief.system` | Opus | `MODEL_BRIEF` | `## OVERVIEW` then `## SECTION: <id>` blocks |
| `names` | Names the companies the day was about that no watchlist tracks | `discover.system` | Haiku | `MODEL_NAMES` | `name\|ticker\|exchange\|article numbers\|what happened` |
| `ideas` | Researches new names worth a closer look, from the brief and the market's largest moves, **with web search** | `ideas.system` | Opus | `MODEL_IDEAS` | `name\|ticker\|exchange\|news or connected\|article numbers\|how the news bears on it` |
| `screen` | Chooses the followed companies whose move and news do not fit, from a table of all of them | `screen.system` | Sonnet | `MODEL_SCREEN` | `ticker\|what does not fit` |
| `verdicts` | Gives each of them BUY, HOLD or SELL from the facts fetched for it, five a call | `verdicts.system` | Opus | `MODEL_VERDICTS` | `=== symbol` blocks of `VERDICT:`, `CONFIDENCE:`, `CHANGED:`, `MOVE:`, `REACTION:`, `CASE:`, `NUMBERS:`, `RISK:` |
| `analysis` | Writes up one company's accounts for `/analyse` | `analysis.system`, the method, and `analysis.related` | Opus | `MODEL_ANALYSIS` | Plain text with capitalised headings |

Sorting sends the day's articles in batches of 60, two at a time
(`RELAY_CONCURRENCY`). When a person is answering, the batches are 150 each, so
there are fewer files to deal with.

Sorting was Haiku until the keywords went (24 September 2026). With nothing but
company names matched by rule, where an article goes rests on reading it
against the sector descriptions, and Sonnet reads less literally; the review is
the check on that reading. `REVIEW=false` turns the review off.

`ideas` and `review` are the only stages with tools: web search and reading
pages, and nothing else, no shell, no files and no connectors. The review is
told to search only to learn what an unfamiliar company does. Every other stage
runs with none. A subagent answering either by hand needs web access too.

## The prompts

Every instruction sent to a model is in
[config/prompts.md](../config/prompts.md), one section per
`=== id ===` line. The analysis also carries
[config/method.md](../config/method.md), the
playbook for reading accounts.

Editing the prose needs no Go. What has to survive an edit are the markers the
replies are parsed by: `## OVERVIEW`, `## SECTION:`, the pipe-delimited lines,
the rating format. The loader checks for them at startup and refuses to run
without them, so a broken prompt fails at once, not at delivery.

`PROMPT_FILE=/path/to/prompts.md` uses another copy without rebuilding.

To see exactly what a stage was sent, open its request file in the run
directory. It is the whole call: the `===== SYSTEM =====` block, then the
`===== PROMPT =====` block.

## Where a run's files go

Each brief and each analysis gets its own directory under `RELAY_DIR`
(`DATA_DIR/relay` by default). The last forty are kept.

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

In the ledger, `- [ ]` is a call waiting for an answer, `- [x]` one answered,
and `- [!]` one that failed or was abandoned, with the reason.

## The latest run's data

The relay keeps the model calls. `DATA_DIR/cache` keeps everything else the
latest run of each kind was made from, and what it sent, so a change can be
worked out from the last run's data instead of a new run. Each folder is
emptied when a run of its kind starts, so it holds only the latest one.

```
data/cache/brief/
  run.json              kind, when it started and finished, why it failed, the files
  prices.json           every followed share's quote
  movers.json           the shares that moved well beyond the market
  filings.json          the SEC filings, as articles
  search.json           what the news searches found, and the credits they used
  collected.json        every article that arrived, the ones kept, the ones the cap cut
  review.json           what the review moved
  articles.json         what the brief was written from, rated and placed
  levels.json           the market levels block
  trends.json           the movers' price history
  report.json           the brief as parsed from the model's reply
  messages.html         the messages as sent
  model/                every model call's request and reply, as in the relay
data/cache/analysis/
  run.json              "subject" is the ticker
  snapshot.json         the accounts, business, filings, news, forecasts, release
  related.json          the companies to read beside it, as checked
  messages.html
  model/
data/cache/recommendations/
  run.json
  look.json             the brief it follows: its text, citations, new names
  market-movers.json    the day's largest moves, from Massive
  research.json         the new names proposed
  screen-rows.json      each followed company's move and news, as screened
  screen.json           the followed companies picked
  backdrop.json         commodities, the dollar, rates
  facts.json            the facts each company was judged on
  verdicts.json         every verdict, shown or not
  facts-2.json, verdicts-2.json   the stand-ins' round, when there was one
  shown.json            what was shown
  messages.html
  model/
```

A closer look sent straight after a brief (`--once --share`) still writes its
own folder: its model calls go to `recommendations/model/`, not the brief's.
Every credential in the configuration is scrubbed out of what is written.

To bring the server's down, from the repository root:

```
scripts/sync-cache-from-fly.sh                  # all three
scripts/sync-cache-from-fly.sh analysis         # or one or two of them
```

Each folder fetched replaces the local one. A local run writes to the local
`data/cache` the same way.

## Running one by hand

A brief, answered by Claude Code, sent to the chat:

```
go run ./cmd/market-watch --once
```

The same from the tests, which is also how an analysis is run:

```
LIVE_BRIEF=1 go test ./internal/app -run TestLiveBrief -v -timeout 1h
LIVE_ANALYSIS=MU go test ./internal/app -run TestLiveAnalysis -v -timeout 1h
```

`go run ./cmd/market-watch --check` confirms the bot, the feeds, the search key
and that Claude Code is installed, and shows which model will answer each
stage. It asks no model anything and spends no search credits.

## Answering a run yourself

Set `RELAY_ANSWER=session` and each call waits for its reply file:

```
LIVE_BRIEF=1 RELAY_ANSWER=session go test ./internal/app -run TestLiveBrief -v -timeout 3h
```

Add `RELAY_AT=08:30` for a **prepared run**: started the night before, it
holds until 08:30, collects the news, and leaves its requests waiting for the
next time a session is open. Set `-timeout` to cover the hold.

The rule: **the answering session never reads a large request itself.** A day's
sorting is 40KB a batch, and the brief's request is the largest of all. Read
those in the main session and there is no room left for the work.

1. Read `ledger.md` in the newest run directory. It says which stages are
   waiting and how large each is.
2. For each waiting request, spawn one subagent with the model that stage
   would get (Sonnet to sort and review, Haiku to spot names, Opus to write),
   told to:
   - read that one request file and nothing else,
   - write the reply file in the format the request's own `===== SYSTEM =====`
     block asks for,
   - append one line to `ledger.md` saying what it did, and
   - report back a single line.
3. Batches can be answered in parallel; they are independent.
4. When a stage's reply lands, the run carries on by itself within two seconds.

The reply format is defined in each request's own system block, which is why a
subagent needs no other context.

### When the session's context is compacted

It will be, on a long run. The design assumes it:

- **Everything is on disk.** Each request and reply is a file, written before
  the next stage starts. Compaction loses the reasoning, never the files.
- **The ledger is the memory.** After a compaction, an interruption or a
  restart, read `ledger.md`.
- **Digests, not transcripts.** A subagent that has just rated 150 headlines
  should leave one line behind, such as "rated 150, 12 at 4 or higher, mostly
  Fed and oil", not a summary of each.
- **Compact between stages**, not during one. The gap after a reply is written
  and before the next request appears is the safe moment.

If the answering session dies, nothing is lost but time: the Go process is
still waiting on its reply file. Open a new session, read the ledger, and carry
on.

## A pre-written run

```
CANNED_REPLY=analysis.txt FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestCannedAnalysis -v
```

This sends an analysis written earlier through the real `/analyse` delivery:
real filings, prices, rendering and chat, and no model call.

## Logging in

On a desktop where you have run `/login` in Claude Code, nothing more is
needed. On a server there is nobody to log in, so:

1. On your own machine, run `claude setup-token`. It opens the browser, then
   prints a token that lasts a year.
2. Set it as `CLAUDE_CODE_OAUTH_TOKEN` where the service runs.

The token uses your subscription, and calls count against your plan's usage
limits, the same ones your own Claude Code use draws on. It is for your use
only. The bot answers only the chat that registered it, so nobody else's
messages can use it.

`ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` are never passed to Claude Code,
even when they are set. Claude Code would otherwise prefer them to the
subscription.

## Worth a closer look

About half an hour after the daily brief, a second message follows it: twenty listed
companies, each with a verdict. Up to six are companies you follow, shown only
as BUY or SELL, and new names fill the rest. A followed HOLD is left out, and
the next new name the research found takes its place, so there are twenty
wherever the research found enough.

1. **New names** (`ideas`, Opus with web search) start from the brief, its new
   names, and the day's fifteen largest moves among US companies nobody
   follows and worth at least US$2bn, read from Massive (`MASSIVE_API_KEY`). The research picks companies
   of two kinds: ones the stories or the moves are about, and ones they bear on
   without naming, such as a supplier, a customer or a rival. It never picks a
   company you follow. Every ticker is checked against the exchange, and one
   that does not check out is dropped.
2. **Followed companies** (`screen`, Sonnet, no tools) are chosen from a table
   of all of them: each one's move today against the week, month, six months,
   year and year to date, where it sits against its averages and its year's
   range, the multiple of today's price on the next two years' forecasts, which
   way the forecasts moved in four weeks, the distance to the price target, and
   the day's articles that name it. It picks those where what changed and how
   the share moved do not fit each other, and none on a day with nothing to
   pick.
3. **Facts** are fetched for each, four at a time: a year of daily prices from
   the chart source on any of the fourteen exchanges, today's move, and for a
   company that files with the SEC, three years of accounts, what its price
   implies, the first part of its latest results release, and what analysts
   expect of it and what its insiders, short sellers and funds have done (from
   Nasdaq, `CONSENSUS`). A company with no SEC filings is judged on its price
   and trading alone, and the message says so.
4. **Verdicts** (`verdicts`, Opus, no tools, five companies a call, two calls at
   once) give BUY, HOLD or SELL over twelve months with a confidence; what the
   news changed and by how much; how far the share moved; whether that move
   overreacted, underreacted or matched the change; the case; the two to four
   numbers that decide it; and the biggest risk. The oil price, the dollar and
   the cost of money are set above them all. BUY and SELL mean at least five
   percentage points better or worse than the S&P 500 over the twelve months,
   in US dollars. **A followed company is shown only as BUY or SELL**: its
   HOLD is left out.

It starts twenty minutes after the brief and takes ten to fifteen, most of it
the research and the verdicts, so it arrives about an hour before the US open.
The research and the screen run side by side. The wait is kept on the data
volume (`pending-look.json`), so a restart or a deploy in that time delays it
rather than losing it; one more than six hours late is dropped. After `/now`,
or `--once`, it follows at once. `IDEAS=false` turns it off.

**Cost.** No search credits: the research searches with Claude's own web
search, not Tavily. On the plan, reckoned from the size of what it reads rather
than measured, it is about two and a half times the six-company closer look it
replaced, and the whole day about a third more than before. The ledger of a
`look` run in `relay/` has the actual sizes.

**Nasdaq is unofficial.** Its figures come from the endpoints Nasdaq's own
website reads, with no key and no agreement, like the chart source. They can
change or refuse without notice. `market-watch --check` asks for one company's
forecasts and says whether they came back; `CONSENSUS=false` stops asking,
and the verdicts and analyses go on without them.

**It goes to the channel with the daily brief.** Not through `/share`, which
only ever posts a brief or an analysis, and not from a brief asked for with
`/now`: a verdict reaches the channel on the scheduled run, or on `-once
-share`, or not at all.

This was owner-only until 23 September 2026, and the reason it was is still
the reason to be careful. Publishing buy and sell calls to other people is
investment advice to others: in Singapore that can be regulated activity, and
Anthropic's usage policy asks two things of AI-written advice given to others
— that a person reviews it, and that readers are told a model wrote it.

Posting automatically gives up the first. What carries the second is the note
at the head of the channel's copy (`channelNote` in
`internal/telegram/ideas.go`), which says a model wrote it, that nobody checked
it, that it is often wrong, and that it is not a recommendation to act. That
note is the whole basis on which the section is allowed out, and a test
asserts it is there. **If you ever remove it, put the section back to owner
only.**

If you want the review back without losing the channel, the change is small:
have `sendIdeas` deliver to the owner alone and let `/share` pass on the
closer look, so nothing reaches readers until you have read it.

**`/scorecard`** says how past verdicts have done. Every verdict is written to
`scorecard.json` on the data volume, and is measured from the first price after
it was given: the next session's open, for the share and for the S&P 500 fund
alike. The verdicts are written before the US open from the night's news, and
measuring from the close before would credit them with the move that news makes
at the open, which nobody reading them could have had. Once that session has
happened, `/scorecard` writes the entry down beside the verdict. Once a verdict
is a week old it is scored: how the share moved against the index since, in US
dollars, so a Tokyo share that rose while the yen fell is counted as a dollar
investor would have seen it. The BUYs and SELLs are also split by confidence,
and by how the company was found, to show whether either tells a better call
from a worse one. A BUY promises to beat the
index by five points over the year, and counts as on course while it is ahead
by at least that pace -- five points a year, pro rata, so about 0.4 points
after a month. A SELL, likewise behind. The verdicts are twelve-month calls, so read the
scorecard for a pattern over months, not for any one name.

## Changing the watchlist

What the brief follows is three files in [config/](../config/), read by the whole
service and compiled into it:

- [sectors.yaml](../config/sectors.yaml): the sections, in order, each described in
  plain words. The sorting and the review judge an article against the
  description, and the news search asks for its first sentence -- so that
  sentence names the sector's ground, and the ones after it say what else
  belongs there and what belongs in another section instead.
- [companies.yaml](../config/companies.yaml): the companies, by sector, each with its
  ticker, the names headlines use, and `match: ticker` or `match: name` where
  one of them is an ordinary word. The comments at the top say how matching
  works; the ones beside Target, UPS, Arm and Morgan Stanley say why they are
  set the way they are.
- [sources.yaml](../config/sources.yaml): the feeds, their weights, and which are
  switched off and why.

There are no keywords. A story that names no followed company reaches a section
because the sorting read it and judged it belonged there.

**From your computer.** Edit the file, then commit and deploy. There is nothing
to migrate: the service reads the file as it is.

**From Telegram**, without a deploy:

```
/watchlist add industrials-defense PLTR Palantir
/watchlist add semis-ai Tokyo Electron        (no ticker: followed by name)
/watchlist remove consumer-retail TGT
/watchlist edits                              (what has changed here)
/watchlist reset                              (drop every change made here)
/sources off yahoo-finance
```

A ticker given without a name takes the one the SEC files it under, less the
"Inc" and "Corp". These changes are kept on the server's disk, on top of the
files, and `/watchlist` marks the companies added this way.

**Bringing Telegram changes into the files.** Run, from the repository root:

```
scripts/sync-from-fly.sh
```

It copies the server's data into `./data` and writes the changes into
`config/companies.yaml` and `config/sources.yaml`, touching only the lines they
change. Read them with `git diff config/`, then commit and deploy. On its next
start the service finds the files saying what its changes said and drops them,
so the list kept on the server is only ever what the files do not yet say; you
never edit the files by hand to catch up with Telegram.

## News search

With `TAVILY_API_KEY` set, each brief searches for the news as well as polling
the feeds: three general searches (markets, the economy, Asia) and one per
sector, worded from the first sentence of its description, each restricted to the
publications in `internal/search/outlets.go`. The results join the feeds before
dedupe and are rated, ranked and cited like any other article. Their source ids
start with `web:`, as in `web:reuters.com`.

Each brief also searches for why a share moved, when one on a watchlist moved
at least three percentage points further than the S&P 500 fund — "Why did
McDonald's (MCD) shares fall?" — at most five a brief, furthest first.
Every watchlist share is priced at the start of the run to find them, which
takes about two minutes at Finnhub's free pace and runs beside the feeds. The
log line `searched movers` names them. The same prices give each section its
line of biggest moves under the heading.

**Cost.** One credit per search: about fifteen per brief, up to five more on
a day with movers, and two for each `/analyse`, which searches the company's
last month. The free plan is 1,000 credits a month; weekday briefs use about
330, up to 440 with movers, which leaves room for `/now` and a few hundred
analyses. The closer look spends none.
`market-watch --check` proves the key without spending a credit, and shows
Tavily's count of credits used, but that count runs late: on 23 September it
still read 0 after 27 searches. `/stats` shows the credits each brief spent, as
each search reported them.
When the month's credits run out, the log says so and the brief carries on
from the feeds; if it keeps happening, `/stats` lists `tavily-search` among the
sources failing regularly.

**What it is for.** The media feeds are the part of the source list that breaks,
and several outlets worth reading (Reuters, Bloomberg, the WSJ, Nikkei Asia)
have no feed at all. Search does not replace the government and company feeds:
those carry the release itself, and search only finds articles about it, later,
if anyone wrote one.

**Deciding whether it can replace the media feeds.** Both run side by side, and
`/stats` has a "News search" block. After two weeks, read its last line,
"Cited stories search did not find, by source":

- If it names only government and company sources (`bls-*`, `fed-*`,
  `fedreg-*`, `fda-*`, `dod-*`, `eia-*`, `sec-*`, the newsrooms), search is
  finding everything the brief uses from the media feeds. Turn those feeds off
  one at a time with `/sources off <id>`.
- If media feeds (`cnbc-*`, `ft-home`, `cna-*`, `straitstimes-business` and so
  on) keep appearing, search is missing stories the brief relies on. Keep those
  feeds, or add their outlets to `outlets.go` if they are missing from it.

The first comparison, on 23 September 2026, found the two mostly finding
different things: of the past day's 387 stories, 120 came from search alone,
246 from the feeds alone and 21 from both. Search found 8 of CNBC's 30 stories
and none of the Guardian's 27. That count includes stories the brief never
uses, which is why the decision rests on citations over a fortnight, not on
one day's overlap.

The first brief written with search, on 24 September 2026, was set beside one
written from the feeds alone, from the same news. With search it cited 109
stories against 90, 41 of them found by search alone, and it caught a wrong
figure the feeds had carried: Disney+ at $27.50 a month, where Bloomberg and
Deadline both said $21.49. Two things were fixed after it. Four of those 41 were
pages rather than stories (a section front, a live blog, two programme
recordings), which search now skips. And with more to choose from, the brief
dropped Singapore's inflation figure, which both feed-only briefs had carried,
so its instructions now say Singapore's own news keeps its place. That news
comes from the Straits Times and CNA feeds, not from search, so keep those
feeds even if the rest of the media feeds go.

To run that comparison again locally, spending about fifteen credits and
sending nothing:

    SEARCH_LIVE=1 go test ./internal/search -run TestLive -v -timeout 5m

## A channel for other readers

Other people can read the brief in a Telegram channel. The bot posts there;
readers can only read. It still takes commands from your chat alone, so the
channel shares the brief and nothing else.

What goes to the channel:

- **The daily brief**, by itself, as soon as it reaches you. `--once --share`
  does the same for a brief run by hand.
- **`/share`**, whichever of your brief or analysis arrived last. A brief from
  `/now` or `--once`, and every `/analyse`, stays in your chat until you share
  it, so you can read it first. The bot remembers only the latest one, and
  forgets it on a restart.

In the channel, only the first message of each post makes a sound; the rest
arrive silently. Nothing is deleted there: `REPLACE_PREVIOUS` clears your chat
only, and readers keep every day's brief.

Readers cannot ask the bot for anything, and that is deliberate. Every brief
and analysis runs on your Claude subscription, and Anthropic's terms do not
allow routing other people's requests through a Pro or Max plan. A brief that
was written for you and then posted is your own use. An analysis someone else
asked for would not be. If readers ever need to run their own, that needs an
API key, paid per call.

Setting one up:

1. Create the channel in Telegram. Private, with an invite link, is simplest.
2. Add the bot as an admin with the right to post messages.
3. Find the channel's id. Open the channel at <https://web.telegram.org/a/>:
   the address ends in `#-100` and a string of digits, and that whole number is
   the id. If it does not start with `-100`, put `-100` in front of the digits.
4. Put it in `.env` as `TELEGRAM_CHANNEL_ID=-100...`, and on Fly run
   `scripts/fly-deploy.sh` again, which sends it as a secret.
5. `--check` says `channel ok` with the channel's title, or says what is
   missing.

## Deploying to Fly

The image carries Claude Code, installed from Anthropic's signed apt
repository, and runs every call through it as on a desktop. It logs in with
`CLAUDE_CODE_OAUTH_TOKEN`.

1. Install flyctl and log in: `fly auth login`.
2. On your own machine, run `claude setup-token` and put the token in `.env` as
   `CLAUDE_CODE_OAUTH_TOKEN=...`.
3. Make sure `.env` has `TELEGRAM_CHAT_ID`. It pins the one chat the bot will
   answer; without it, whoever sends `/start` first becomes the owner.
4. Stop any copy of the service running elsewhere. Two copies polling one bot
   token fight over every message, and two schedulers send two briefs. A local
   `--once` or `LIVE_BRIEF` run is fine; `market-watch` left running is not,
   and neither is a container left over from testing the image: `docker ps`
   shows one. On the server, `telegram poll failed ... 409 Conflict` in the
   logs every ten seconds means another copy is running somewhere.
5. From the repository root, run `scripts/fly-deploy.sh`. It creates the app and
   its volume if they are missing, sets the secrets from `.env` without printing
   them, deploys, and runs `--check` on the machine.

Deploying again later is the same script.

### Moving the data down

`scripts/sync-from-fly.sh` copies the volume's files into `./data`, so a local
run starts where the server is, and writes the Telegram changes into `config/`
("Changing the watchlist", above). What it replaces is kept in `data/.backup/`.

`scripts/sync-cache-from-fly.sh` copies the latest runs' data into
`./data/cache` ("The latest run's data", above).

### Moving the data up

The volume starts empty. The lists come from `config/`, but the service knows
nothing of what earlier briefs covered, so its first brief may repeat them. To
carry the local record over:

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
