**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → **4 Function by function** → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

---

# Market Watch, function by function

This page lists every function the app has. For each one it gives:

- **Starts from:** what triggers it.
- **Entry point:** where its code begins.
- **What it does:** a short summary.
- **Files:** the files involved.
- **Sequence:** the order its code runs in: which function, in which file, does what.
- **Links:** which other functions it feeds or reads from.

The line numbers were last checked on 3 October 2026. When a flow changes, update its entry here.

**How to use it:** read "At a glance" and "How they connect" first. Then pick
one function and follow its **Sequence** table. Click each step to go to the
code. Any word you don't know is in the [Glossary](GLOSSARY.md).

Companion documents:
- [ARCHITECTURE.md](ARCHITECTURE.md) tells the same code as the story of a day, then goes file by file.
- [RUNBOOK.md](RUNBOOK.md) says how to operate it.
- [TASKS.md](TASKS.md) is what isn't built yet.

---

## At a glance

| # | Function | Starts from | Entry point |
|---|---|---|---|
| 1 | [Start-up and the three loops](#1-start-up-and-the-three-loops) | The process starting (on Fly, the machine booting) | [`main`](../cmd/market-watch/main.go#L33) → [`Serve`](../internal/app/app.go#L851) |
| 2 | [The daily brief](#2-the-daily-brief) | The scheduler: 07:30 New York, weekdays | [`RunScheduler`](../internal/app/app.go#L820) → [`publishScheduled`](../internal/app/app.go#L415) |
| 3 | [Worth a closer look](#3-worth-a-closer-look) | Straight after the brief: the daily run, `/now`, `--once` | [`brief`](../internal/app/app.go#L425) → [`sendIdeas`](../internal/app/ideas.go#L88) |
| 3a | [Weekly themes](#3a-weekly-themes) | The first scheduled closer look of the week | [`runThemes`](../internal/app/themes.go#L72) |
| 3b | [Daily reactions](#3b-daily-reactions) | Every closer look | [`runReactions`](../internal/app/reactions.go#L68) |
| 3c | [Facts and verdicts](#3c-facts-and-verdicts) | Called by 3a and 3b | [`factsFor`](../internal/app/ideas.go#L281) → [`judge`](../internal/app/ideas.go#L231) |
| 4 | [Market history](#4-market-history) | A background loop, and before each closer look | [`RunMarket`](../internal/app/marketdata.go#L54), [`topUpMarket`](../internal/app/marketdata.go#L89) |
| 5 | [Scorecard](#5-scorecard) | Verdicts shown, `/analyse`, `/scorecard` | [`recordVerdicts`](../internal/app/ideas.go#L419), [`recordAnalysis`](../internal/app/commands.go#L810), [`handleScorecard`](../internal/app/ideas.go#L469) |
| 6 | [`/analyse`](#6-analyse) | Owner's command | [`handleAnalyse`](../internal/app/commands.go#L589) |
| 6b | [`/industry`](#6b-industry) | Owner's command | [`handleIndustry`](../internal/app/industry.go#L21) |
| 7 | [`/now`](#7-now) | Owner's command | [`handleNow`](../internal/app/commands.go#L247) |
| 8 | [The channel and `/share`](#8-the-channel-and-share) | The daily run; owner's command | [`shareBrief`](../internal/app/channel.go#L127) → [`sendTogether`](../internal/app/pages.go#L61), [`handleShare`](../internal/app/channel.go#L196) |
| 8b | [Pages](#8b-pages) | Every long delivery, when `PAGES_URL` is set | [`send`](../internal/app/pages.go#L39) → [`pages.Store.Publish`](../internal/pages/pages.go#L52) |
| 9 | [`/watchlist` and `/sources`](#9-watchlist-and-sources) | Owner's command | [`handleWatchlist`](../internal/app/commands.go#L330), [`handleSources`](../internal/app/commands.go#L413) |
| 10 | [The other commands](#10-the-other-commands) | Owner's command | [`HandleMessage`](../internal/app/commands.go#L70) |
| 11 | [Failure alerts](#11-failure-alerts) | A scheduled brief failing | [`reportFailure`](../internal/app/app.go#L1079) |
| 12 | [Model calls (the relay)](#12-model-calls-the-relay) | Every call to a model | [`Relay.Begin`](../internal/relay/relay.go#L98), [`Run.Ask`](../internal/relay/relay.go#L229) |
| 13 | [Run cache](#13-run-cache) | The brief, `/analyse`, the closer look | [`Cache.Start`](../internal/runcache/runcache.go#L97) |
| 14 | [Secret scrubbing](#14-secret-scrubbing) | Every log line and error reply | [`logging.New`](../internal/logging/scrub.go#L35), [`logging.Scrub`](../internal/logging/scrub.go#L47) |
| 15 | [Command-line modes](#15-command-line-modes) | `--check`, `--once`, `--once --share`, `--clear`, `--fold` | [`main`](../cmd/market-watch/main.go#L33) |
| 16 | [Deploy and sync scripts](#16-deploy-and-sync-scripts) | By hand | [scripts/](../scripts/) |
| 17 | [Live tests](#17-live-tests) | By hand, with an environment switch | `*_test.go` |

---

## How they connect

```
 process start
   main ─► run ─► config.Load, LoadPrompts, logging.New, app.New ─► Serve
                                                                      │
   ┌──────────────────────────────┬─────────────────────────────────────┴───────────┬──────────────────────────┐
   ▼                              ▼                                                 ▼                          │
 RunScheduler (2)              RunMarket (4)                                    Bot.Poll ─► HandleMessage     │
   │ 07:30 NY, weekdays          │ 10 days at a time                                 │                          │
   ▼                              ▼                                                 ├─ /now ─► SendReport ─► brief ─► sendReport ─► sendIdeas (reactions only, owner only)
 publishScheduled          data/market/*.json.gz                                    ├─ /analyse (asks which) ─► handleAnalyse ─► recordAnalysis ──┐
   │                              │                                                 ├─ /industry ─► handleIndustry ─► Explain        │
   ▼                              ▲   (2 years of bars)                             ├─ /scorecard ─► Due, Settle, Summary ◄──────┤
 brief ─► sendReport              │                                                 ├─ /share ─► share(latest delivery)          │
   │  (prices, calendar, filings, │ read by                                         ├─ /watchlist, /sources ─► prefs.yaml        │
   │   search, feeds, triage,     │                                                 └─ /start /help /schedule /stats /clear      │
   │   review, brief, new names)  │                                                                                              │
   ├─► owner's chat               │                                                                                              │
   ├─► sendIdeas ─────────────────┘                                                                                              │
   │     ├─ topUpMarket, listings, backdrop                                                                                     │
   │     ├─ runThemes (3a, weekly) ─┐                                                                                           │
   │     ├─ runReactions (3b, daily)┼─► factsFor, judge (3c)                                                                     │
   │     ├─ RenderPicks ─► owner                                                                                                 │
   │     └─ recordVerdicts ─► scorecard.json ◄──────────────────────────────────────────────────────────────────────────────────┘
   └─► shareBrief ─► channel: brief + closer look as one post, a button to each page (closer look under channelNote)
                                                                                  │
                                                          recentPicks (8-week no-repeat) ◄┘

 Every model call, from anywhere:  Relay.Begin (one folder a run) ─► Run.Ask ─► claude -p (Opus 5.5) ─► reply file
 Every run's data:                 runcache (data/cache/<kind>/)          Every log line and error reply: logging scrub
```

What passes between the functions:

- **Brief → closer look:** the brief's articles (`look.Cited`), passed in memory straight after the brief. The reactions need an article to explain a move, and the verdicts cite the articles.
- **Brief → weekly themes:** the headlines the briefs carried (`covered.json`, three weeks, via [`recentHeadlines`](../internal/app/themes.go#L234)) become each industry's "named in N recent headlines".
- **Market history → closer look:** the themes and reactions measure everything from the two years of daily prices in `data/market`.
- **Closer look and `/analyse` → scorecard:** every BUY or SELL shown is recorded, and `/scorecard` grades them. The weekly themes read the scorecard back. A company given the same verdict in the last eight weeks goes on the "earlier picks" list and is not shown again.
- **Brief and `/analyse` → `/share`:** the last delivery is remembered in memory, and `/share` posts it.

---

## 1. Start-up and the three loops

**Starts from:** the process starting. On Fly, the Docker `CMD` runs `market-watch` with no flags.

**Entry point:** [`main`](../cmd/market-watch/main.go#L33) → [`run`](../cmd/market-watch/main.go#L73) → [`App.Serve`](../internal/app/app.go#L851)

**What it does:** loads and checks the settings and builds the service. Then it runs three loops side by side until the program is told to stop.

**Files:** [cmd/market-watch/main.go](../cmd/market-watch/main.go), [internal/app/app.go](../internal/app/app.go), [config/config.go](../config/config.go), [config/prompts.go](../config/prompts.go), [config/prefs.go](../config/prefs.go), [internal/logging/scrub.go](../internal/logging/scrub.go)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`main`](../cmd/market-watch/main.go#L33) | Parses the flags `--once`, `--share`, `--check`, `--clear` and `--fold`. `--fold` goes straight to [`runFold`](../cmd/market-watch/main.go#L151) (see 15). |
| 2 | [`config.Load`](../config/config.go#L206) | Reads every environment variable, and `.env` on your machine. It reports all the missing ones at once. |
| 3 | [`config.LoadPrompts`](../config/prompts.go#L61) → [`CheckPrompts`](../config/prompts.go#L134) | Reads [prompts.md](../config/prompts.md). It stops the program if a section has lost a marker that a reply is read by. |
| 4 | [`logging.New`](../internal/logging/scrub.go#L35) | Wraps the log handler so every line has its secrets removed (see 14). |
| 5 | [`app.New`](../internal/app/app.go#L190) | Builds the `App`. It loads `prefs.yaml`, `covered.json`, `runs.json`, `candidates.json`, `themes.json` and `scorecard.json` from the data folder. It creates the Telegram, Finnhub, chart, FRED, Tavily, SEC, Massive and Nasdaq clients, and connects each model stage to the relay. A feature that is switched off leaves its field nil (`TRIAGE`, `REVIEW`, `DISCOVER`, `IDEAS`, `CONSENSUS`). |
| 6 | `signal.NotifyContext` in [`run`](../cmd/market-watch/main.go#L73) | SIGTERM cancels the context, so a brief that is being sent finishes its delivery. |
| 7 | [`Serve`](../internal/app/app.go#L851) | Publishes the command menu ([`BotCommands`](../internal/app/commands.go#L50) → [`SetMyCommands`](../internal/telegram/updates.go#L125)). It throws away commands sent while it was down ([`DrainUpdates`](../internal/telegram/updates.go#L131)). |
| 8 | [`Serve`](../internal/app/app.go#L851) | Starts three goroutines: **[`RunScheduler`](../internal/app/app.go#L820)** (2, and 3 after each brief), **[`RunMarket`](../internal/app/marketdata.go#L54)** and **[`Bot.Poll`](../internal/telegram/updates.go#L77)** → [`HandleMessage`](../internal/app/commands.go#L70) (6–10). With `PAGES_URL` set, there is a fourth, the page server **[`pages.Store.Serve`](../internal/pages/pages.go#L146)** (8b). The first one to fail stops the others. |

**Links:** everything else runs from these three loops. The `App` struct holds a lock called `running`, which lets only one brief or closer look run at a time.

---

## 2. The daily brief

**Starts from:** [`RunScheduler`](../internal/app/app.go#L820) at `REPORT_AT` (07:30) in `SCHEDULE_TZ` (America/New_York), weekdays only. [`config.NextRun`](../config/config.go#L396) works out the next time afresh each round, so daylight saving never shifts it. In Singapore that is 19:30 until 1 November.

**Entry point:** [`publishScheduled`](../internal/app/app.go#L415) → [`brief`](../internal/app/app.go#L425)`(share=true, scheduled=true)` → [`sendReport`](../internal/app/app.go#L512)

**What it does:** gathers the day's prices, SEC filings, news searches and about forty feeds. Opus 5.5 rates and files every article, and a second pass reviews where each one landed. Opus writes the brief, including what is coming up today and this week. It also spots new company names, and each one is checked against its exchange. The brief goes to the owner and the closer look follows at once. Then both go to the channel as one post.

**Files:**
- **App:** [app.go](../internal/app/app.go), [calendar.go](../internal/app/calendar.go), [prices.go](../internal/app/prices.go), [filings.go](../internal/app/filings.go), [search.go](../internal/app/search.go), [movers.go](../internal/app/movers.go), [channel.go](../internal/app/channel.go)
- **Packages:** [calendar](../internal/calendar/), [consensus](../internal/consensus/) (earnings), [feed](../internal/feed/), [sec](../internal/sec/), [search](../internal/search/), [triage](../internal/triage/), [history](../internal/history/), [report](../internal/report/), [discover](../internal/discover/), [prices](../internal/prices/), [telegram](../internal/telegram/)
- **Config:** [sectors.yaml](../config/sectors.yaml), [companies.yaml](../config/companies.yaml), [sources.yaml](../config/sources.yaml), [prompts.md](../config/prompts.md), [glossary.yaml](../config/glossary.yaml)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`brief`](../internal/app/app.go#L425) | Takes the run lock. Stops if no chat is registered. Opens the run cache (`brief`) and a relay run folder. |
| 2 | [`collectPrices`](../internal/app/prices.go#L46), in the background | Prices the 12 benchmark funds and every followed share from Finnhub ([`prices.Client.Fetch`](../internal/prices/prices.go#L71)), at about one a second. Whatever Finnhub misses comes from the charts ([`fromCharts`](../internal/app/prices.go#L71) → [`History.Fetch`](../internal/prices/history.go#L88)). |
| 3 | [`collectFilings`](../internal/app/filings.go#L25) → [`sec.Client.Collect`](../internal/sec/sec.go#L107) | Each followed company's recent important 8-Ks, as articles. |
| 4 | [`collectSearch`](../internal/app/search.go#L26) → [`search.Queries`](../internal/search/queries.go#L56) → [`search.Client.Collect`](../internal/search/search.go#L93) | Tavily news searches, the general ones plus one for each sector, reaching back to the previous brief ([`searchSince`](../internal/app/search.go#L56)). |
| 5 | [`movers`](../internal/app/movers.go#L35) → [`searchMovers`](../internal/app/movers.go#L73) → [`search.Merge`](../internal/search/search.go#L273) | Once the prices are in, up to 5 followed shares that moved at least 3 points more than the S&P 500 fund each get a "why did it move" search. |
| 6 | [`feed.Collect`](../internal/feed/collect.go#L115) | The gathering pipeline:<br>• [`Fetcher.Fetch`](../internal/feed/fetch.go#L62) reads every enabled feed at once;<br>• the filings and search results are added;<br>• [`DropStale`](../internal/feed/collect.go#L228) drops anything more than a week old;<br>• [`Dedupe`](../internal/feed/collect.go#L251) removes duplicates by URL and then by title;<br>• [`Match`](../internal/feed/collect.go#L404) tags articles by the company names in `companies.yaml`;<br>• [`Result.triage`](../internal/feed/collect.go#L170) → [`Triager.Triage`](../internal/triage/triage.go#L107) has **Opus** rate every article 1–5 and place it in up to 2 sectors, in batches of 60. An article sorted in the last eight days under the same sectors keeps its verdict from `sorted.json` ([`Memory`](../internal/triage/memory.go)) and isn't sent;<br>• [`Limit`](../internal/feed/collect.go#L597) ranks by [`score`](../internal/feed/collect.go#L548) and cuts to `MAX_ARTICLES`. |
| 7 | [`history.Store.Mark`](../internal/history/history.go#L63) | Marks stories that earlier briefs carried (`covered.json`), so the writer treats them as updates rather than news. |
| 8 | [`triage.TopUp`](../internal/triage/topup.go#L27) → [`Reviewer.Review`](../internal/triage/review.go#L62) | Sections with fewer than 10 articles are offered the ones rated 3. **Opus with the web** then checks every placement again. It keeps a top-up only if it names that section. |
| 2b | [`collectCalendar`](../internal/app/calendar.go#L38), in the background | What is due from now to the end of the week. [`ForexFactory.Week`](../internal/calendar/calendar.go#L41) → [`calendar.Key`](../internal/calendar/calendar.go#L109) keeps the US releases rated high or medium and the high ones from other large economies, with their forecast and previous figure. [`consensus.Client.Earnings`](../internal/consensus/earnings.go#L18) gives the results due over five weekdays, followed companies first, with what analysts expect each to earn a share. |
| 2c | [`marketMoves`](../internal/app/movers.go#L132), in the background | Tops up the market's history ([`topUpMarket`](../internal/app/marketdata.go#L89)) and picks the last session's 5 biggest moves across the market against each share's usual ([`market.Moves`](../internal/market/screen.go#L416)), among companies worth US$2bn or more, followed or not. They are shown in a line under the overview. |
| 9 | [`collectLevels`](../internal/app/filings.go#L88) → [`FRED.Fetch`](../internal/prices/fred.go#L147); [`trendsFor`](../internal/app/movers.go#L173) | Bond yields, the Fed's rate, the S&P 500, the VIX and inflation. Also each mover's averages and range. |
| 10 | [`report.Generator.Generate`](../internal/report/generate.go#L73) | [`buildPrompt`](../internal/report/prompt.go#L119) builds the prompt, with the calendar as a "Coming up" block ([`report.renderCalendar`](../internal/report/calendar.go#L15)). **Opus** writes the brief through the relay (stage `brief`). It writes in plain English, with an emoji on each sub-heading and the key figure in each bullet in bold, and ends the overview with "What to watch". [`parseResponse`](../internal/report/parse.go#L22) splits the reply into the overview and the sections. If there are no articles, a "no news" message goes out instead and the run stops. |
| 11 | [`discover.Finder.Find`](../internal/discover/discover.go#L64) → [`FIGI.Verify`](../internal/discover/verify.go#L60) → [`Store.Note`](../internal/discover/store.go#L54) → [`priceCandidates`](../internal/app/prices.go#L277) | **Opus** names the companies in the news that no sector follows. Each ticker is checked against OpenFIGI, counted in `candidates.json` and priced. |
| 12 | [`telegram.RenderWith`](../internal/telegram/render.go#L95) | The report becomes Telegram HTML messages, each under 4,096 characters. Each sector's heading gets its emoji ([sectors.yaml](../config/sectors.yaml)), the writer's `**marks**` become bold ([`highlight`](../internal/telegram/render.go#L419)), the first mention of each glossary term in a section links to its explanation ([`linkTerms`](../internal/telegram/terms.go#L19)), and the "📅 Coming up" block goes after the overview ([`telegram.renderCalendar`](../internal/telegram/calendar.go#L25)). |
| 13 | [`Bot.DeleteMessages`](../internal/telegram/client.go#L237) | With `REPLACE_PREVIOUS` on, deletes the last brief. This happens after the new one is written, so a failed run loses nothing. |
| 14 | [`send`](../internal/app/pages.go#L39) | Sends to the owner, and records the message ids in `prefs.yaml`. With pages on, that is one message, [`BriefSummary`](../internal/telegram/summary.go#L56), with a button to the whole brief as a page (see 8b). The summary holds the overview's opening line, the writer's `IN SHORT` bullets, and the next 24 hours' high-impact releases and results. Without pages, or if the page fails, it sends the messages in full. |
| 15 | [`rememberSent`](../internal/app/channel.go#L69) | Keeps this delivery in memory for `/share`. |
| 16 | [`Covered.Record`](../internal/history/history.go#L89), [`Runs.Add`](../internal/history/runs.go#L121) (+ [`recordSearch`](../internal/app/search.go#L80)) | Writes down what the brief covered, only after delivery, and the run's numbers for `/stats`. |
| 17 | [`lookFrom`](../internal/app/ideas.go#L80) → [`sendIdeas`](../internal/app/ideas.go#L88) | Runs the closer look straight away, from the brief's articles (see 3). It returns the channel's copy. |
| 18 | [`shareBrief`](../internal/app/channel.go#L127) | Posts the brief and the closer look to the channel as one post (see 8). |
| 19 | [`RunScheduler`](../internal/app/app.go#L820) | On any error, [`reportFailure`](../internal/app/app.go#L1079) tells the owner (see 11). Nothing is retried. |

**Links:**
- Feeds **3** (closer look), with its articles.
- Feeds **3a** through `covered.json` headlines.
- Feeds **8** (`/share`) and **10** (`/stats`).
- Uses **12** for each model call and **13** for the run cache.
- The next brief reads `covered.json` and `candidates.json` from this one.

---

## 3. Worth a closer look

**Starts from:**
- The daily run, `/now` and `--once`. [`brief`](../internal/app/app.go#L425) calls [`sendIdeas`](../internal/app/ideas.go#L88) as soon as the owner has the brief, still holding the brief's run lock and relay run. Until 2026-10-01 the daily run waited 20 minutes first.

**Entry point:** [`sendIdeas`](../internal/app/ideas.go#L88)

**What it does:**
- **Once a week** (only on the scheduled run's first look of the week), up to 10 companies from the weekly themes (3a).
- **Every day**, up to 3 shares that moved far beyond their usual on the news (3b).
- Each company gets a BUY, HOLD or SELL verdict (3c). Only BUYs and SELLs are shown, and only for companies the watchlists don't follow. A day with none sends nothing.

**Files:** [ideas.go](../internal/app/ideas.go), [themes.go](../internal/app/themes.go), [reactions.go](../internal/app/reactions.go), [marketdata.go](../internal/app/marketdata.go), [research.go](../internal/app/research.go), [channel.go](../internal/app/channel.go), [internal/ideas](../internal/ideas/), [internal/market](../internal/market/), [internal/telegram/ideas.go](../internal/telegram/ideas.go)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`sendIdeas`](../internal/app/ideas.go#L88) | Runs the weekly part if `lk.Scheduled` is true and [`ThemeLog.DoneThisWeek`](../internal/ideas/themelog.go#L90) is false (ISO week, New York time). A weekly run gets 75 minutes, any other run 25. Opens the run cache (`recommendations`). |
| 2 | [`topUpMarket`](../internal/app/marketdata.go#L89) | Fetches the last 3 sessions into the store, waiting up to 4 minutes (see 4). |
| 3 | [`listings`](../internal/app/marketdata.go#L109) → [`consensus.Client.Listings`](../internal/consensus/listings.go#L23) | Nasdaq's list of every US listing, with its market value, sector and industry. It is kept in `data/market/listings.json` for 20 hours. With no list at all, the closer look stops here. |
| 4 | [`backdrop`](../internal/app/research.go#L193) | FRED's oil, gas, copper, dollar, 10-year yield, credit spread and breakeven inflation, shown above every verdict. |
| 5 | [`newFollowing`](../internal/app/ideas.go#L207) | What the watchlists follow. The closer look leaves those companies to the brief. |
| 6 | [`runThemes`](../internal/app/themes.go#L72), weekly only | See **3a**. |
| 7 | [`runReactions`](../internal/app/reactions.go#L68) | See **3b**. |
| 8 | [`ThemeLog.Add`](../internal/ideas/themelog.go#L61) | Writes the week to `themes.json` however it went, so it runs once a week. |
| 9 | [`telegram.RenderPicks`](../internal/telegram/ideas.go#L94) → [`send`](../internal/app/pages.go#L39) | Lays out and sends to the owner the themes with their picks, then the reactions, then the earlier picks. With pages on, it sends one line for each pick ([`PicksSummary`](../internal/telegram/summary.go#L98)) and a button to the cases (see 8b). The message ids are added to `LastBrief`, so they are cleared along with the brief. |
| 10 | [`sendIdeas`](../internal/app/ideas.go#L88) | Returns a copy laid out with `ForChannel`, headed by [`channelNote`](../internal/telegram/ideas.go#L35), the warning that must stay. The daily run posts it with the brief ([`shareBrief`](../internal/app/channel.go#L127), see 8). |
| 11 | [`recordVerdicts`](../internal/app/ideas.go#L419) | Writes every BUY and SELL shown to the scorecard (see 5). |

**Links:**
- Reads **2** (the brief's articles) and **4** (the bars).
- Writes **5** (the scorecard), which **3a** reads back to avoid repeats.
- Uses **12** for each model call and **13** for the run cache.

### 3a. Weekly themes

**Entry point:** [`runThemes`](../internal/app/themes.go#L72)

**What it does:** measures every US company over two years, and picks out the leaders and the popular and early industries. Opus sorts the leaders into up to 3 themes. Opus with the web scouts up to 2 early ones. Opus with the web then researches each theme for the part the market hasn't paid for, and up to 4 companies in it. Up to 16 companies are judged against their theme's valuation, and up to 10 BUYs or SELLs are shown.

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`loadPanel`](../internal/app/themes.go#L222) → [`market.Store.Load`](../internal/market/store.go#L210) | Two years of daily prices for companies worth at least US$1bn, plus SPY. With less than 13 months of sessions it stops and says "the market's history is still filling". |
| 2 | [`market.Measure`](../internal/market/screen.go#L89) | Each company's returns over about 2 years (`TwoYears - Month` sessions), over 12 months without the last month, and over 6 and 3 months. Also its swing, averages, trading and biggest one-day rise. |
| 3 | [`singaporeStocks`](../internal/app/marketdata.go#L146) | The Straits Times Index's 30 companies ([config/singapore.yaml](../config/singapore.yaml)), priced from the charts in US dollars. |
| 4 | [`market.Leaders`](../internal/market/screen.go#L167) | The best 150. Only eligible companies count: worth US$2bn or more, priced US$5 or more, trading US$20m a day, with 13 months of history. They must be above their 200-day average, must not have made most of their rise in one day, and must not be pinned to a takeover offer. Each is ranked on four measures against SPY. |
| 5 | [`recentHeadlines`](../internal/app/themes.go#L234) → [`market.Mentions`](../internal/market/names.go#L77) | Counts the headlines that name each company, from `covered.json`. A company is matched by its full name, or by a first word that no other company's name has. |
| 6 | [`market.Industries`](../internal/market/screen.go#L267) → [`Popular`](../internal/market/screen.go#L377) / [`Early`](../internal/market/screen.go#L382) | Scores every industry with at least 4 eligible members.<br>• **Popular:** its typical member's 6- and 12-month return, its breadth, the money flowing in against a year ago, and the headlines.<br>• **Early:** only for industries whose year lags the typical industry's, whose 3 months beat the typical industry's, and whose share of members above their 50-day average rose more than the typical industry's did. |
| 7 | [`ideas.Sorter.Sort`](../internal/ideas/themes.go#L59) | **Opus** (stage `themes`) sorts the leaders into up to 3 popular themes. It is shown the popular industries, the headlines and last week's names. |
| 8 | [`ideas.Scout.Find`](../internal/ideas/themes.go#L176) | **Opus with the web** (stage `scout`) finds up to 2 early themes. It starts from the early industries. If there are none, it is told so and searches the web. |
| 9 | [`themeFigures`](../internal/app/themes.go#L292) | Each theme's figures, for the research and for display. |
| 10 | [`recentPicks`](../internal/app/themes.go#L242) → [`Scorecard.Since`](../internal/ideas/scorecard.go#L454) | The theme picks of the last 8 weeks (`RepeatWindow`). |
| 11 | [`researchThemes`](../internal/app/themes.go#L251) → [`ideas.Researcher.Research`](../internal/ideas/themes.go#L330) | **Opus with the web** (stage `research`), 2 themes at a time. It is given last week's research on a theme of the same name ([`ThemeLog.Previous`](../internal/ideas/themelog.go#L117)) and the recent picks. It returns the parts the market has and hasn't paid for, and up to 4 companies, each checked against OpenFIGI ([`ideas.verify`](../internal/ideas/ideas.go#L85)). |
| 12 | [`runThemes`](../internal/app/themes.go#L72), the round-robin loop | Up to 16 candidates, taken from each theme in turn, skipping followed companies. A company picked before carries its earlier verdict (`idea.Before`). |
| 13 | [`factsFor`](../internal/app/ideas.go#L281) | See **3c**. |
| 14 | [`value`](../internal/app/themes.go#L371) | Reads the accounts of up to 8 top members per theme ([`readAccounts`](../internal/app/themes.go#L466)) to get the theme's medians. Compares each candidate's [`Multiples`](../internal/fundamentals/multiples.go#L68) with the theme and with its own five year ends ([`pastMultiples`](../internal/app/themes.go#L504)), adds [`WarningSigns`](../internal/ideas/valuation.go#L188), and sets [`BuyClosed`](../internal/ideas/valuation.go#L109). |
| 15 | [`judge`](../internal/app/ideas.go#L231) | See **3c**. |
| 16 | [`best`](../internal/app/ideas.go#L550) | Keeps only BUYs and SELLs. Drops any with the same verdict as in the last 8 weeks. Keeps at most 10 (`PicksShown`), the most confident first. |
| 17 | [`earlierPicks`](../internal/app/themes.go#L528) → [`panelPath`](../internal/app/marketdata.go#L183), [`ideas.Called`](../internal/ideas/scorecard.go#L438) | The last 8 weeks' picks, with how each has done against SPY. |

### 3b. Daily reactions

**Entry point:** [`runReactions`](../internal/app/reactions.go#L68)

**What it does:** finds shares that moved at least 3 times their usual daily move, on at least twice their usual trading, where the brief's articles explain the move. It judges up to 6, putting results stories first, and shows up to 3 BUYs or SELLs.

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`recentMoves`](../internal/app/reactions.go#L52) → [`market.Store.Load`](../internal/market/store.go#L210) | The last 120 days. It needs at least 62 sessions. |
| 2 | [`market.Moves`](../internal/market/screen.go#L416) | The latest session's outsized moves, among eligible companies. |
| 3 | [`fundamentals.Relevant`](../internal/fundamentals/news.go#L92) | For the 40 largest moves, finds the brief's articles (`lk.Cited`) that name the company. A move that no article explains is skipped. |
| 4 | `resultsWords` sort | Results or outlook stories first, then the largest move against its usual. Takes the top 6 (`ReactionsJudged`). |
| 5 | [`factsFor`](../internal/app/ideas.go#L281) + [`WarningSigns`](../internal/ideas/valuation.go#L188) | See **3c**. There is no theme to compare with here, so only the warning signs are checked. |
| 6 | [`judge`](../internal/app/ideas.go#L231) | See **3c**. The verdict says what changed, how the share moved, and whether the move was bigger or smaller than the news justified. |
| 7 | [`best`](../internal/app/ideas.go#L550) | BUYs and SELLs only, up to 3 (`ReactionsShown`). |

### 3c. Facts and verdicts

**Entry point:** [`factsFor`](../internal/app/ideas.go#L281) (4 companies at a time) → [`ideaFacts`](../internal/app/ideas.go#L320); then [`judge`](../internal/app/ideas.go#L231) → [`ideas.Judge.Judge`](../internal/ideas/judge.go#L54)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`ideaFacts`](../internal/app/ideas.go#L320) → [`marketFor`](../internal/app/prices.go#L170) | Price history and last price from the charts ([`ChartSymbol`](../internal/prices/symbols.go#L38)). A US listing uses Finnhub's live price when it can ([`quoteFor`](../internal/app/commands.go#L855)). |
| 2 | [`fundamentals.Client.Fetch`](../internal/fundamentals/metrics.go#L253) | For a US SEC filer, 5 years of accounts, plus what `/analyse` reads:<br>• [`AddBusiness`](../internal/fundamentals/business.go#L39);<br>• [`addIdeaNews`](../internal/app/ideas.go#L378): the last fortnight's Finnhub news and one Tavily search;<br>• [`addExpectations`](../internal/app/research.go#L115) → [`consensus.Client.Fetch`](../internal/consensus/consensus.go#L132);<br>• [`addRelease`](../internal/app/research.go#L143) → [`sec.EarningsRelease`](../internal/sec/release.go#L40);<br>• [`addPeers`](../internal/app/research.go#L64) → [`Client.Peers`](../internal/fundamentals/peers.go#L177).<br>The fact sheet is [`Snapshot.Table`](../internal/fundamentals/table.go#L46) + [`SensitivityFacts`](../internal/fundamentals/sensitivity.go#L140). |
| 3 | [`ideaFacts`](../internal/app/ideas.go#L320), fallback | Without accounts (a company that doesn't file with the SEC, or a Singapore one), the sheet is [`TradingFacts`](../internal/fundamentals/market.go#L25), "no accounts were read", and the same news ([`addIdeaNews`](../internal/app/ideas.go#L378), [`NewsFacts`](../internal/fundamentals/market.go#L141)). |
| 4 | [`ideas.Judge.Judge`](../internal/ideas/judge.go#L54) | **Opus with the web** (stage `verdicts`), 3 companies a call, 2 calls at a time. It must confirm the claim its case rests on in two independent sources, and searches the web where the facts don't settle it. For each company it gives the verdict, confidence, case, numbers, catalyst, sensitivity, the sources it checked, and the risk. |
| 5 | [`hold`](../internal/ideas/judge.go#L139) | The code's rules, which the model can't bend:<br>• a BUY that `BuyClosed` rules out becomes a HOLD, with the reason given;<br>• no accounts means low confidence at most, which covers every Singapore pick;<br>• a case whose CHECKED line says "one source only" gets low confidence at most ([`oneSource`](../internal/ideas/judge.go#L152)). |

---

## 4. Market history

**Starts from:** [`RunMarket`](../internal/app/marketdata.go#L54), one of the three loops. [`topUpMarket`](../internal/app/marketdata.go#L89) also runs before each closer look.

**What it does:** keeps two years of the whole US market's daily prices in `data/market/`, one gzipped file for each session (about 40MB in all). They come from Massive's free plan at 5 requests a minute, so the first fill takes about 2 hours.

**Files:** [internal/app/marketdata.go](../internal/app/marketdata.go), [internal/market/sync.go](../internal/market/sync.go), [internal/market/store.go](../internal/market/store.go), [internal/prices/massive.go](../internal/prices/massive.go), [internal/consensus/listings.go](../internal/consensus/listings.go)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`RunMarket`](../internal/app/marketdata.go#L54) | Does nothing without `MASSIVE_API_KEY`. |
| 2 | [`Store.Sync`](../internal/market/sync.go#L42) | Asks for up to 10 missing days (`marketChunk`), newest first, back as far as `Reach` (2 years less 5 days, about 496 sessions). Skips weekends, days it already has ([`Known`](../internal/market/store.go#L66)) and today before 18:00 New York. |
| 3 | [`session`](../internal/market/sync.go#L95) → [`Massive.Session`](../internal/prices/massive.go#L73) | One day's prices for every listing. It tries 3 times, unless the key is refused or the context is cancelled. |
| 4 | [`Store.Save`](../internal/market/store.go#L76) | Keeps common shares that traded at a price of at least $1 and a value of at least $1m that day. An old day with no trading is marked `.closed` (a holiday). |
| 5 | [`Massive.Splits`](../internal/prices/massive.go#L143) | Reads the splits since the last sync into `splits.json`. [`Load`](../internal/market/store.go#L210) applies them. |
| 6 | [`RunMarket`](../internal/app/marketdata.go#L54) | After a full chunk it goes again at once. After an error it waits 5 minutes, or 24 hours if the key was refused. When it is up to date, it waits 3 hours. |

**Links:** read by **3a**, **3b** and `--check` (whose `history_missing_days` comes from [`Missing`](../internal/market/sync.go#L146)).

---

## 5. Scorecard

**What it does:**
- Records every BUY and SELL shown, and every `/analyse` verdict.
- Grades each against the S&P 500 (SPY) in US dollars once it is a week old. Both the share and SPY are measured from the first open after the verdict.
- A BUY is right if it beats SPY by at least 5 points a year, scaled to the time passed (`Clearly`). A SELL is right if it trails SPY by as much.
- It is stored in `data/scorecard.json`, and **held in memory** while the service runs.

**Files:** [internal/ideas/scorecard.go](../internal/ideas/scorecard.go), [internal/app/ideas.go](../internal/app/ideas.go), [internal/app/commands.go](../internal/app/commands.go)

**Recording:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`recordVerdicts`](../internal/app/ideas.go#L419) | For the closer look, [`NewRecord`](../internal/ideas/scorecard.go#L208) makes a record for each idea shown, with its chart symbol, source (`theme` or `reaction`), theme and today's dollar exchange rate ([`dollarRates`](../internal/app/ideas.go#L510)). |
| 2 | [`recordAnalysis`](../internal/app/commands.go#L810) | For `/analyse`, with source `analysis`. The same verdict on the same share within 24 hours counts once ([`AddOnce`](../internal/ideas/scorecard.go#L274)). It checks and adds in one step, so two analyses of one share finishing together count once. |
| 3 | [`Scorecard.Add`](../internal/ideas/scorecard.go#L260) | Adds the record in memory and rewrites the whole file. |

**`/scorecard`**, via [`handleScorecard`](../internal/app/ideas.go#L469):

| Step | Code | What it does |
|---|---|---|
| 1 | [`Scorecard.Due`](../internal/ideas/scorecard.go#L308) | The charts of verdicts at least 7 days old (`MinAge`), up to 60. |
| 2 | [`pathFor`](../internal/app/ideas.go#L446) → [`History.Fetch`](../internal/prices/history.go#L88) | Each one's sessions from the chart source, and SPY's. |
| 3 | [`dollarRates`](../internal/app/ideas.go#L510) | Exchange-rate history for shares priced abroad. |
| 4 | [`Scorecard.Settle`](../internal/ideas/scorecard.go#L349) | Writes down each verdict's entry price, the next session's open, once that session has happened. |
| 5 | [`Scorecard.Summary`](../internal/ideas/scorecard.go#L469) | Right or wrong for each verdict, split by confidence and by source (theme, reaction, analysis, news). |

**Links:** written by **3** and **6**, and read by **3a** ([`recentPicks`](../internal/app/themes.go#L242) and [`earlierPicks`](../internal/app/themes.go#L528)). It is **not** given to the models.

---

## 6. `/analyse`

**Starts from:** the owner sending `/analyse`. The bot asks which company ([`ask`](../internal/app/ask.go#L31)), opening a reply box with "Ticker, e.g. NVDA" in it. [`HandleMessage`](../internal/app/commands.go#L70) takes the chat's next message as the ticker ([`answering`](../internal/app/ask.go#L54)), if it comes within 10 minutes and no command comes first. If the question is dropped either way, it is deleted ([`withdraw`](../internal/app/ask.go#L102)). Otherwise Telegram would reopen the reply box every time the chat is opened. An answer that doesn't look like a ticker ([`asTicker`](../internal/app/ask.go#L116)) gets the question again. `/analyse NVDA` in one line still works, and so do the spellings `/analyze` and `/accounts`. A ticker the SEC has no filer for is answered at once ([`analyseWhich`](../internal/app/commands.go#L551)), without waiting its turn behind the analyses running. Once it has a ticker, the analysis runs in the background ([`background`](../internal/app/jobs.go)), so several can run at once (see 10).

**Entry point:** [`handleAnalyse`](../internal/app/commands.go#L589)

**What it does:** reads a company's SEC filings: five years, the year so far, and the latest five quarters each on its own. It also reads its trading, its news, what analysts expect and its latest results release. Opus, searching the web for the last fortnight's news and the company's plans, writes an analysis with the house method. It centres on the case for and against the company, where the company is heading, and whether its figures hold up. It ends with a short BUY, HOLD or SELL verdict against the S&P 500 over 12 months.

**Files:**
- [internal/app/commands.go](../internal/app/commands.go), [research.go](../internal/app/research.go), [prices.go](../internal/app/prices.go)
- [internal/fundamentals](../internal/fundamentals/), [internal/sec](../internal/sec/), [internal/consensus](../internal/consensus/)
- [internal/telegram/render.go](../internal/telegram/render.go), [config/method.md](../config/method.md)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`handleAnalyse`](../internal/app/commands.go#L589) | Opens the run cache (`analysis`), replies "reading…", and gives itself 5 minutes plus one model call's time. |
| 2 | [`fundamentals.Client.Fetch`](../internal/fundamentals/metrics.go#L253) | 5 years of XBRL accounts from EDGAR (US GAAP or IFRS), with the year so far. [`buildQuarters`](../internal/fundamentals/quarters.go#L44) adds the latest five quarters each on its own, and the twelve months the last four make. A quarter's income is taken as filed. Its cash flow is the difference between two running totals. A fourth quarter is the year minus nine months. Its earnings a share are its profit over its own diluted shares, worked out the same way. |
| 3 | [`quoteFor`](../internal/app/commands.go#L855) | The share price, which turns the filed figures into multiples. |
| 4 | [`AddBusiness`](../internal/fundamentals/business.go#L39) | The business description from the annual report (10-K or 20-F). |
| 5 | [`tradingFor`](../internal/app/prices.go#L146) → [`Summarise`](../internal/prices/history.go#L281) | Returns, averages, range, VWAP and volatility, and the S&P 500's returns over the same stretches. |
| 6 | [`addNews`](../internal/app/prices.go#L232) | Finnhub company news plus 2 Tavily searches ([`searchCompany`](../internal/app/research.go#L214)), which cost 2 credits. [`Relevant`](../internal/fundamentals/news.go#L92) keeps only the results that name the company. |
| 7 | [`addExpectations`](../internal/app/research.go#L115), [`addRelease`](../internal/app/research.go#L143), [`addReleaseFigures`](../internal/app/research.go#L162), [`addPeers`](../internal/app/research.go#L64), [`backdrop`](../internal/app/research.go#L193) | Nasdaq's forecasts, price targets, insiders, short interest and funds. The latest results release. Where the filings don't reach the release's quarter yet, its figures, copied out by a quick model call (stage `release`) and checked against the filings ([`Snapshot.AddRelease`](../internal/fundamentals/release.go#L112)), as columns marked *. Where the company stands in its Nasdaq industry group on last calendar year's SEC figures ([`Client.Peers`](../internal/fundamentals/peers.go#L177)). The FRED backdrop. |
| 8 | [`Relay.Begin`](../internal/relay/relay.go#L98) → [`Analyzer.Analyze`](../internal/fundamentals/analyze.go#L65) | **Opus with the web** (stage `analysis`) reads [`Snapshot.Table`](../internal/fundamentals/table.go#L46), with the quarters in their own block ([`quarterTable`](../internal/fundamentals/quarters.go#L196)). [method.md](../config/method.md) is added to its system prompt. It searches for the last fortnight's news, the company's plans, and what is due in the next ninety days. For a foreign filer whose interim figures aren't in XBRL, it also searches for its latest results announcement. With `ANALYSIS_TOOLS` on, it also has three tools of the service's own ([`analysisTools`](../internal/app/tools.go#L20) → [`relay.WithTools`](../internal/relay/tools.go#L37)): `find_concepts`, `read_concept` and `compute`, served by this program as an MCP server ([`AccountTools`](../internal/fundamentals/tools.go#L46)). It may make 16 lookups. What it asked is kept in `-tools.txt` beside the reply. |
| 9 | [`SplitRelated`](../internal/fundamentals/related.go#L54) → [`VerifyRelated`](../internal/fundamentals/related.go#L90) | Cuts out the "companies to read next to it" table and checks those tickers against OpenFIGI. |
| 10 | [`SplitVerdict`](../internal/fundamentals/verdict.go#L29), [`SplitShort`](../internal/fundamentals/verdict.go#L67) | Takes out THE VERDICT section, then IN SHORT, the analysis's own summary in plain words. [`SplitSources`](../internal/fundamentals/sources.go#L51) then takes out the links, which become numbered footnotes at the end, and renumbers the [3] citations in the text to match. |
| 11 | [`analysisMessages`](../internal/app/commands.go#L789) → [`RenderAnalysis`](../internal/telegram/render.go#L672) | The analysis, then the verdict, then the related list. |
| 12 | [`send`](../internal/app/pages.go#L39) | Sends to the owner. With pages on, it sends the IN SHORT summary (what the company does, who buys it, what is coming, the figures that matter most, one point for and one against, in the analysis's order), then the verdict in one line ([`AnalysisSummary`](../internal/telegram/summary.go#L184)) and a button to the whole analysis (see 8b). |
| 13 | [`rememberFor`](../internal/app/channel.go#L59) | Keeps the owner's copy and the channel's copy, which carries the warning, for `/share`. |
| 14 | [`recordAnalysis`](../internal/app/commands.go#L810) | Writes the verdict to the scorecard (see 5). |

**Links:** feeds **5** and **8**. The closer look's verdicts (**3c**) read the same sources, except that they make 1 Tavily search instead of 2.

---

## 6b. `/industry`

**Starts from:** the owner sending `/industry`. The bot asks which industry ([`ask`](../internal/app/ask.go#L31)). The next message is the topic, however many words it has (see 6). `/industry robotics` in one line still works.

**Entry point:** [`handleIndustry`](../internal/app/industry.go#L21)

**What it does:** explains how an industry fits together, for someone who doesn't work in it. It is a map to read single companies against. It covers the big picture and the chain from inputs to the customer. For each building block it says what it does, how it makes money, who leads it, what is changing, and what that leads to. Then it says where the profits pool. Then it looks ahead: what people are saying about the industry, the research and developments due in the next one to three years, the knock-on effects of those changes, what to watch, and the risks. It ends with three to six listed companies to look into for each part. Where a part has companies in at least three countries, they come from at least three. Each is shown with its flag and country, and every ticker is checked against its exchange.

**Files:** [internal/app/industry.go](../internal/app/industry.go), [internal/industry/industry.go](../internal/industry/industry.go), [internal/telegram/industry.go](../internal/telegram/industry.go), `industry.system` in [config/prompts.md](../config/prompts.md)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`handleIndustry`](../internal/app/industry.go#L21) | With no topic, says how to ask. Otherwise opens the run cache (`industry`) and a relay run, replies "Mapping…", and gives itself one model call's time plus two minutes. |
| 2 | [`industry.Explainer.Explain`](../internal/industry/industry.go#L61) | **Opus with the web** (stage `industry`) writes the explanation in the brief's style, with `### ` sub-headings that each have an emoji, over plain-English bullets. It ends with a `COMPANIES BY PART` table of `part\|name\|ticker\|exchange\|why`. |
| 3 | [`industry.Split`](../internal/industry/industry.go#L86) → [`industry.Verify`](../internal/industry/industry.go#L113) → [`VerifyRelated`](../internal/fundamentals/related.go#L90) | Cuts the table out of the text and checks each ticker against OpenFIGI. A ticker that fails, or a "?", is dropped. |
| 4 | [`RenderIndustry`](../internal/telegram/industry.go#L31) | The heading and its note, the text with its finance words linked, then "🏢 Companies to look into", grouped by part. |
| 5 | [`send`](../internal/app/pages.go#L39), [`rememberSent`](../internal/app/channel.go#L69) | Sends to the owner. With pages on, it sends the big picture, the parts and a button (see 8b). It keeps the channel's copy for `/share`. That copy adds "AI-written, unchecked, not advice." |

**Links:** works with **6**. Send `/analyse` with any of the tickers to read that company's accounts. Feeds **8**.

---

## 7. `/now`

**Entry point:** [`handleNow`](../internal/app/commands.go#L247) → [`SendReport`](../internal/app/app.go#L393) → [`brief`](../internal/app/app.go#L425)`(share=false, later=false)`

**What it does:** runs the whole of **2**, for the owner only. It is followed at once by **3** with the **reactions only**. `lk.Scheduled` is false, so the weekly themes never run from `/now`. Nothing goes to the channel until `/share`. If no chat is registered yet, `/now` registers the sender's chat first.

**Links:** **2**, **3b**, **8**.

---

## 8. The channel and `/share`

**What it does:** the channel (`TELEGRAM_CHANNEL_ID`) is read-only for other readers.
- The **daily brief and its closer look** are posted there on their own, as one post, once the closer look is done ([`shareBrief`](../internal/app/channel.go#L127) → [`share`](../internal/app/channel.go#L91) → [`sendTogether`](../internal/app/pages.go#L61)). The post holds both summaries, then a button for each page, with the closer look under [`channelNote`](../internal/telegram/ideas.go#L35). The owner asked for this on 2026-10-01. A day with no closer look posts the brief alone. With pages off, or when the two summaries are too long for one message, each goes on its own. If a `/share` posted the brief while the closer look was running, the closer look goes alone ([`shareIdeas`](../internal/app/channel.go#L170)).
- **`/share`** ([`handleShare`](../internal/app/channel.go#L196)) posts whichever of `/now`'s brief, `/analyse`'s analysis or `/industry`'s explanation arrived last. It never posts the closer look.

**Files:** [internal/app/channel.go](../internal/app/channel.go), [internal/telegram/client.go](../internal/telegram/client.go), [internal/telegram/ideas.go](../internal/telegram/ideas.go)

**Sequence for `/share`:** [`latest`](../internal/app/channel.go#L81) gets the last delivery. [`share`](../internal/app/channel.go#L91) guards against posting it twice. It then calls [`send`](../internal/app/pages.go#L39) as a broadcast, using the channel's copy where there is one. With pages on, that is the channel's own summary and page, under the channel's note. If the channel refuses, the owner is told.

**Links:** fed by **2**, **3**, **6** and **7**.

---

## 8b. Pages

**Starts from:** every long delivery, when `PAGES_URL` is set: the brief, the closer look, `/analyse` and `/industry`, to the owner and to the channel.

**Entry point:** [`send`](../internal/app/pages.go#L39)

**What it does:** sends a short summary with a "📖 Read…" button, and keeps the whole thing as a web page that the service serves itself. Since 2026-10-01 the page has been laid out from the same data as the messages (the report, the verdicts, the accounts, the explanation). It has tables, charts and a card for each company. Its wording is the same as the messages'.

**Files:** [internal/app/pages.go](../internal/app/pages.go), [internal/pages/pages.go](../internal/pages/pages.go), [internal/pages/doc.go](../internal/pages/doc.go), [internal/pages/render.go](../internal/pages/render.go), [internal/telegram/summary.go](../internal/telegram/summary.go), [internal/telegram/pagedocs.go](../internal/telegram/pagedocs.go), [internal/telegram/client.go](../internal/telegram/client.go), [fly.toml](../fly.toml)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`BriefSummary`](../internal/telegram/summary.go#L56), [`PicksSummary`](../internal/telegram/summary.go#L98), [`AnalysisSummary`](../internal/telegram/summary.go#L184), [`IndustrySummary`](../internal/telegram/summary.go#L305) | The summary for the reader. For the brief: its opening line, its bullets and the next 24 hours ("… @ time"). For the closer look: one line for each pick. For an analysis: its IN SHORT summary, then its verdict. For an industry: its big picture and its parts. There is one emoji, on the title. |
| 2 | [`BriefDoc`](../internal/telegram/pagedocs.go#L43), [`PicksDoc`](../internal/telegram/pagedocs.go#L269), [`AnalysisDoc`](../internal/telegram/pagedocs.go#L651), [`IndustryDoc`](../internal/telegram/pagedocs.go#L1101) | The page. The brief's page has the markets table and chart, the overview, the coming-up tables, and the sectors with their moves and sources. The closer look's has a card for each pick, with its price chart and case. An analysis's has the business first, as the owner asked on 2026-10-04 ([`businessFirst`](../internal/telegram/pagedocs.go#L837)): the reading up to WHERE IT IS HEADING, then tiles of the figures a reader looks for first ([`glanceTiles`](../internal/telegram/pagedocs.go#L724), from [`Snapshot.Glance`](../internal/fundamentals/glance.go#L32): market value, P/E, forward P/E, price to sales, cash and debt, each with a short note of what it is worked out on), the share price as one chart ([`priceParts`](../internal/telegram/pagedocs.go#L427): the price and the day's move, the moves beside the S&P 500's, and the year's line with its averages, high and low), the accounts as tables and charts (each period named by its months, as [`MonthSpan`](../internal/fundamentals/period.go#L16) names it; revenue growth with the figure and months it grew from; gross profit beside its margin; cash from operations against capital spending; the release's columns marked *), the latest balance sheet's cash and debt, the company beside its industry, the rest of the reading from KEY NUMBERS, then the verdict. An industry's has the chain of parts, then its companies by part. |
| 3 | [`pages.Store.Publish`](../internal/pages/pages.go#L52) | Writes the page to `/data/pages/<id>.html` under a random 128-bit id. Deletes pages older than 30 days. |
| 4 | [`pages.Render`](../internal/pages/render.go#L35) | Lays the page out as one document, escaping everything except Telegram's tags. It has no scripts, and fetches nothing from elsewhere. |
| 5 | [`Bot.SendLinked`](../internal/telegram/client.go#L137) | Sends the summary with a button to `PAGES_URL/r/<id>`. For the channel's daily post ([`sendTogether`](../internal/app/pages.go#L61)), it sends two summaries and a button for each page. |
| 6 | [`send`](../internal/app/pages.go#L39) | If the page or the summary fails, sends the messages in full instead. |
| 7 | [`pages.Store.Handler`](../internal/pages/pages.go#L109) | Answers `GET /r/<id>` with the page, and anything else with "not found". Pages carry a strict content policy, ask search engines not to index them, and send no referrer. Fly serves them over HTTPS on port 8080 (`[http_service]` in [fly.toml](../fly.toml)). `auto_stop_machines = "off"` there makes sure the scheduler never stops. |

**Links:** used by **2**, **3**, **6**, **6b** and **8**.

---

## 9. `/watchlist` and `/sources`

**What it does:** changes what the service follows, without a deploy. The changes are kept in `prefs.yaml` on the data volume, on top of [companies.yaml](../config/companies.yaml) and [sources.yaml](../config/sources.yaml). `scripts/sync-from-fly.sh` writes them into `config/` (see 16).

**Files:** [internal/app/commands.go](../internal/app/commands.go), [config/prefs.go](../config/prefs.go), [config/watchlist.go](../config/watchlist.go), [config/fold.go](../config/fold.go)

| Command | Code | What it does |
|---|---|---|
| `/watchlist` | [`renderWatchlists`](../internal/app/commands.go#L440) | Lists the sectors and companies in force. |
| `/watchlist add <sector> <ticker or name>` | [`companyFrom`](../internal/app/commands.go#L384) → [`Prefs.AddCompany`](../config/prefs.go#L108) → [`Save`](../config/prefs.go#L194) | Follows a company. A bare ticker gets its name from the SEC index. |
| `/watchlist remove <sector> <term>` | [`Prefs.RemoveCompany`](../config/prefs.go#L138) | Stops following one. |
| `/watchlist edits` / `reset` | [`renderEdits`](../internal/app/commands.go#L488) / [`Prefs.ResetEdits`](../config/prefs.go#L166) | Shows or drops the changes made from Telegram. |
| `/sources` / `/sources on\|off <id>` | [`renderSources`](../internal/app/commands.go#L508) / [`Prefs.SwitchFeed`](../config/prefs.go#L174) | Lists the feeds, or switches one. |

**Links:** the next **2** reads the lists in force, for prices, filings, matching, sections and the "already followed" checks.

---

## 10. The other commands

They all arrive through [`Bot.Poll`](../internal/telegram/updates.go#L77) → [`HandleMessage`](../internal/app/commands.go#L70). It ignores commands from any chat the owner hasn't allowed, routes on the command, and sends any error back with secrets scrubbed out.

Most commands are quick and are handled in turn. `/analyse` and `/industry` take minutes. Once they know what to look at, they run as background jobs ([jobs.go](../internal/app/jobs.go)), side by side, and the bot keeps reading messages. `/now` also runs in the background ([`detach`](../internal/app/jobs.go)), and its brief runs alone:

| Step | Code | What it does |
|---|---|---|
| 1 | [`background`](../internal/app/jobs.go) | Counts the job against its chat, puts it at the back of the line, and starts a goroutine for it. |
| 2 | [`admit`](../internal/app/jobs.go) | Reads the plan through [`planNow`](../internal/app/usage.go#L62). Jobs start in the order they were asked for, three at a time, for the server's 1 GB of memory. Once any window is 85% used, it warns the chat once and runs one job at a time. While a brief is being written, no job starts. A job that has to wait says why, and starts when its turn comes. |
| 3 | the handler | Runs with its own relay run and cache folder. Each model call is a new Claude Code process that keeps no session. |
| 4 | [`jobDone`](../internal/app/jobs.go) | When the chat's last job ends, sends how much of each plan window is used. A chat whose jobs never reached a model is not told. |

A brief takes its turn through [`hold`](../internal/app/jobs.go): it waits for the jobs already running to end, and no job starts until it calls `release`. A report that falls back to several messages is sent under a lock ([`send`](../internal/app/pages.go)), so two reports finishing together don't interleave.

| Command | Code | What it does |
|---|---|---|
| `/start` | [`handleStart`](../internal/app/commands.go#L227) | Registers the chat as the owner's, once. |
| `/help` | `helpText` in [commands.go](../internal/app/commands.go) | What the bot can do. |
| `/schedule` | [`handleSchedule`](../internal/app/commands.go#L319) | When the next brief is due, in both time zones. |
| `/stats` | [`handleStats`](../internal/app/commands.go#L845) → [`Runs.Summary`](../internal/history/runs.go#L138) | What recent briefs found, placed, moved and cost (`runs.json`). |
| `/usage` (or `/credits`) | [`handleUsage`](../internal/app/usage.go#L51) → [`planNow`](../internal/app/usage.go#L62), [`Search.Usage`](../internal/search/search.go#L302) | What is left of the Claude plan, and of the month's Tavily credits. For the plan it shows how much of each window is used and when it resets. The reading comes from the last call if that was under five minutes ago. Otherwise it makes a one-word Haiku call through [`CheckLimits`](../internal/relay/answer.go#L304). Any command chat may ask. |
| `/clear` | [`handleClear`](../internal/app/commands.go#L276) → [`ClearChat`](../internal/app/commands.go#L283) → [`SweepMessages`](../internal/telegram/client.go#L218) | Deletes the bot's messages among the last 300 ids. Telegram only allows deleting messages less than 48 hours old. |

---

## 11. Failure alerts

**Entry point:** [`reportFailure`](../internal/app/app.go#L1079), called by [`RunScheduler`](../internal/app/app.go#L820) when a scheduled brief fails.

**What it does:** sends the owner the error, with secrets scrubbed out, and says when the next attempt is. It never retries, because a retry would spend the plan on the same error. The closer look and the channel report their own failures to the owner ([`shareBrief`](../internal/app/channel.go#L127), [`shareIdeas`](../internal/app/channel.go#L170)).

---

## 12. Model calls (the relay)

**What it does:** this is the only way the app reaches a model. Each call writes a request file. A fresh `claude -p` then runs without a screen on the owner's subscription, and its reply is written beside the request. There is no API key and no API client.

**Files:** [internal/relay/relay.go](../internal/relay/relay.go), [internal/relay/answer.go](../internal/relay/answer.go), [config/prompts.md](../config/prompts.md)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`Relay.Begin`](../internal/relay/relay.go#L98) | Opens `data/relay/<time>-<kind>/` (brief, look, analysis-TICKER), and puts it on the context. [`prune`](../internal/relay/relay.go#L132) keeps the last 40. |
| 2 | [`Stage`](../internal/relay/relay.go#L172) / [`Plain`](../internal/relay/relay.go#L180) | Fit the relay to each package's `Completer`, and fix the stage name. |
| 3 | [`Run.Ask`](../internal/relay/relay.go#L229) | Writes `NN-stage-request.txt` and notes it in the ledger ([`Note`](../internal/relay/relay.go#L284)). Then it asks, writes `NN-stage-reply.txt`, and copies both into the run cache. |
| 4 | [`Claude.Answer`](../internal/relay/answer.go#L108) | Runs `claude -p` with the stage's model, in the run's own folder. [`childEnv`](../internal/relay/answer.go#L395) removes `ANTHROPIC_API_KEY`. All tools are off, except web search and web fetch for the six stages marked "yes" below. |

Which model answers each stage (`DefaultModels` in [answer.go](../internal/relay/answer.go)):

| Stage | Model | Web | Used by |
|---|---|---|---|
| `triage` | Opus | no | 2: rating and filing articles |
| `review` | Opus | yes | 2: checking placements |
| `brief` | Opus | no | 2: writing the brief |
| `names` | Opus | no | 2: new names |
| `themes` | Opus | no | 3a: sorting leaders into themes |
| `scout` | Opus | yes | 3a: early themes |
| `research` | Opus | yes | 3a: researching each theme |
| `verdicts` | Opus | yes | 3c: BUY/HOLD/SELL, checked in two sources |
| `analysis` | Opus | yes | 6: `/analyse` |
| `industry` | Opus | yes | 6b: `/industry` |

`RELAY_ANSWER=session` uses [`Session.Answer`](../internal/relay/answer.go#L436) instead. It waits for a person, or a session's subagent, to write the reply file.

---

## 13. Run cache

**Entry point:** [`Cache.Start`](../internal/runcache/runcache.go#L97)`(ctx, kind, subject)`, called by [`brief`](../internal/app/app.go#L425), [`handleAnalyse`](../internal/app/commands.go#L589), [`handleIndustry`](../internal/app/industry.go#L21) and [`sendIdeas`](../internal/app/ideas.go#L88).

**What it does:** keeps the **latest** run of each kind in `data/cache/<kind>/`, where the kind is `brief`, `analysis`, `recommendations` or `industry`. It holds each step's data as JSON ([`Save`](../internal/runcache/runcache.go#L199)), the messages sent, the model calls, and `run.json` ([`Finish`](../internal/runcache/runcache.go#L239)). A run writes under `data/cache/.running/` and moves into its kind's folder when it finishes. If two runs of one kind overlap, the one that finished last is kept. Each compares its end time with the `run.json` of the run already kept. Secrets are scrubbed out. `scripts/sync-cache-from-fly.sh` brings it down from Fly. A change to the brief, analysis or closer look should start from it, rather than from a new run.

---

## 14. Secret scrubbing

**Entry point:** [`logging.New`](../internal/logging/scrub.go#L35) wraps the log handler at start-up. [`logging.Scrub`](../internal/logging/scrub.go#L47) cleans the error text sent to the chat ([`HandleMessage`](../internal/app/commands.go#L70), [`reportFailure`](../internal/app/app.go#L1079), the channel failures).

**What it does:** replaces every credential in the settings with `[redacted]`. This is needed because `net/http` puts request addresses into its error messages, and those addresses contain the bot token and API keys.

---

## 15. Command-line modes

All in [cmd/market-watch/main.go](../cmd/market-watch/main.go).

| Flag | Code | What it does |
|---|---|---|
| (none) | [`run`](../cmd/market-watch/main.go#L73) → [`Serve`](../internal/app/app.go#L851) | The service (1). |
| `--check` | [`runCheck`](../cmd/market-watch/main.go#L229) | Checks each part without spending anything. It covers Telegram, the owner's chat, the channel, the feeds, Tavily (through its free usage call), one Massive session plus `history_missing_days`, Nasdaq's forecasts and listings, the next brief's time, and the Claude Code version and login. It never asks a model. The deploy script runs it on Fly. |
| `--once` | [`SendReport`](../internal/app/app.go#L393) | Sends one brief plus the reactions to the owner, then exits. **Calls models.** |
| `--once --share` | [`Publish`](../internal/app/app.go#L409) | The same, but also to the channel, with the closer look straight after. **Calls models.** |
| `--clear` | [`runClear`](../cmd/market-watch/main.go#L195) → [`ClearChat`](../internal/app/commands.go#L283) | Deletes the bot's earlier messages. |
| `--fold` | [`runFold`](../cmd/market-watch/main.go#L151) → [`config.Fold`](../config/fold.go#L29) | Writes the Telegram changes in `data/prefs.yaml` into `config/companies.yaml` and `config/sources.yaml`, changing only the lines it must. It needs no credentials. |
| `--mcp-accounts <CIK>` | [`runAccountTools`](../cmd/market-watch/main.go#L346) → [`mcp.Serve`](../internal/mcp/mcp.go#L55) | Serves one company's account tools to Claude Code on standard input and output. Claude Code starts it for an analysis call; nobody runs it by hand. `--tools-log` names a file for each call. |

---

## 16. Deploy and sync scripts

| Script | What it does | Links |
|---|---|---|
| [fly-deploy.sh](../scripts/fly-deploy.sh) | Creates the app and volume if they are missing. Copies the secrets from `.env`, printing only their names. Runs `fly deploy`, then runs `--check` on the machine as the app's user. | 15 (`--check`) |
| [sync-from-fly.sh](../scripts/sync-from-fly.sh) | Copies `prefs.yaml`, `covered.json`, `runs.json`, `candidates.json`, `scorecard.json` and `terms.json` from Fly into `./data`. It backs up what they replace to `data/.backup/<time>/`, and leaves alone any file Fly doesn't have. Then it runs `--fold`. | 9, 15 (`--fold`) |
| [sync-cache-from-fly.sh](../scripts/sync-cache-from-fly.sh) | Copies the latest `brief`, `analysis` and/or `recommendations` run cache from Fly into `data/cache/`. | 13 |

---

## 17. Live tests

These tests talk to real services. They are skipped unless their environment switch is set. Those marked **Models** call Claude on the owner's plan, so ask before running them.

| Test | Switch | What it does |
|---|---|---|
| [`TestLiveBrief`](../internal/app/live_test.go#L35) | `LIVE_BRIEF=1` | A real brief to the owner's chat. **Models.** |
| [`TestLiveAnalysis`](../internal/app/live_test.go#L52) | `LIVE_ANALYSIS=<ticker>` | A real `/analyse` to the owner's chat. **Models.** |
| [`TestLiveMarket`](../internal/app/live_test.go#L115) | `LIVE_MARKET=1` | Fills `data/market` and logs the leaders, the popular and early industries and the day's moves. No models. |
| [`TestLiveThemes`](../internal/app/live_test.go#L177) | `LIVE_THEMES=1` | A week's themes plus the reactions, to the owner only. The week isn't saved, so the scheduled run still does it. **Models.** |
| [`TestLiveDefaultSourcesStillParse`](../internal/feed/live_test.go#L21) | `MARKET_WATCH_LIVE=1` | Every feed still parses. |
| [`TestLiveFundamentals`](../internal/fundamentals/live_test.go#L24) | `MARKET_WATCH_LIVE=1` | One company's accounts from EDGAR. |
| [`TestLivePrices`](../internal/prices/live_test.go#L17) | `MARKET_WATCH_LIVE=1` | Finnhub quotes. |
| [`TestLiveSearchBesideTheFeeds`](../internal/search/live_test.go#L27) | `SEARCH_LIVE=1` | Search against the feeds. Spends about 15 Tavily credits. |
| [`TestLiveBotTokenIsValid`](../internal/telegram/live_test.go#L16) | `MARKET_WATCH_LIVE=1` | The bot token. |
| [`TestLiveClaude`](../internal/relay/relay_test.go#L425) | `LIVE_CLAUDE=1` | One real `claude -p` call. **Models.** |

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → **4 Function by function** → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

**Next:** [5 Reviewing the code](REVIEW.md), how to review it, from seeing real output to the rules that must hold.
