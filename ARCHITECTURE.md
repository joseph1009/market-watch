# The shape of the code

What every file does, what the functions in it are for, and how a day's work
travels through them.

This is the map. [RUNBOOK.md](RUNBOOK.md) is how to operate the thing;
[TASKS.md](TASKS.md) is what is not built yet.

---

## The one-paragraph version

The service wakes on a schedule, prices every watchlist share, pulls about
forty news feeds, runs about fifteen news searches — and one more for each
share that moved well beyond the market — and reads the SEC's recent filings,
throws away what is
stale or duplicated, has a small model rate
and file every article, has a large model write a brief from what survived,
renders that into Telegram messages and sends them to one chat. Then it posts
the same brief to a channel for other readers, and finally — for the owner
alone — researches a handful of companies the news bears on and gives each a
buy, hold or sell verdict. In between it answers commands in the chat, the
largest of which reads a company's SEC filings and writes them up. Every call
to a model goes through the **relay**: a file written to disk, answered by a
headless Claude Code process, and the answer written beside it.

---

## The map

| Package | What it owns |
|---|---|
| [cmd/market-watch](cmd/market-watch/) | The executable: flags, startup, shutdown |
| [internal/app](internal/app/) | The service itself. Wires everything together and owns the order things happen in |
| [internal/config](internal/config/) | Configuration from the environment, and preferences on the data volume |
| [internal/model](internal/model/) | The plain data everything else passes around. Depends on nothing |
| [internal/feed](internal/feed/) | Fetching RSS, parsing it, deduplicating, matching to watchlists, ranking |
| [internal/sec](internal/sec/) | EDGAR: recent filings as articles, and the annual report for `/analyse` |
| [internal/search](internal/search/) | Tavily: news found by searching, one search per watchlist, as articles |
| [internal/triage](internal/triage/) | The small model that rates and files every article |
| [internal/report](internal/report/) | Building the brief's prompt and parsing the brief back out |
| [internal/discover](internal/discover/) | "New names in the news", and checking every ticker against an exchange |
| [internal/ideas](internal/ideas/) | "Worth a closer look": research, verdicts, and the scorecard that grades them |
| [internal/fundamentals](internal/fundamentals/) | Reading XBRL accounts out of EDGAR and turning them into a table |
| [internal/prices](internal/prices/) | Share prices: a live quote feed, a daily-history chart source, company news |
| [internal/marketdata](internal/marketdata/) | FRED: yields, the curve, fed funds, VIX, inflation |
| [internal/telegram](internal/telegram/) | The Telegram API client, and all rendering into messages |
| [internal/relay](internal/relay/) | Every model call. Writes the request, runs Claude Code, keeps the reply |
| [internal/prompts](internal/prompts/) | One file holding every system prompt, checked at startup |
| [internal/history](internal/history/) | What earlier briefs covered, and what each run cost |
| [internal/logging](internal/logging/) | A log handler that scrubs secrets out of every line |

Dependencies point inward. `model` imports nothing of ours; `app` imports
nearly everything; nothing imports `app` except `main`.

---

## The flow of a day

This is the main event. `Serve` runs two goroutines — the scheduler and the bot
poller — and this is what the scheduler fires.

### 1. Waking up

[`RunScheduler`](internal/app/app.go#L534) recomputes the next run every time
rather than ticking on an interval, so the schedule stays pinned to 20:30 US
Eastern across a daylight-saving change. When the timer fires it calls
[`Publish`](internal/app/app.go#L268) → [`brief(ctx, share: true)`](internal/app/app.go#L275).

`brief` does three things before any work starts:

- takes `a.running`, a mutex, so a `/now` arriving mid-brief waits instead of
  starting a second run;
- calls [`Relay.Begin`](internal/relay/relay.go#L91), which creates a directory
  for this run and puts it on the context — every model call the run makes
  lands there, numbered in order;
- then calls [`sendReport`](internal/app/app.go#L317), which is the pipeline.

### 2. Gathering

[`collectPrices`](internal/app/prices.go#L29) starts first, in the background:
the twelve benchmark funds and every watchlist share, read from Finnhub by
[`prices.Client.Fetch`](internal/prices/prices.go#L54) at the free tier's pace
of about one a second. That is about two minutes for 95 shares, which is why it
runs beside the filings and searches rather than after them. It prices every
share, not only those in the news, so a share that moved with no story behind
it is still seen.

[`collectFilings`](internal/app/filings.go#L25) runs next, so filings arrive on
the same footing as news rather than being bolted on afterwards. It calls
[`sec.Client.Collect`](internal/sec/sec.go#L94), which looks each watchlist
ticker up in EDGAR's ticker index, reads its recent submissions, keeps only the
8-K item codes that matter ([`materialCodes`](internal/sec/sec.go#L252)), and
turns each into an `model.Article` via [`Filing.article`](internal/sec/sec.go#L160).

[`collectSearch`](internal/app/search.go#L26) runs next, for the same reason.
It builds the searches with [`search.Queries`](internal/search/queries.go#L56)
— three general ones (markets, the economy, Asia), then one per watchlist,
worded from the watchlist's sector sentence — and runs them through
[`search.Client.Collect`](internal/search/search.go#L93). Each is a Tavily news
search restricted to the publications in
[`search.Outlets`](internal/search/outlets.go#L44), reaching back to the
previous brief ([`searchSince`](internal/app/search.go#L56)), so Monday's
covers the weekend. Every result becomes an article whose source id is
`web:` plus the outlet's domain — `web:reuters.com` — and which ranks with that
outlet's weight. Search is an addition, like the filings: without a key, or on
a day Tavily is down, the brief comes from the feeds alone.

When the prices are in, [`movers`](internal/app/movers.go#L33) picks the
watchlist shares that moved at least three percentage points further than the
S&P 500 fund, in either direction: at most five, furthest first, and none from
a session the previous brief already reported. Measuring against the market
means a sell-off does not send a search for every share that fell with it.
[`searchMovers`](internal/app/movers.go#L71) asks why each moved —
"Why did MCDONALDS (MCD) shares fall today?", from
[`search.MoverQuery`](internal/search/queries.go#L125), with the company's name as
the SEC files it — and [`search.Merge`](internal/search/search.go#L273) joins the
results to the other searches'.

Then [`feed.Collect`](internal/feed/collect.go#L109) — the heart of the
gathering — runs this sequence:

1. [`Fetcher.Fetch`](internal/feed/fetch.go#L62) pulls every enabled feed
   concurrently. Each response goes through [`feed.Parse`](internal/feed/parse.go#L37),
   which handles RSS and Atom, bad charsets, HTML in summaries and a dozen date
   formats. A source that fails becomes a `SourceError` and does not stop the run.
2. The filings and search results are appended.
3. [`DropStale`](internal/feed/collect.go#L220) removes anything older than a week.
4. [`Dedupe`](internal/feed/collect.go#L243) removes the same story twice: first
   by canonical URL, then by title similarity using
   [`similar.go`](internal/feed/similar.go)'s token overlap, keeping whichever
   copy came from the better-weighted source. The kept copy remembers the
   sources of the copies folded into it (`Article.Also`), which is how the run
   record can tell a story only search found from one the feeds had too.
5. [`Match`](internal/feed/collect.go#L394) tags each article with the watchlists
   whose tickers, names or keywords it mentions.
6. [`Result.triage`](internal/feed/collect.go#L162) hands everything to
   [`triage.Triager.Triage`](internal/triage/triage.go#L90), which sends the
   articles to Haiku in batches of 60, two batches at a time, and gets back a
   rating of 1–5 and up to two watchlist placements for each. Articles rated 1
   that no watchlist claims are dropped here. A triage failure is logged and the
   run continues on keyword matches alone.
7. [`Limit`](internal/feed/collect.go#L590) ranks by [`score`](internal/feed/collect.go#L541)
   — rating, watchlist match, source weight, recency — and cuts to `MAX_ARTICLES`.

### 3. Context, not content

First, [`history.Store.Mark`](internal/history/history.go#L63) flags stories
earlier briefs already carried, so today's can say a story has moved rather than
reporting it again from scratch. They are marked, not dropped: silently removing
a running story would leave the reader with a development and no thread to hang
it on.

Then [`collectLevels`](internal/app/filings.go#L89) reads FRED via
[`marketdata.Client.Fetch`](internal/marketdata/fred.go#L93) for yields, the
curve, fed funds, the S&P, the VIX, and inflation: the consumer price index and
its core, asked for as the change from a year earlier.

The prices read at the start go to the brief whole. For the movers,
[`trendsFor`](internal/app/movers.go#L116) also reads each one's price history
from the chart source: its 50- and 200-day averages, its range over the year,
and the day's volume against its usual. That lets the brief say what kind of
move it was.

Both are best-effort. A failure here costs the anchor numbers, never the brief.

### 4. Writing

[`report.Generator.Generate`](internal/report/generate.go#L64):

- [`splitByCoverage`](internal/report/prompt.go#L82) decides which watchlists
  have enough news to deserve a section, and which are merely quiet.
- [`buildPrompt`](internal/report/prompt.go#L111) assembles the prompt: the
  market levels ([`renderMarketData`](internal/report/prompt.go#L332)), the
  prices with the movers' history ([`renderPrices`](internal/report/prompt.go#L385)),
  then each active section's articles under its line of biggest moves
  ([`sectionMoves`](internal/report/moves.go#L30)), then the general news the
  overview may draw on. Every article gets a citation number from
  [`numbering`](internal/report/prompt.go#L237). The writer is told the reader
  sees the moves line, so it explains the moves rather than listing them.
- The call goes through `Completer`, which is the relay.
- [`parseResponse`](internal/report/parse.go#L20) splits the reply on
  `## OVERVIEW` and `## SECTION: <id>` markers into a `model.Report`, and each
  section is given the same biggest moves the prompt showed.

### 5. New names

[`discover.Finder.Find`](internal/discover/discover.go#L64) asks Haiku which
companies the day's stories were about that no watchlist tracks. The reply is a
pipe-delimited table, parsed by [`parse`](internal/discover/discover.go#L135).

Then the part that matters: [`Finder.verify`](internal/discover/discover.go#L291)
checks every ticker against OpenFIGI through
[`FIGI.Verify`](internal/discover/verify.go#L74), confirming both that the
symbol exists on the exchange claimed and that the registered name is the same
company ([`SameCompany`](internal/discover/verify.go#L167)). A verification
failure returns nothing rather than unchecked tickers.

[`discover.Store.Note`](internal/discover/store.go#L54) counts how many days a
name has been running, and [`priceCandidates`](internal/app/prices.go#L155)
attaches each one's move on the day — US names from the quote feed, everywhere
else from the chart source.

### 6. Rendering and delivery

[`telegram.RenderWith`](internal/telegram/render.go#L87) turns the report into
Telegram HTML: the overview, each section under its line of biggest moves, the new names
([`renderCandidates`](internal/telegram/render.go#L604)), the quiet watchlists,
the source links and a footer of token counts. Citations become links via
[`linkCitations`](internal/telegram/render.go#L571). Paragraphs become bullets
([`bullets`](internal/telegram/render.go#L314)) and labels get emphasised
([`emphasizeLabel`](internal/telegram/render.go#L258)).

[`pack`](internal/telegram/render.go#L362) then lays the pieces out across
messages under Telegram's 4096-character cap, breaking between sections rather
than mid-thought, and never leaving a heading alone at the end of a message.

[`Client.SendReport`](internal/telegram/client.go#L111) sends them. Message ids
are recorded in prefs so the next run can delete them if `REPLACE_PREVIOUS` is on.

Afterwards: [`history.Store.Record`](internal/history/history.go#L89) marks what
this brief covered — only after delivery, because a brief that never arrived has
not covered anything — and [`history.Runs.Add`](internal/history/runs.go#L104)
records what the run cost and did, for `/stats`. When search is on,
[`recordSearch`](internal/app/search.go#L80) adds what it contributed: how many
kept stories no feed carried, how many of the brief's citations came from
search alone, and — by source — the cited stories no search found. The
citations are read back out of the prose by
[`Report.Referenced`](internal/model/report.go#L95). Those numbers are what
decides whether search can take over from the media feeds.

### 7. The channel

[`shareBrief`](internal/app/channel.go#L95) posts the same messages to the
channel, before the research starts, so readers are not kept waiting on minutes
of web searches whose result they will never see.

The channel is one-way. Its readers cannot reach `HandleMessage`; the bot takes
commands from the owner's chat alone.

### 8. Worth a closer look

[`sendIdeas`](internal/app/ideas.go#L50), owner only, under a 25-minute budget:

1. [`ideas.Researcher.Propose`](internal/ideas/ideas.go#L68) — Opus **with web
   search and web fetch**, the only stage with tools — names up to six companies
   today's news bears on. Their tickers are verified against OpenFIGI too.
2. For each, [`ideaFacts`](internal/app/ideas.go#L138) assembles what a verdict
   should rest on: for a US SEC filer, the same accounts table `/analyse` uses;
   for anything else, the price and trading history alone, and it says so.
3. [`ideas.Judge.Judge`](internal/ideas/judge.go#L27) gives each a BUY, HOLD or
   SELL with the case, the numbers and the risk.
4. [`telegram.RenderIdeas`](internal/telegram/ideas.go#L45) renders them.
5. [`recordVerdicts`](internal/app/ideas.go#L175) writes each verdict to the
   scorecard with the price at the time, so `/scorecard` can grade it against
   the S&P 500 once it is a week old.

---

## The other journeys

### Startup

[`main`](cmd/market-watch/main.go#L27) parses four flags — `--once`, `--share`,
`--check`, `--clear` — and calls [`run`](cmd/market-watch/main.go#L49), which:

- [`config.Load`](internal/config/config.go#L162) reads the environment (and
  `.env` via [`LoadDotEnv`](internal/config/dotenv.go#L22)), reporting every
  missing variable at once rather than one per run;
- [`prompts.Load`](internal/prompts/prompts.go#L61) checks the prompts file
  still carries every marker the parsers depend on — a missing section stops the
  process here rather than four hours later when a reply cannot be parsed;
- wraps the log handler in [`logging.New`](internal/logging/scrub.go#L35) so
  every line passes through the scrubber. This exists because `net/http` puts
  the request URL into connection errors, and the bot token lives in that URL;
- [`app.New`](internal/app/app.go#L118) builds the service, loading prefs from
  the data volume and seeding them on first run;
- installs a SIGTERM handler, so a brief in flight finishes its delivery.

`--check` runs [`runCheck`](cmd/market-watch/main.go#L149): Telegram, the
channel, the feeds, the search key, the schedule and Claude Code, each reported
separately. The search key is proved with
[`search.Client.Usage`](internal/search/search.go#L302), which costs nothing; a
test search would spend a credit. Tavily's count of credits used runs late, so
the run record keeps its own, from each search's reply. This
is what the deploy script runs on the machine afterwards.

### The bot loop

[`Client.Poll`](internal/telegram/updates.go#L77) long-polls `getUpdates` and
hands each message to [`HandleMessage`](internal/app/commands.go#L56), which
checks the sender is the owner and routes on the command:

| Command | Handler | What it does |
|---|---|---|
| `/start` | [`handleStart`](internal/app/commands.go#L132) | Registers the chat as the owner's, once |
| `/now` | [`handleNow`](internal/app/commands.go#L152) | A brief to the owner only; waits for `/share` |
| `/share` | [`handleShare`](internal/app/channel.go#L150) | Posts whatever arrived last to the channel |
| `/analyse` | [`handleAnalyse`](internal/app/commands.go#L429) | Reads a company's filings — below |
| `/scorecard` | [`handleScorecard`](internal/app/ideas.go#L223) | How the verdicts have done against the index |
| `/stats` | [`handleStats`](internal/app/commands.go#L540) | What recent runs found and did |
| `/watchlist` | [`handleWatchlist`](internal/app/commands.go#L235) | Add or remove a ticker, name or keyword |
| `/sources` | [`handleSources`](internal/app/commands.go#L331) | Turn a feed on or off |
| `/schedule` | [`handleSchedule`](internal/app/commands.go#L224) | When the next brief is due |
| `/clear` | [`handleClear`](internal/app/commands.go#L181) | Delete the bot's earlier messages |

### `/analyse <ticker>`

[`handleAnalyse`](internal/app/commands.go#L429), under its own budget so it
does not inherit whatever the caller's context has left:

1. [`fundamentals.Client.Fetch`](internal/fundamentals/metrics.go#L179) looks the
   ticker up in EDGAR, pulls five years of XBRL facts through
   [`xbrl.Client.Concept`](internal/fundamentals/xbrl.go#L84), and assembles a
   `Snapshot`. It handles both US GAAP and IFRS tag names
   ([`metrics.go`](internal/fundamentals/metrics.go#L59)), picks the filer's own
   reporting currency, prefers later filings over restated earlier ones
   ([`supersedes`](internal/fundamentals/metrics.go#L391)) and builds the current
   year so far beside the full years ([`buildYTD`](internal/fundamentals/metrics.go#L493)).
2. [`quoteFor`](internal/app/commands.go#L550) adds the share price, so filed
   figures become multiples.
3. [`AddBusiness`](internal/fundamentals/business.go#L39) pulls the business
   description out of the latest annual report;
   [`tradingFor`](internal/app/prices.go#L60) reads the daily price history and
   [`prices.Summarise`](internal/prices/history.go#L243) turns it into returns,
   moving averages, the year's range, VWAP and volatility;
   [`addNews`](internal/app/prices.go#L133) adds what has been written lately,
   filtered by [`Relevant`](internal/fundamentals/news.go#L70) to pieces that
   actually name the company. All three are best-effort.
4. [`Snapshot.Table`](internal/fundamentals/table.go#L46) lays the figures out as
   a fixed-width table, and [`Analyzer.Analyze`](internal/fundamentals/analyze.go#L45)
   sends it to Opus with [`method.md`](internal/fundamentals/method.md) — the
   house method for reading accounts — appended to the system prompt.
5. [`SplitRelated`](internal/fundamentals/related.go#L54) cuts the "companies to
   read next to it" table out of the prose, and
   [`VerifyRelated`](internal/fundamentals/related.go#L90) checks those tickers
   against OpenFIGI before any of them is shown.
6. [`RenderPlain`](internal/telegram/render.go#L504) and
   [`RenderRelated`](internal/telegram/related.go#L22) render it.

### Every model call

There is one path, and this is it. [`Relay.Begin`](internal/relay/relay.go#L91)
opens a run directory and puts it on the context.
[`Run.Ask`](internal/relay/relay.go#L220):

1. writes `NN-stage-request.txt` — the system prompt and the prompt;
2. notes it in the run's ledger, a markdown checklist;
3. hands it to an `Answerer`;
4. writes `NN-stage-reply.txt` beside it and ticks the ledger line.

The answerer is normally [`Claude`](internal/relay/answer.go#L64), which runs
`claude -p` as a fresh process per call with `--no-session-persistence`, the
system prompt in a temp file (Windows caps a command line at 32K characters),
and the working directory set to the run's own so the call sees no `CLAUDE.md`
and no project settings. `ANTHROPIC_API_KEY` is stripped from the child
environment ([`childEnv`](internal/relay/answer.go#L249)) so the subscription is
used rather than API credit.

Tools are off for every stage except `ideas`, which gets web search and web
fetch and nothing else — no shell, no files, no MCP. A headline in a feed should
not be able to steer a model into running a command.

The alternative answerer, [`Session`](internal/relay/answer.go#L283), waits for
a person to write the reply file. That is how a run is watched or answered by
hand.

---

## File by file

### cmd/market-watch

**[main.go](cmd/market-watch/main.go)** — the executable.
`main` parses flags; `run` loads config, checks the prompts, builds the logger
and the app, and dispatches on the flags; `runClear` deletes the bot's earlier
messages without the service running; `runCheck` reports on each moving part in
turn; `stageModels` formats which model answers which stage.

### internal/app

**[app.go](internal/app/app.go)** — the service struct and the brief pipeline.
`App` holds every collaborator, most of them nil-able so a missing key disables
one feature rather than the run. `New` builds it. `Prefs`/`UpdatePrefs` guard
the preferences behind a mutex. `SendReport`, `Publish` and `brief` are the
three entry points to a run. `sendReport` is the pipeline itself. `RunScheduler`
fires the daily brief; `Serve` runs it alongside the bot poller and returns when
either fails. `reportFailure` tells the owner when a brief failed. Helpers:
`groupSummary`, `sourceMode`, `watchedNames`, `newRelay`, `placedExamples`,
`clipRunes`, `namesRemembered`, `now`.

**[commands.go](internal/app/commands.go)** — the bot's commands.
`BotCommands` publishes the menu; `HandleMessage` checks the sender and routes;
`splitCommand` parses. One handler per command, as tabled above. `addTerm` and
`removeTerm` decide from a term's shape whether it is a ticker or a company
name; `isTicker` is that test. `renderWatchlists`, `renderSources`, `groupIDs`
and `escape` render the replies. `quoteFor` fetches one price for `/analyse`.

**[prices.go](internal/app/prices.go)** — everything price-shaped the app does.
`collectPrices` reads the benchmarks and every watchlist share.
`tradingFor` reads one listing's history for the analysis; `marketFor` returns
both the history and the last price from a single fetch, which is what gives a
non-US listing a price at all; `seriesFor` is the fetch; `summarise` turns a
history into figures and refuses a stale one. `addNews` attaches company news.
`priceCandidates` prices the new names, US from the quote feed and everywhere
else from the chart source. The budgets — `scanBudget` 150s, `quoteBudget` 90s,
`historyBudget` 25s, `newsBudget` 25s — live here.

**[movers.go](internal/app/movers.go)** — the shares that moved on their own.
`movers` picks those at least `moverGap` (three percentage points) beyond the
S&P 500 fund, at most five; `searchMovers` searches for why, naming each company
by `companyName` from the SEC index; `trendsFor` reads their price histories
side by side.

**[ideas.go](internal/app/ideas.go)** — "worth a closer look".
`sendIdeas` runs the whole sequence; `ideaFacts` assembles the fact sheet a
verdict rests on; `recordVerdicts` writes them to the scorecard; `lastClose`
reads the benchmark's price at the time; `handleScorecard` answers `/scorecard`;
`briefText` and `trackedNames` prepare the researcher's input.

**[channel.go](internal/app/channel.go)** — the channel for other readers.
`remember` and `latest` hold the last delivery in memory; `share` posts it once,
guarding against a double post; `shareBrief` is what the scheduler calls;
`handleShare` is the command.

**[search.go](internal/app/search.go)** — news search beside the feeds.
`collectSearch` runs the searches under a two-minute budget and logs what they
found and cost; `searchSince` is where they start — the previous brief, at
least a day and at most a week back; `recordSearch` writes the comparison with
the feeds into the run record, using `onlySearch` and `anySearch` to sort each
story by who carried it. `searchFailure` is the one id a failed round of
searches is recorded under.

**[filings.go](internal/app/filings.go)** — SEC filings and FRED.
`collectFilings` gathers filings as articles; `watchedTickers` is every symbol
across every watchlist; `SECSourceEntry` gives filings a source entry so they can
be scored like anything else; `collectLevels` reads FRED.

### internal/config

**[config.go](internal/config/config.go)** — the process configuration, read
entirely from the environment. `Load` reads and validates it; `NextRun` computes
when the next brief is due, skipping weekends; `ClockTime`/`ParseClockTime`/
`Next` handle the schedule time; the `envOr`/`envInt`/`envBool`/`envDuration`
family reads one variable each with a default.

**[prefs.go](internal/config/prefs.go)** — the user-editable preferences that
live on the data volume, because the bot rewrites them at runtime: watchlists,
sources, the chat id, the last brief's message ids. `LoadPrefs`, `Save`,
`MergeDefaults`, `Group`, `EnabledSources`, `normalize`.

**[defaults.go](internal/config/defaults.go)** — the watchlists and the roughly
forty feeds a fresh volume is seeded with.

**[migrate.go](internal/config/migrate.go)** — schema migrations for that file,
so an existing volume picks up new watchlists and dropped tickers without being
wiped. `CurrentSchemaVersion` is 6.

**[dotenv.go](internal/config/dotenv.go)** — reads `.env` for local runs.

### internal/model

Plain data, no behaviour to speak of, imported by everything.

**[article.go](internal/model/article.go)** — `Article`, plus `ArticleID` and
`CanonicalURL`, which strip tracking parameters so the same story from two
places deduplicates. `Also` holds the sources dedupe folded into an article,
and `Carriers` lists every source a story arrived from.
**[group.go](internal/model/group.go)** — `Group`, a watchlist: tickers, names,
keywords and a sentence of scope.
**[report.go](internal/model/report.go)** — `Report`, `Section` (with the
`Movers` shown under its heading), `Usage`.
`Referenced` returns the articles the prose actually cites, as opposed to
`Cited`, which is everything the model was offered.
**[candidate.go](internal/model/candidate.go)** — `Candidate`, a new name in the
news, and `Symbol`, which writes it as `700.HK` or `NVDA`.
**[idea.go](internal/model/idea.go)** — `Idea` and the verdict constants.
**[quote.go](internal/model/quote.go)** — `Quote`, with `Move` (the percentage)
and `Unit` (the currency); `Moves` writes a list of them as one line.
**[trading.go](internal/model/trading.go)** — `Trading`, what a share has been
doing: returns, averages, range, volume, volatility. `Stale` refuses a history
that stops weeks ago.
**[source.go](internal/model/source.go)** — `Source`, a feed.

### internal/feed

**[fetch.go](internal/feed/fetch.go)** — `Fetcher.Fetch` pulls every source
concurrently; `fetchOne` and `normalize` handle one; `SourceError` names which
feed failed without failing the run.

**[parse.go](internal/feed/parse.go)** — RSS and Atom into `Item`s. Most of this
file is defensive: `looksLikeHTML` catches a feed that has become a web page,
`stripComments` and `charsetReader` handle malformed XML and Latin-1,
`parseTime` tries a dozen date formats, `stripHTML` and `unescape` clean the
summaries. `Summarize` is exported so search snippets are cleaned and cut to
the same 400 characters as feed summaries.

**[collect.go](internal/feed/collect.go)** — the gathering pipeline.
`Collect` runs it; `Result` is what it reports. `DropStale`, `Dedupe`
(`dedupeByURL`, `dedupeByTitle`, `clusterByTitle`, `merge` — which also records
the folded copy's source in `Also` — and `better`), `Match`
(`mentionsTicker`, `mentionsWord`), `score` and `Limit`.

**[similar.go](internal/feed/similar.go)** — the title-similarity test dedup
uses: `titleTokens`, `stripOutletSuffix`, `similarity`, `sameStory`, and
`contradicts`, which stops "X buys Y" from merging with "X denies buying Y".

### internal/sec

**[sec.go](internal/sec/sec.go)** — EDGAR. `Client.Collect` turns recent material
filings into articles; `tickerIndex` maps symbols to CIKs; `recent` reads a
company's submissions; `materialCodes` keeps only the 8-K items that matter;
`LookupCIK`, `Recent`, `AnnualReport` and `BusinessSection` serve `/analyse`.

**[business.go](internal/sec/business.go)** — pulls Item 1 out of an annual
report's HTML: `businessText`, `plainText`.

### internal/search

The media half of the feed list is the half that breaks: outlets move feeds,
put them behind bot protection, or never offer one. This package asks Tavily
for the news instead. It does not replace the government and company feeds,
which carry the documents themselves rather than articles about them.

**[search.go](internal/search/search.go)** — the Tavily client.
`Client.Collect` runs the searches four at a time and keeps each article once
across them; `searchOne` sends one — a `news` search, `basic` depth (one
credit), twenty results, restricted to the outlets, over the past day or from
the previous brief's date — and turns the results into articles. A result with
no date is dropped, since that is a quote page or a section front rather than a
report, and so is a dated page that is not one story (see pages.go). `Usage` reads the month's credit use for free. `statusError` turns
Tavily's refusals into advice, including `ErrOutOfCredits` for its 432 and 433.
`parseDate` and `cleanSnippet` handle Tavily's date format and extracted text.

**[pages.go](internal/search/pages.go)** — `isStory` keeps a result only if it
is one report. It drops section and topic fronts, quote pages and live blogs by
their address, and the recording of a whole programme by its title, which is
only a show's name and a date ("Post Market Wrap: September 23, 2026"). A live
blog goes because its headline follows its latest entry, so the link stops
leading to what the brief cited. A video clip about one story is kept: in the
first brief written with search, three of them carried facts no article did.

**[outlets.go](internal/search/outlets.go)** — `Outlets`, the publications a
search may return, each with the name the brief shows and a weight on the feed
list's scale; `Domains` sends them as the restriction, and `Sources` gives the
scorer a weight for each. `IDPrefix` (`web:`) and `IsSearch` mark a source id as
a search result. `outletFor` maps an address to its outlet; `trimOutlet` takes
" - Reuters" off the end of a headline, which would otherwise make every
headline from one outlet look a little alike to the dedupe.

**[queries.go](internal/search/queries.go)** — `Queries` builds the round:
`General` first, then one search per watchlist from `groupQuery`, which uses the
sector sentence and falls back to the watchlist's names and keywords. `MaxQueries`
caps a round at twenty, so adding watchlists cannot run up the bill.
`MoverQuery` asks why one share moved, for ten results rather than twenty, and
`plainName` takes the corporate words off the name it files under;
`MaxMoverQueries` caps those at five a brief.

**[live_test.go](internal/search/live_test.go)** — with `SEARCH_LIVE=1`, runs the
real searches beside the real feeds and reports what each found that the other
did not. Spends about fifteen credits and sends nothing.

### internal/triage

**[triage.go](internal/triage/triage.go)** — `Triager.Triage` batches the day's
articles to a small model and applies what comes back. `systemPrompt` builds the
watchlist descriptions the model files against; `parse` reads the
`number|rating|watchlist ids` replies; `apply` writes the ratings and placements
onto the articles.

### internal/report

**[generate.go](internal/report/generate.go)** — `Generator.Generate`: choose the
sections, build the prompt, make the call, assemble the `Report`.
**[prompt.go](internal/report/prompt.go)** — `buildPrompt` and its parts, plus
the thresholds that decide what is worth writing about: `MinSectionArticles` 3,
`MaxSectionArticles` 25, `MinGeneralRating` 4, `MinSectionRating` 3. `market`
carries what was measured rather than written.
**[moves.go](internal/report/moves.go)** — `sectionMoves` picks a watchlist's
biggest moves on the day, at least `MinMoveShown` (1%) and at most
`MaxMovesShown` (four); `trend` describes a mover against its own history.
**[parse.go](internal/report/parse.go)** — `parseResponse` splits the reply on
its markers.

### internal/discover

**[discover.go](internal/discover/discover.go)** — `Finder.Find`: ask, parse,
merge duplicates, remove names already watched, verify, apply the evidence bar
(`withEvidence`), cap.
**[verify.go](internal/discover/verify.go)** — `FIGI.Verify` checks tickers
against OpenFIGI; `Exchanges` is the fourteen exchange codes; `SameCompany`
compares a registered name to a claimed one, ignoring corporate forms.
**[store.go](internal/discover/store.go)** — `Store.Note` counts how many days a
name has been running; `Save`; sixty days of retention.

### internal/ideas

**[ideas.go](internal/ideas/ideas.go)** — `Researcher.Propose` runs the web
search stage; `researchPrompt`, `parseIdeas`, `verify`.
**[judge.go](internal/ideas/judge.go)** — `Judge.Judge` turns facts into
verdicts; `judgePrompt`, `parseVerdicts`, `normaliseVerdict`.
**[scorecard.go](internal/ideas/scorecard.go)** — `Record`, `Scorecard.Add`,
`Due` (which verdicts are old enough to grade), and `Summary`, which measures
each against the S&P 500. `MinAge` is a week.

### internal/fundamentals

**[metrics.go](internal/fundamentals/metrics.go)** — the accounts. `Client.Fetch`
builds a `Snapshot`; the `concept` tables map a figure to its US GAAP and IFRS
tag names; `buildYears`, `buildBalance`, `buildYTD`; `supersedes` prefers a later
filing to a restated earlier one; `reportingCurrency` picks the filer's own.
**[xbrl.go](internal/fundamentals/xbrl.go)** — the EDGAR XBRL client.
`Client.Concept` reads one concept's history; `Annual`, `Quarterly`, `Instant`
filter observations by period; `Tags` and `Search` list what a filer reports.
**[table.go](internal/fundamentals/table.go)** — `Snapshot.Table` lays the figures
out in fixed-width columns; `valuation`, `trailing`, `revenueGrowth`.
**[analyze.go](internal/fundamentals/analyze.go)** — `Analyzer.Analyze` makes the
call.
**[method.md](internal/fundamentals/method.md)** / **[method.go](internal/fundamentals/method.go)**
— the house method for reading accounts, embedded into the system prompt.
**[market.go](internal/fundamentals/market.go)** — renders the trading history
and the news into the prompt's prose.
**[news.go](internal/fundamentals/news.go)** — `AddNews` and `Relevant`, which
keeps only pieces that actually name the company and spreads them across days.
**[business.go](internal/fundamentals/business.go)** — `AddBusiness`.
**[related.go](internal/fundamentals/related.go)** — `SplitRelated`,
`VerifyRelated`, `RelatedFor`.
**[compute.go](internal/fundamentals/compute.go)** — a small expression
evaluator, and `Number`, which formats a figure for a reader.

### internal/prices

**[prices.go](internal/prices/prices.go)** — the Finnhub quote client.
`Client.Fetch` paces a list of symbols, up to `maxSymbols` (150); `Benchmarks`
is the twelve funds, and `MarketSymbol` the one a share's move is measured
against; `Index` and `LabelFor` serve rendering.
**[history.go](internal/prices/history.go)** — the keyless chart source.
`History.Fetch` returns a `Series` of daily bars in the local currency;
`Latest` turns the last two closes into a quote; `Summarise` turns the whole
series into a `model.Trading`.
**[symbols.go](internal/prices/symbols.go)** — `ChartSymbol` spells a listing the
way the chart source does: `0700.HK`, `7203.T`, `BRK-B`. Empty for an exchange
it does not cover, rather than a guess that would return another company's
prices.
**[news.go](internal/prices/news.go)** — `News.Company` reads company news.

### internal/marketdata

**[fred.go](internal/marketdata/fred.go)** — `Client.Fetch` reads the
`DefaultSeries` — ten-year and two-year yields, the curve, fed funds, the S&P,
the VIX, and inflation — and returns a `Reading` for each with its latest,
previous and week-ago values. A series can ask FRED for a transform (`Units`:
`pc1`, the change from a year earlier, turns the price index into inflation),
and a `Monthly` one is compared with last month and has no week-ago value.

### internal/telegram

**[client.go](internal/telegram/client.go)** — the API client. `SendMessage`,
`Send`, `SendReport`, `Broadcast`, `DeleteMessages`, `SweepMessages`, `Me`,
`CanPost`. `do` handles rate limits and retries; `redact` keeps the bot token
out of errors.
**[updates.go](internal/telegram/updates.go)** — `Poll` long-polls for messages;
`SetMyCommands` publishes the menu; `DrainUpdates` discards commands sent while
the process was down.
**[render.go](internal/telegram/render.go)** — the brief as messages. `RenderWith`
assembles the segments, `pack` lays them across messages, `bullets` and
`emphasizeLabel` shape the prose, `linkCitations` turns `[3]` into a link, each
section's heading carries its line of biggest moves,
`renderCandidates` draws the new-names block, `renderSources` the links,
`renderFooter` the token counts. `RenderPlain` does the same for an analysis.
**[ideas.go](internal/telegram/ideas.go)** — `RenderIdeas` and `renderIdea`, the
closer look's layout.
**[related.go](internal/telegram/related.go)** — `RenderRelated` and
`RelatedList`, shared by the analysis and anything else with a list of companies.

### internal/relay

**[relay.go](internal/relay/relay.go)** — the run. `Relay.Begin` opens a
directory; `prune` keeps the last forty; `Run.Ask` writes the request, gets it
answered and writes the reply; `Run.Note` maintains the ledger. `Stage` and
`Plain` adapt a relay to the `Completer` interfaces the other packages expect.
**[answer.go](internal/relay/answer.go)** — who answers. `Claude.Answer` runs
`claude -p`; `DefaultModels` maps a stage to Haiku or Opus; `webStages` is the
one stage with tools; `childEnv` strips the API key. `Session.Answer` waits for
a person.

### internal/prompts

**[prompts.go](internal/prompts/prompts.go)** — every system prompt in one
embedded file. `Get` reads a section, `Render` fills a template, `Check`
verifies at startup that each section still carries the markers its parser
depends on. `PROMPT_FILE` points at an external copy for editing without a
rebuild.

### internal/history

**[history.go](internal/history/history.go)** — what earlier briefs covered.
`Store.Mark` flags repeats, `Seen` counts them, `Record` writes them. Three weeks
of retention.
**[runs.go](internal/history/runs.go)** — `Runs.Add` records a run; `Summary`
renders `/stats`. The last thirty are kept. `searchSummary` is the news-search
block of `/stats`: articles and credits per brief, stories only search found,
and the cited stories it missed by source, most-missed first.

### internal/logging

**[scrub.go](internal/logging/scrub.go)** — a `slog.Handler` that replaces every
known secret with `[redacted]` in every message, attribute and group, so a token
cannot leak through an error string nobody thought about.

---

## What lives on disk

On Fly this is the `market_watch_data` volume at `/data`; locally it is `./data`.

| File | Written by | Holds |
|---|---|---|
| `prefs.yaml` | [config/prefs.go](internal/config/prefs.go) | Watchlists, sources, the owner's chat id, the last brief's message ids |
| `covered.json` | [history/history.go](internal/history/history.go) | Which stories earlier briefs carried, three weeks back |
| `runs.json` | [history/runs.go](internal/history/runs.go) | The last thirty runs, for `/stats` |
| `candidates.json` | [discover/store.go](internal/discover/store.go) | New names and how many days each has been running |
| `scorecard.json` | [ideas/scorecard.go](internal/ideas/scorecard.go) | Every verdict and the price at the time |
| `relay/` | [relay/relay.go](internal/relay/relay.go) | The last forty runs: every request, every reply, a ledger each |

---

## Configuration

Everything is environment variables, read once by
[`config.Load`](internal/config/config.go#L162). The deployed values are in
[fly.toml](fly.toml); the secrets are Fly secrets, set from `.env` by
[scripts/fly-deploy.sh](scripts/fly-deploy.sh) without being printed.
[.env.example](.env.example) documents every one.

The shape of it: a missing **secret** usually disables a feature rather than
failing the run. No `FRED_API_KEY` means no market-levels block. No
`TAVILY_API_KEY` means no news searches — the brief comes from the feeds alone. No
`FINNHUB_API_KEY` means no US quotes, so no lines of biggest moves and no
searches for shares that moved, and no company news — but listings outside the
US are still priced for the analysis, because the chart source needs no key. No `USER_AGENT`
means no SEC filings, because EDGAR answers an anonymous request with 403.

---

## A few things worth knowing

**Nil means off.** Most of `App`'s fields are pointers that may be nil, and each
nil disables one feature. This is deliberate: a service missing one key should
lose one section, not refuse to start.

**The relay is the only way to a model.** There is no API client anywhere in
this repository. Every call is a file, a headless process, and a file back.

**Verification is not a nicety.** Any ticker the service shows a reader —
new names, closer-look companies, the analysis's related list — has been checked
against OpenFIGI first. A verification failure returns nothing rather than an
unchecked symbol.

**Order matters in `sendReport`.** Filings and searches before feeds, so they are
deduplicated with everything else. Prices after the articles, so a price outage costs no
prose. Clearing the last brief after generating the new one, so a failed run
does not also throw away what it failed to replace. Recording what was covered
after delivery, not before.

**A verdict published is not a verdict kept.** `sendIdeas` sends to the owner
always and to the channel when the brief went there, so the daily run shares
both and a `/now` shares neither. The channel's copy is rendered separately,
under a note saying a model wrote it, that nobody checked it and that it is not
advice to act on. That note is the condition the section is published under
rather than a formality — see `channelNote` in `internal/telegram/ideas.go` and
the section in RUNBOOK.md.
