**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → **4 Function by function** → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

---

# Market Watch, function by function

Every function the app has. For each one:

- **Starts from:** what triggers it.
- **Entry point:** where its code begins.
- **What it does:** a short summary.
- **Files:** the files involved.
- **Sequence:** the order its code runs in, as which function in which file does what.
- **Links:** which other functions it feeds or reads from.

Line numbers are as of commit 862f491 (27 September 2026). When a flow changes, update its entry here.

**How to use it:** read "At a glance" and "How they connect" first. Then pick
one function and follow its **Sequence** table, clicking each step through to
the code. Words you don't know are in the [Glossary](GLOSSARY.md).

Companion documents:
- [ARCHITECTURE.md](ARCHITECTURE.md) tells the same code as the story of a day, then file by file.
- [RUNBOOK.md](RUNBOOK.md) is how to operate it.
- [TASKS.md](TASKS.md) is what isn't built yet.

---

## At a glance

| # | Function | Starts from | Entry point |
|---|---|---|---|
| 1 | [Start-up and the four loops](#1-start-up-and-the-four-loops) | The process starting (on Fly, the machine booting) | [`main`](../cmd/market-watch/main.go#L27) → [`Serve`](../internal/app/app.go#L728) |
| 2 | [The daily brief](#2-the-daily-brief) | The scheduler: 07:30 New York, weekdays | [`RunScheduler`](../internal/app/app.go#L697) → [`publishScheduled`](../internal/app/app.go#L330) |
| 3 | [Worth a closer look](#3-worth-a-closer-look) | 20 minutes after the daily brief; at once after `/now` or `--once` | [`RunLooks`](../internal/app/look.go#L73) → [`sendIdeas`](../internal/app/ideas.go#L91) |
| 3a | [Weekly themes](#3a-weekly-themes) | The first scheduled closer look of the week | [`runThemes`](../internal/app/themes.go#L72) |
| 3b | [Daily reactions](#3b-daily-reactions) | Every closer look | [`runReactions`](../internal/app/reactions.go#L45) |
| 3c | [Facts and verdicts](#3c-facts-and-verdicts) | Called by 3a and 3b | [`factsFor`](../internal/app/ideas.go#L276) → [`judge`](../internal/app/ideas.go#L226) |
| 4 | [Market history](#4-market-history) | A background loop, and before each closer look | [`RunMarket`](../internal/app/marketdata.go#L54), [`topUpMarket`](../internal/app/marketdata.go#L89) |
| 5 | [Scorecard](#5-scorecard) | Verdicts shown, `/analyse`, `/scorecard` | [`recordVerdicts`](../internal/app/ideas.go#L359), [`recordAnalysis`](../internal/app/commands.go#L587), [`handleScorecard`](../internal/app/ideas.go#L409) |
| 6 | [`/analyse <ticker>`](#6-analyse-ticker) | Owner's command | [`handleAnalyse`](../internal/app/commands.go#L459) |
| 7 | [`/now`](#7-now) | Owner's command | [`handleNow`](../internal/app/commands.go#L157) |
| 8 | [The channel and `/share`](#8-the-channel-and-share) | The daily run; owner's command | [`shareBrief`](../internal/app/channel.go#L110), [`shareIdeas`](../internal/app/channel.go#L140), [`handleShare`](../internal/app/channel.go#L165) |
| 9 | [`/watchlist` and `/sources`](#9-watchlist-and-sources) | Owner's command | [`handleWatchlist`](../internal/app/commands.go#L240), [`handleSources`](../internal/app/commands.go#L323) |
| 10 | [The other commands](#10-the-other-commands) | Owner's command | [`HandleMessage`](../internal/app/commands.go#L61) |
| 11 | [Failure alerts](#11-failure-alerts) | A scheduled brief failing | [`reportFailure`](../internal/app/app.go#L950) |
| 12 | [Model calls (the relay)](#12-model-calls-the-relay) | Every call to a model | [`Relay.Begin`](../internal/relay/relay.go#L95), [`Run.Ask`](../internal/relay/relay.go#L226) |
| 13 | [Run cache](#13-run-cache) | The brief, `/analyse`, the closer look | [`Cache.Start`](../internal/runcache/runcache.go#L79) |
| 14 | [Secret scrubbing](#14-secret-scrubbing) | Every log line and error reply | [`logging.New`](../internal/logging/scrub.go#L35), [`logging.Scrub`](../internal/logging/scrub.go#L47) |
| 15 | [Command-line modes](#15-command-line-modes) | `--check`, `--once`, `--once --share`, `--clear`, `--fold` | [`main`](../cmd/market-watch/main.go#L27) |
| 16 | [Deploy and sync scripts](#16-deploy-and-sync-scripts) | By hand | [scripts/](../scripts/) |
| 17 | [Live tests](#17-live-tests) | By hand, with an environment switch | `*_test.go` |

---

## How they connect

```
 process start
   main ─► run ─► config.Load, LoadPrompts, logging.New, app.New ─► Serve
                                                                      │
   ┌──────────────────────────────┬─────────────────────────┬─────────┴───────────┬──────────────────────────┐
   ▼                              ▼                         ▼                     ▼                          │
 RunScheduler (2)              RunLooks (3)             RunMarket (4)        Bot.Poll ─► HandleMessage     │
   │ 07:30 NY, weekdays          │ every 30 s             │ 10 days at a time     │                          │
   ▼                              │                         ▼                     ├─ /now ─► SendReport ─► brief ─► sendReport ─► sendIdeas (reactions only, owner only)
 publishScheduled                 │                  data/market/*.json.gz        ├─ /analyse ─► handleAnalyse ─► recordAnalysis ──┐
   ▼                              │                  ▲   (2 years of bars)        ├─ /scorecard ─► Due, Settle, Summary ◄──────┤
 brief ─► sendReport              │                  │                            ├─ /share ─► share(latest delivery)          │
   │  (prices, filings, search,   │                  │ read by                    ├─ /watchlist, /sources ─► prefs.yaml        │
   │   feeds, triage, review,     │                  │                            └─ /start /help /schedule /stats /clear      │
   │   Opus brief, new names)     │                  │                                                                         │
   ├─► owner's chat               │                  │                                                                         │
   ├─► shareBrief ─► channel      │                  │                                                                         │
   └─► queueLook ─► pending-look.json ─(due in 20 min)─► runLook ─► sendIdeas                                                  │
                                                          ├─ topUpMarket, listings, backdrop                                    │
                                                          ├─ runThemes (3a, weekly) ─┐                                          │
                                                          ├─ runReactions (3b, daily)┼─► factsFor, judge (3c)                    │
                                                          ├─ RenderPicks ─► owner (+ channel under channelNote)                  │
                                                          └─ recordVerdicts ─► scorecard.json ◄─────────────────────────────────┘
                                                                                  │
                                                          recentPicks (8-week no-repeat) ◄┘

 Every model call, from anywhere:  Relay.Begin (one folder a run) ─► Run.Ask ─► claude -p (Sonnet / Haiku / Opus) ─► reply file
 Every run's data:                 runcache (data/cache/<kind>/)          Every log line and error reply: logging scrub
```

What passes between the functions:

- **Brief → closer look:** the brief's articles (`look.Cited`) travel through `pending-look.json`. The reactions need an article to explain a move, and the verdicts cite them.
- **Brief → weekly themes:** the headlines the briefs carried (`covered.json`, three weeks, via [`recentHeadlines`](../internal/app/themes.go#L234)) become each industry's "named in N recent headlines".
- **Market history → closer look:** the two years of bars in `data/market` are all the themes and reactions measure from.
- **Closer look and `/analyse` → scorecard:** every BUY or SELL shown is recorded. `/scorecard` grades them. The weekly themes read it back: a company given the same verdict in the last eight weeks goes on the "earlier picks" list and is not shown again.
- **Brief and `/analyse` → `/share`:** the last delivery is remembered in memory, and `/share` posts it.

---

## 1. Start-up and the four loops

**Starts from:** the process starting. On Fly, the Docker `CMD` runs `market-watch` with no flags.

**Entry point:** [`main`](../cmd/market-watch/main.go#L27) → [`run`](../cmd/market-watch/main.go#L57) → [`App.Serve`](../internal/app/app.go#L728)

**What it does:** loads and checks the configuration, builds the service, then runs four loops side by side until the process is told to stop.

**Files:** [cmd/market-watch/main.go](../cmd/market-watch/main.go), [internal/app/app.go](../internal/app/app.go), [config/config.go](../config/config.go), [config/prompts.go](../config/prompts.go), [config/prefs.go](../config/prefs.go), [internal/logging/scrub.go](../internal/logging/scrub.go)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`main`](../cmd/market-watch/main.go#L27) | Parses the flags `--once`, `--share`, `--check`, `--clear` and `--fold`. `--fold` goes straight to [`runFold`](../cmd/market-watch/main.go#L134) (see 15). |
| 2 | [`config.Load`](../config/config.go#L180) | Reads every environment variable (and `.env` locally), reporting all the missing ones at once. |
| 3 | [`config.LoadPrompts`](../config/prompts.go#L61) → [`CheckPrompts`](../config/prompts.go#L111) | Reads [prompts.md](../config/prompts.md) and stops the process if a section lost a marker a parser depends on. |
| 4 | [`logging.New`](../internal/logging/scrub.go#L35) | Wraps the log handler so every line has its secrets removed (see 14). |
| 5 | [`app.New`](../internal/app/app.go#L141) | Builds the `App`: loads `prefs.yaml`, `covered.json`, `runs.json`, `candidates.json`, `themes.json` and `scorecard.json` from the data folder; creates the Telegram, Finnhub, chart, FRED, Tavily, SEC, Massive and Nasdaq clients; and wires each model stage to the relay. A switched-off feature leaves its field nil (`TRIAGE`, `REVIEW`, `DISCOVER`, `IDEAS`, `CONSENSUS`). |
| 6 | `signal.NotifyContext` in [`run`](../cmd/market-watch/main.go#L57) | SIGTERM cancels the context, so a brief in flight finishes its delivery. |
| 7 | [`Serve`](../internal/app/app.go#L728) | Publishes the command menu ([`BotCommands`](../internal/app/commands.go#L43) → [`SetMyCommands`](../internal/telegram/updates.go#L125)) and discards commands sent while it was down ([`DrainUpdates`](../internal/telegram/updates.go#L131)). |
| 8 | [`Serve`](../internal/app/app.go#L728) | Starts four goroutines: **[`RunScheduler`](../internal/app/app.go#L697)** (2), **[`RunLooks`](../internal/app/look.go#L73)** (3), **[`RunMarket`](../internal/app/marketdata.go#L54)** (4) and **[`Bot.Poll`](../internal/telegram/updates.go#L77)** → [`HandleMessage`](../internal/app/commands.go#L61) (6–10). The first to fail stops the others. |

**Links:** everything else runs from these four loops. The `App` struct holds the mutex `running`, which lets only one brief or closer look run at a time.

---

## 2. The daily brief

**Starts from:** [`RunScheduler`](../internal/app/app.go#L697) at `REPORT_AT` (07:30) in `SCHEDULE_TZ` (America/New_York), weekdays only. [`config.NextRun`](../config/config.go#L350) works out the next time afresh each round, so daylight saving never shifts it. That is 19:30 Singapore time until 1 November.

**Entry point:** [`publishScheduled`](../internal/app/app.go#L330) → [`brief`](../internal/app/app.go#L338)`(share=true, later=true)` → [`sendReport`](../internal/app/app.go#L425)

**What it does:** gathers the day's prices, SEC filings, news searches and about forty feeds. Sonnet rates and files every article, and a second Sonnet pass reviews where each landed. Opus writes the brief. Haiku spots new company names, each checked against the exchange. The brief goes to the owner, then the channel, and the closer look is queued for 20 minutes later.

**Files:**
- **App:** [app.go](../internal/app/app.go), [prices.go](../internal/app/prices.go), [filings.go](../internal/app/filings.go), [search.go](../internal/app/search.go), [movers.go](../internal/app/movers.go), [channel.go](../internal/app/channel.go), [look.go](../internal/app/look.go)
- **Packages:** [feed](../internal/feed/), [sec](../internal/sec/), [search](../internal/search/), [triage](../internal/triage/), [history](../internal/history/), [report](../internal/report/), [discover](../internal/discover/), [prices](../internal/prices/), [telegram](../internal/telegram/)
- **Config:** [sectors.yaml](../config/sectors.yaml), [companies.yaml](../config/companies.yaml), [sources.yaml](../config/sources.yaml), [prompts.md](../config/prompts.md)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`brief`](../internal/app/app.go#L338) | Takes the run lock; stops if no chat is registered; opens the run cache (`brief`) and a relay run folder. |
| 2 | [`collectPrices`](../internal/app/prices.go#L45), in the background | The 12 benchmark funds and every followed share, from Finnhub ([`prices.Client.Fetch`](../internal/prices/prices.go#L71)) at about one a second. Whatever Finnhub misses comes from the charts ([`fromCharts`](../internal/app/prices.go#L70) → [`History.Fetch`](../internal/prices/history.go#L88)). |
| 3 | [`collectFilings`](../internal/app/filings.go#L25) → [`sec.Client.Collect`](../internal/sec/sec.go#L106) | Each followed company's recent material 8-Ks, as articles. |
| 4 | [`collectSearch`](../internal/app/search.go#L26) → [`search.Queries`](../internal/search/queries.go#L56) → [`search.Client.Collect`](../internal/search/search.go#L93) | Tavily news searches (general ones, plus one per sector) since the previous brief ([`searchSince`](../internal/app/search.go#L56)). |
| 5 | [`movers`](../internal/app/movers.go#L33) → [`searchMovers`](../internal/app/movers.go#L71) → [`search.Merge`](../internal/search/search.go#L273) | Once the prices are in: up to 5 followed shares that moved at least 3 points more than the S&P 500 fund each get a "why did it move" search. |
| 6 | [`feed.Collect`](../internal/feed/collect.go#L115) | The gathering pipeline:<br>• [`Fetcher.Fetch`](../internal/feed/fetch.go#L62) reads every enabled feed at once;<br>• the filings and search results are added;<br>• [`DropStale`](../internal/feed/collect.go#L228) drops anything more than a week old;<br>• [`Dedupe`](../internal/feed/collect.go#L251) removes duplicates by URL and then by title;<br>• [`Match`](../internal/feed/collect.go#L404) tags articles by the company names in `companies.yaml`;<br>• [`Result.triage`](../internal/feed/collect.go#L170) → [`Triager.Triage`](../internal/triage/triage.go#L100) has **Sonnet** rate every article 1–5 and place it in up to 2 sectors, in batches of 60;<br>• [`Limit`](../internal/feed/collect.go#L597) ranks by [`score`](../internal/feed/collect.go#L548) and cuts to `MAX_ARTICLES`. |
| 7 | [`history.Store.Mark`](../internal/history/history.go#L63) | Marks stories earlier briefs carried (`covered.json`), so the writer treats them as updates rather than news. |
| 8 | [`triage.TopUp`](../internal/triage/topup.go#L27) → [`Reviewer.Review`](../internal/triage/review.go#L62) | Sections with fewer than 10 articles are offered the ones rated 3. **Sonnet with the web** then re-checks every placement, and keeps a top-up only if it names the section. |
| 9 | [`collectLevels`](../internal/app/filings.go#L88) → [`FRED.Fetch`](../internal/prices/fred.go#L147); [`trendsFor`](../internal/app/movers.go#L121) | Yields, fed funds, the S&P 500, the VIX and inflation; and each mover's averages and range. |
| 10 | [`report.Generator.Generate`](../internal/report/generate.go#L64) | [`buildPrompt`](../internal/report/prompt.go#L111) builds the prompt, **Opus** writes the brief through the relay (stage `brief`), and [`parseResponse`](../internal/report/parse.go#L20) splits it into the overview and sections. If there are no articles, a "no news" message goes out instead and the run stops. |
| 11 | [`discover.Finder.Find`](../internal/discover/discover.go#L64) → [`FIGI.Verify`](../internal/discover/verify.go#L74) → [`Store.Note`](../internal/discover/store.go#L54) → [`priceCandidates`](../internal/app/prices.go#L284) | **Haiku** names the companies in the news that nobody follows. Each ticker is checked against OpenFIGI, counted in `candidates.json` and priced. |
| 12 | [`telegram.RenderWith`](../internal/telegram/render.go#L87) | The report becomes Telegram HTML messages, each under 4,096 characters. |
| 13 | [`Bot.DeleteMessages`](../internal/telegram/client.go#L169) | With `REPLACE_PREVIOUS`, deletes the last brief. This happens after writing the new one, so a failed run loses nothing. |
| 14 | [`Bot.SendReport`](../internal/telegram/client.go#L111) | Sends to the owner, and records the message ids in `prefs.yaml`. |
| 15 | [`remember`](../internal/app/channel.go#L52) | Keeps this delivery in memory for `/share`. |
| 16 | [`Covered.Record`](../internal/history/history.go#L89), [`Runs.Add`](../internal/history/runs.go#L121) (+ [`recordSearch`](../internal/app/search.go#L80)) | Writes what the brief covered (only after delivery) and the run's numbers for `/stats`. |
| 17 | [`shareBrief`](../internal/app/channel.go#L110) | Posts the same messages to the channel (see 8). |
| 18 | [`lookFrom`](../internal/app/ideas.go#L83) → [`queueLook`](../internal/app/look.go#L40) | Writes `pending-look.json` holding the brief's articles, due `LookDelay` (20 minutes) later. If that write fails, the closer look runs at once instead. |
| 19 | [`RunScheduler`](../internal/app/app.go#L697) | On any error, [`reportFailure`](../internal/app/app.go#L950) tells the owner (see 11). Nothing is retried. |

**Links:**
- Feeds **3** (closer look), through `pending-look.json`.
- Feeds **3a** through `covered.json` headlines.
- Feeds **8** (`/share`) and **10** (`/stats`).
- Uses **12** for each model call and **13** for the run cache.
- The next brief reads `covered.json` and `candidates.json` from this one.

---

## 3. Worth a closer look

**Starts from:**
- The daily run: [`RunLooks`](../internal/app/look.go#L73) checks `pending-look.json` every 30 seconds, and [`sendDueLook`](../internal/app/look.go#L91) sends it when due. A look more than 6 hours late is dropped.
- `/now` and `--once`: [`brief`](../internal/app/app.go#L338) calls [`sendIdeas`](../internal/app/ideas.go#L91) straight away.

**Entry point:** [`runLook`](../internal/app/look.go#L120) (takes the run lock and opens a `look` relay run) → [`sendIdeas`](../internal/app/ideas.go#L91)

**What it does:**
- **Once a week** (only on the scheduled run's first look of the week), up to 10 companies from the weekly themes (3a).
- **Every day**, up to 3 shares that moved far beyond their usual on the news (3b).
- Each company gets a BUY, HOLD or SELL verdict (3c). Only BUYs and SELLs are shown, only for companies the watchlists don't follow, and a day with none sends nothing.

**Files:** [ideas.go](../internal/app/ideas.go), [look.go](../internal/app/look.go), [themes.go](../internal/app/themes.go), [reactions.go](../internal/app/reactions.go), [marketdata.go](../internal/app/marketdata.go), [research.go](../internal/app/research.go), [channel.go](../internal/app/channel.go), [internal/ideas](../internal/ideas/), [internal/market](../internal/market/), [internal/telegram/ideas.go](../internal/telegram/ideas.go)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`sendIdeas`](../internal/app/ideas.go#L91) | Weekly if `lk.Scheduled` and [`ThemeLog.DoneThisWeek`](../internal/ideas/themelog.go#L90) is false (ISO week, New York time). The budget is 75 minutes for a weekly run, else 25. Opens the run cache (`recommendations`). |
| 2 | [`topUpMarket`](../internal/app/marketdata.go#L89) | Fetches the last 3 sessions into the store, waiting up to 4 minutes (see 4). |
| 3 | [`listings`](../internal/app/marketdata.go#L109) → [`consensus.Client.Listings`](../internal/consensus/listings.go#L23) | Nasdaq's list of every US listing: market value, sector and industry. It is kept in `data/market/listings.json` for 20 hours. With no list at all, the look stops here. |
| 4 | [`backdrop`](../internal/app/research.go#L85) | FRED's oil, gas, copper, dollar, 10-year yield, credit spread and breakeven inflation, shown above every verdict. |
| 5 | [`newFollowing`](../internal/app/ideas.go#L202) | What the watchlists follow, which the look leaves to the brief. |
| 6 | [`runThemes`](../internal/app/themes.go#L72), weekly only | See **3a**. |
| 7 | [`runReactions`](../internal/app/reactions.go#L45) | See **3b**. |
| 8 | [`ThemeLog.Add`](../internal/ideas/themelog.go#L61) | Writes the week to `themes.json` however it went, so it runs once a week. |
| 9 | [`telegram.RenderPicks`](../internal/telegram/ideas.go#L90) → [`Bot.SendReport`](../internal/telegram/client.go#L111) | Renders and sends to the owner: the themes with their picks, then the reactions, then the earlier picks. The ids are added to `LastBrief`, so they are cleared with the brief. |
| 10 | [`shareIdeas`](../internal/app/channel.go#L140) | If the brief went to the channel (`lk.Share`), posts a copy rendered with `ForChannel`, headed by [`channelNote`](../internal/telegram/ideas.go#L31), the warning that must stay. |
| 11 | [`recordVerdicts`](../internal/app/ideas.go#L359) | Writes every BUY and SELL shown to the scorecard (see 5). |

**Links:**
- Reads **2** (the brief's articles) and **4** (the bars).
- Writes **5** (the scorecard), which **3a** reads back to avoid repeats.
- Uses **12** for each model call and **13** for the run cache.

### 3a. Weekly themes

**Entry point:** [`runThemes`](../internal/app/themes.go#L72)

**What it does:** measures every US company over two years, and picks the leaders and the popular and early industries. Sonnet sorts the leaders into up to 3 themes, and Opus with the web scouts up to 2 early ones. Opus with the web researches each theme for its unpriced part and up to 4 companies. Up to 16 are judged against their theme's valuation, and up to 10 BUYs or SELLs are shown.

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`loadPanel`](../internal/app/themes.go#L222) → [`market.Store.Load`](../internal/market/store.go#L210) | Two years of bars for companies worth at least US$1bn, plus SPY. With less than 13 months of sessions it stops: "the market's history is still filling". |
| 2 | [`market.Measure`](../internal/market/screen.go#L89) | Each company's returns over about 2 years (`TwoYears - Month` sessions), 12 months less the last month, 6 and 3 months, plus its swing, averages, trading and biggest one-day rise. |
| 3 | [`singaporeStocks`](../internal/app/marketdata.go#L146) | The Straits Times Index's 30 companies ([config/singapore.yaml](../config/singapore.yaml)), priced from the charts in US dollars. |
| 4 | [`market.Leaders`](../internal/market/screen.go#L167) | The best 150. Only eligible companies count (US$2bn+, US$5+, US$20m a day, 13 months of history), above their 200-day average, not risen mostly in one day, and not pinned to a takeover offer. Each is ranked on four measures against SPY. |
| 5 | [`recentHeadlines`](../internal/app/themes.go#L234) → [`market.Mentions`](../internal/market/names.go#L77) | Counts the headlines naming each company, from `covered.json`. A company is matched by its full name, or by a first word no other company's name has. |
| 6 | [`market.Industries`](../internal/market/screen.go#L267) → [`Popular`](../internal/market/screen.go#L377) / [`Early`](../internal/market/screen.go#L382) | Scores every industry with at least 4 eligible members:<br>• **Popular:** its 6- and 12-month median, breadth, money flowing in against a year ago, and headlines.<br>• **Early:** only for industries whose year lags the typical industry's, whose 3 months beat the typical industry's, and whose share above the 50-day average rose more than the typical industry's did. |
| 7 | [`ideas.Sorter.Sort`](../internal/ideas/themes.go#L59) | **Sonnet** (stage `themes`) sorts the leaders into up to 3 popular themes. It is shown the popular industries, the headlines and last week's names. |
| 8 | [`ideas.Scout.Find`](../internal/ideas/themes.go#L176) | **Opus with the web** (stage `scout`) finds up to 2 early themes. It starts from the early industries; if there are none, it is told so and searches the web. |
| 9 | [`themeFigures`](../internal/app/themes.go#L292) | Each theme's figures, for the research and for display. |
| 10 | [`recentPicks`](../internal/app/themes.go#L242) → [`Scorecard.Since`](../internal/ideas/scorecard.go#L425) | The theme picks of the last 8 weeks (`RepeatWindow`). |
| 11 | [`researchThemes`](../internal/app/themes.go#L251) → [`ideas.Researcher.Research`](../internal/ideas/themes.go#L330) | **Opus with the web** (stage `research`), 2 themes at a time. It is given last week's research on a theme of the same name ([`ThemeLog.Previous`](../internal/ideas/themelog.go#L117)) and the recent picks. It returns the priced-in and unpriced parts and up to 4 companies, each checked against OpenFIGI ([`ideas.verify`](../internal/ideas/ideas.go#L85)). |
| 12 | [`runThemes`](../internal/app/themes.go#L72), the round-robin loop | Up to 16 candidates, taken a theme at a time in turn, skipping followed companies. A company picked before carries its earlier verdict (`idea.Before`). |
| 13 | [`factsFor`](../internal/app/ideas.go#L276) | See **3c**. |
| 14 | [`value`](../internal/app/themes.go#L371) | Reads the accounts of up to 8 top members per theme ([`readAccounts`](../internal/app/themes.go#L466)) to get the theme's medians. Compares each candidate's [`Multiples`](../internal/fundamentals/multiples.go#L68) with the theme and with its own five year ends ([`pastMultiples`](../internal/app/themes.go#L504)), adds [`WarningSigns`](../internal/ideas/valuation.go#L188), and sets [`BuyClosed`](../internal/ideas/valuation.go#L109). |
| 15 | [`judge`](../internal/app/ideas.go#L226) | See **3c**. |
| 16 | [`best`](../internal/app/ideas.go#L490) | Keeps BUYs and SELLs only, drops any with the same verdict as within the last 8 weeks, and caps at 10 (`PicksShown`), most confident first. |
| 17 | [`earlierPicks`](../internal/app/themes.go#L528) → [`panelPath`](../internal/app/marketdata.go#L183), [`ideas.Called`](../internal/ideas/scorecard.go#L409) | The last 8 weeks' picks, with how each has done against SPY. |

### 3b. Daily reactions

**Entry point:** [`runReactions`](../internal/app/reactions.go#L45)

**What it does:** finds shares that moved at least 3 times their usual daily move on at least twice their usual trading, and which the brief's articles explain. It judges up to 6, putting results stories first, and shows up to 3 BUYs or SELLs.

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`market.Store.Load`](../internal/market/store.go#L210) | The last 120 days. It needs at least 62 sessions. |
| 2 | [`market.Moves`](../internal/market/screen.go#L416) | The latest session's outsized moves, among eligible companies. |
| 3 | [`fundamentals.Relevant`](../internal/fundamentals/news.go#L92) | For the 40 largest moves, the brief's articles (`lk.Cited`) that name the company. A move no article explains is skipped. |
| 4 | `resultsWords` sort | Results or outlook stories first, then the largest move against its usual. Takes the top 6 (`ReactionsJudged`). |
| 5 | [`factsFor`](../internal/app/ideas.go#L276) + [`WarningSigns`](../internal/ideas/valuation.go#L188) | See **3c**. There is no theme valuation here, only the warning signs. |
| 6 | [`judge`](../internal/app/ideas.go#L226) | See **3c**. The verdict says what changed, how the share moved, and whether the move over- or under-matched the news. |
| 7 | [`best`](../internal/app/ideas.go#L490) | BUYs and SELLs only, up to 3 (`ReactionsShown`). |

### 3c. Facts and verdicts

**Entry point:** [`factsFor`](../internal/app/ideas.go#L276) (4 companies at a time) → [`ideaFacts`](../internal/app/ideas.go#L315); then [`judge`](../internal/app/ideas.go#L226) → [`ideas.Judge.Judge`](../internal/ideas/judge.go#L54)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`ideaFacts`](../internal/app/ideas.go#L315) → [`marketFor`](../internal/app/prices.go#L160) | Price history and last price from the charts ([`ChartSymbol`](../internal/prices/symbols.go#L31)). A US listing prefers Finnhub's live quote ([`quoteFor`](../internal/app/commands.go#L631)). |
| 2 | [`fundamentals.Client.Fetch`](../internal/fundamentals/metrics.go#L215) | For a US SEC filer, 5 years of accounts, plus what `/analyse` reads:<br>• [`AddBusiness`](../internal/fundamentals/business.go#L39);<br>• [`addPressNews`](../internal/app/prices.go#L263) (Finnhub news, not Tavily);<br>• [`addExpectations`](../internal/app/research.go#L43) → [`consensus.Client.Fetch`](../internal/consensus/consensus.go#L132);<br>• [`addRelease`](../internal/app/research.go#L70) → [`sec.EarningsRelease`](../internal/sec/release.go#L40).<br>The fact sheet is [`Snapshot.Table`](../internal/fundamentals/table.go#L46) + [`SensitivityFacts`](../internal/fundamentals/sensitivity.go#L140). |
| 3 | [`ideaFacts`](../internal/app/ideas.go#L315), fallback | Without accounts (a non-filer, or Singapore), the sheet is [`TradingFacts`](../internal/fundamentals/market.go#L25) plus "no accounts were read". |
| 4 | [`ideas.Judge.Judge`](../internal/ideas/judge.go#L54) | **Opus** (stage `verdicts`), 3 companies a call, 2 calls at a time. For each: verdict, confidence, case, numbers, catalyst, sensitivity and risk. |
| 5 | [`hold`](../internal/ideas/judge.go#L138) | The code's rules, which the model cannot bend:<br>• a BUY that `BuyClosed` rules out becomes a HOLD, with the reason given;<br>• no accounts means low confidence at most. That covers every Singapore pick. |

---

## 4. Market history

**Starts from:** [`RunMarket`](../internal/app/marketdata.go#L54), one of the four loops. [`topUpMarket`](../internal/app/marketdata.go#L89) also runs before each closer look.

**What it does:** keeps two years of the whole US market's daily bars in `data/market/`, one gzipped file per session (about 40MB). They come from Massive's free plan at 5 requests a minute, so the first fill takes about 2 hours.

**Files:** [internal/app/marketdata.go](../internal/app/marketdata.go), [internal/market/sync.go](../internal/market/sync.go), [internal/market/store.go](../internal/market/store.go), [internal/prices/massive.go](../internal/prices/massive.go), [internal/consensus/listings.go](../internal/consensus/listings.go)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`RunMarket`](../internal/app/marketdata.go#L54) | Does nothing without `MASSIVE_API_KEY`. |
| 2 | [`Store.Sync`](../internal/market/sync.go#L42) | Asks for up to 10 missing days (`marketChunk`), newest first, back to `Reach` (2 years less 5 days: about 496 sessions). Skips weekends, days it already has ([`Known`](../internal/market/store.go#L66)) and today before 18:00 New York. |
| 3 | [`session`](../internal/market/sync.go#L95) → [`Massive.Session`](../internal/prices/massive.go#L73) | One day's bars for every listing. It retries 3 times, except on a refused key or a cancelled context. |
| 4 | [`Store.Save`](../internal/market/store.go#L76) | Keeps common shares that traded at least $1 and $1m that day. An empty old day is marked `.closed` (a holiday). |
| 5 | [`Massive.Splits`](../internal/prices/massive.go#L143) | Reads the splits since the last sync into `splits.json`. [`Load`](../internal/market/store.go#L210) applies them. |
| 6 | [`RunMarket`](../internal/app/marketdata.go#L54) | After a full chunk it goes again at once. After an error it waits 5 minutes (24 hours on a refused key); when up to date, 3 hours. |

**Links:** read by **3a**, **3b** and `--check` (whose `history_missing_days` comes from [`Missing`](../internal/market/sync.go#L146)).

---

## 5. Scorecard

**What it does:**
- Records every BUY and SELL shown, and every `/analyse` verdict.
- Grades each against the S&P 500 (SPY) in US dollars once it is a week old. Both the share and SPY are measured from the first open after the verdict.
- A BUY is right if it beats SPY by at least 5 points a year, pro-rated (`Clearly`); a SELL, if it trails by as much.
- Stored in `data/scorecard.json`, and **held in memory** while the service runs.

**Files:** [internal/ideas/scorecard.go](../internal/ideas/scorecard.go), [internal/app/ideas.go](../internal/app/ideas.go), [internal/app/commands.go](../internal/app/commands.go)

**Recording:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`recordVerdicts`](../internal/app/ideas.go#L359) | For the closer look: [`NewRecord`](../internal/ideas/scorecard.go#L207) for each idea shown, with its chart symbol, source (`theme` or `reaction`), theme and today's dollar rate ([`dollarRates`](../internal/app/ideas.go#L450)). |
| 2 | [`recordAnalysis`](../internal/app/commands.go#L587) | For `/analyse`: source `analysis`. The same verdict on the same share within 24 hours counts once ([`Repeats`](../internal/ideas/scorecard.go#L266)). |
| 3 | [`Scorecard.Add`](../internal/ideas/scorecard.go#L255) | Appends in memory and rewrites the whole file. |

**`/scorecard`**, via [`handleScorecard`](../internal/app/ideas.go#L409):

| Step | Code | What it does |
|---|---|---|
| 1 | [`Scorecard.Due`](../internal/ideas/scorecard.go#L285) | The charts of verdicts at least 7 days old (`MinAge`), up to 60. |
| 2 | [`pathFor`](../internal/app/ideas.go#L386) → [`History.Fetch`](../internal/prices/history.go#L88) | Each one's sessions from the chart source, and SPY's. |
| 3 | [`dollarRates`](../internal/app/ideas.go#L450) | Exchange-rate history for shares priced abroad. |
| 4 | [`Scorecard.Settle`](../internal/ideas/scorecard.go#L322) | Writes down each verdict's entry, the next session's open, once that session has happened. |
| 5 | [`Scorecard.Summary`](../internal/ideas/scorecard.go#L438) | Right or wrong per verdict, split by confidence and by source (theme, reaction, analysis, news). |

**Links:** written by **3** and **6**; read by **3a** ([`recentPicks`](../internal/app/themes.go#L242) and [`earlierPicks`](../internal/app/themes.go#L528)). It is **not** given to the models.

---

## 6. `/analyse <ticker>`

**Starts from:** the owner sending `/analyse NVDA`. The spellings `/analyze` and `/accounts` route here too.

**Entry point:** [`handleAnalyse`](../internal/app/commands.go#L459)

**What it does:** reads a company's SEC filings, its trading, its news, what analysts expect and its latest results release. Opus writes an analysis with the house method and ends with a BUY, HOLD or SELL verdict against the S&P 500 over 12 months.

**Files:**
- [internal/app/commands.go](../internal/app/commands.go), [research.go](../internal/app/research.go), [prices.go](../internal/app/prices.go)
- [internal/fundamentals](../internal/fundamentals/), [internal/sec](../internal/sec/), [internal/consensus](../internal/consensus/)
- [internal/telegram/render.go](../internal/telegram/render.go), [config/method.md](../config/method.md)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`handleAnalyse`](../internal/app/commands.go#L459) | Opens the run cache (`analysis`), replies "reading…", and sets its own budget of 5 minutes plus one model call. |
| 2 | [`fundamentals.Client.Fetch`](../internal/fundamentals/metrics.go#L215) | 5 years of XBRL accounts from EDGAR (US GAAP or IFRS), with the year so far. |
| 3 | [`quoteFor`](../internal/app/commands.go#L631) | The share price, which turns the filed figures into multiples. |
| 4 | [`AddBusiness`](../internal/fundamentals/business.go#L39) | The business description from the annual report (10-K or 20-F). |
| 5 | [`tradingFor`](../internal/app/prices.go#L145) → [`Summarise`](../internal/prices/history.go#L281) | Returns, averages, range, VWAP and volatility. |
| 6 | [`addNews`](../internal/app/prices.go#L222) | Finnhub company news plus 2 Tavily searches ([`searchCompany`](../internal/app/research.go#L106)), which spend 2 credits. The results are filtered by [`Relevant`](../internal/fundamentals/news.go#L92). |
| 7 | [`addExpectations`](../internal/app/research.go#L43), [`addRelease`](../internal/app/research.go#L70), [`backdrop`](../internal/app/research.go#L85) | Nasdaq forecasts, targets, insiders, short interest and funds; the latest results release; the FRED backdrop. |
| 8 | [`Relay.Begin`](../internal/relay/relay.go#L95) → [`Analyzer.Analyze`](../internal/fundamentals/analyze.go#L47) | **Opus** (stage `analysis`) reads [`Snapshot.Table`](../internal/fundamentals/table.go#L46), with [method.md](../config/method.md) appended to its system prompt. |
| 9 | [`SplitRelated`](../internal/fundamentals/related.go#L54) → [`VerifyRelated`](../internal/fundamentals/related.go#L90) | Cuts out the "companies to read next to it" table and checks those tickers against OpenFIGI. |
| 10 | [`SplitVerdict`](../internal/fundamentals/verdict.go#L27) | Takes out THE VERDICT section. |
| 11 | [`analysisMessages`](../internal/app/commands.go#L567) → [`RenderAnalysis`](../internal/telegram/render.go#L609) | The verdict first, then the analysis and the related list. |
| 12 | [`Bot.SendReport`](../internal/telegram/client.go#L111) | Sends to the owner. |
| 13 | [`rememberFor`](../internal/app/channel.go#L57) | Keeps both the owner's copy and the channel's copy, which carries the warning, for `/share`. |
| 14 | [`recordAnalysis`](../internal/app/commands.go#L587) | Writes the verdict to the scorecard (see 5). |

**Links:** feeds **5** and **8**. The closer look's verdicts (**3c**) read the same sources, apart from the 2 Tavily searches.

---

## 7. `/now`

**Entry point:** [`handleNow`](../internal/app/commands.go#L157) → [`SendReport`](../internal/app/app.go#L315) → [`brief`](../internal/app/app.go#L338)`(share=false, later=false)`

**What it does:** the whole of **2**, to the owner only. It is followed at once by **3** with the **reactions only**: `lk.Scheduled` is false, so the weekly themes never run from `/now`. Nothing goes to the channel until `/share`. If no chat is registered yet, `/now` registers the sender's chat first.

**Links:** **2**, **3b**, **8**.

---

## 8. The channel and `/share`

**What it does:** the channel (`TELEGRAM_CHANNEL_ID`) is read-only for other readers.
- The **daily brief** is posted there automatically ([`shareBrief`](../internal/app/channel.go#L110)).
- The **closer look** is posted there only when the brief was ([`shareIdeas`](../internal/app/channel.go#L140)), under [`channelNote`](../internal/telegram/ideas.go#L31).
- **`/share`** ([`handleShare`](../internal/app/channel.go#L165)) posts whichever of `/now`'s brief or `/analyse`'s analysis arrived last. It never posts the closer look.

**Files:** [internal/app/channel.go](../internal/app/channel.go), [internal/telegram/client.go](../internal/telegram/client.go), [internal/telegram/ideas.go](../internal/telegram/ideas.go)

**Sequence for `/share`:** [`latest`](../internal/app/channel.go#L66) gets the last delivery. [`share`](../internal/app/channel.go#L75) guards against posting it twice, then calls [`Bot.Broadcast`](../internal/telegram/client.go#L118), using the channel copy where there is one. If the channel refuses, the owner is told.

**Links:** fed by **2**, **3**, **6** and **7**.

---

## 9. `/watchlist` and `/sources`

**What it does:** changes what the service follows without a deploy. The changes are kept in `prefs.yaml` on the data volume, on top of [companies.yaml](../config/companies.yaml) and [sources.yaml](../config/sources.yaml). `scripts/sync-from-fly.sh` writes them into `config/` (see 16).

**Files:** [internal/app/commands.go](../internal/app/commands.go), [config/prefs.go](../config/prefs.go), [config/watchlist.go](../config/watchlist.go), [config/fold.go](../config/fold.go)

| Command | Code | What it does |
|---|---|---|
| `/watchlist` | [`renderWatchlists`](../internal/app/commands.go#L350) | Lists the sectors and companies in force. |
| `/watchlist add <sector> <ticker or name>` | [`companyFrom`](../internal/app/commands.go#L294) → [`Prefs.AddCompany`](../config/prefs.go#L108) → [`Save`](../config/prefs.go#L194) | Follows a company. A bare ticker gets its name from the SEC index. |
| `/watchlist remove <sector> <term>` | [`Prefs.RemoveCompany`](../config/prefs.go#L138) | Stops following one. |
| `/watchlist edits` / `reset` | [`renderEdits`](../internal/app/commands.go#L398) / [`Prefs.ResetEdits`](../config/prefs.go#L166) | Shows or drops the changes made from Telegram. |
| `/sources` / `/sources on\|off <id>` | [`renderSources`](../internal/app/commands.go#L418) / [`Prefs.SwitchFeed`](../config/prefs.go#L174) | Lists the feeds, or switches one. |

**Links:** the next **2** reads the lists in force, for prices, filings, matching, sections and "already followed".

---

## 10. The other commands

All arrive through [`Bot.Poll`](../internal/telegram/updates.go#L77) → [`HandleMessage`](../internal/app/commands.go#L61). It drops commands from any chat but the owner's, routes on the command, and sends any error back scrubbed.

| Command | Code | What it does |
|---|---|---|
| `/start` | [`handleStart`](../internal/app/commands.go#L137) | Registers the chat as the owner's, once. |
| `/help` | `helpText` in [commands.go](../internal/app/commands.go) | What the bot can do. |
| `/schedule` | [`handleSchedule`](../internal/app/commands.go#L229) | When the next brief is due, in both time zones. |
| `/stats` | [`handleStats`](../internal/app/commands.go#L621) → [`Runs.Summary`](../internal/history/runs.go#L138) | What recent briefs found, placed, moved and cost (`runs.json`). |
| `/clear` | [`handleClear`](../internal/app/commands.go#L186) → [`ClearChat`](../internal/app/commands.go#L193) → [`SweepMessages`](../internal/telegram/client.go#L150) | Deletes the bot's messages from the last 300 ids. Telegram only allows deleting messages under 48 hours old. |

---

## 11. Failure alerts

**Entry point:** [`reportFailure`](../internal/app/app.go#L950), called by [`RunScheduler`](../internal/app/app.go#L697) when a scheduled brief fails.

**What it does:** sends the owner the scrubbed error and when the next attempt is. It never retries, because a retry spends the plan on the same error. The closer look and the channel report their own failures to the owner ([`shareBrief`](../internal/app/channel.go#L110), [`shareIdeas`](../internal/app/channel.go#L140)).

---

## 12. Model calls (the relay)

**What it does:** is the only way the app reaches a model. Each call writes a request file, runs a fresh headless `claude -p` on the owner's subscription, and writes the reply beside it. There is no API key and no API client.

**Files:** [internal/relay/relay.go](../internal/relay/relay.go), [internal/relay/answer.go](../internal/relay/answer.go), [config/prompts.md](../config/prompts.md)

**Sequence:**

| Step | Code | What it does |
|---|---|---|
| 1 | [`Relay.Begin`](../internal/relay/relay.go#L95) | Opens `data/relay/<time>-<kind>/` (brief, look, analysis-TICKER) and puts it on the context. [`prune`](../internal/relay/relay.go#L129) keeps the last 40. |
| 2 | [`Stage`](../internal/relay/relay.go#L169) / [`Plain`](../internal/relay/relay.go#L177) | Adapt the relay to each package's `Completer`, fixing the stage name. |
| 3 | [`Run.Ask`](../internal/relay/relay.go#L226) | Writes `NN-stage-request.txt`, notes it in the ledger ([`Note`](../internal/relay/relay.go#L275)), asks, writes `NN-stage-reply.txt`, and copies both into the run cache. |
| 4 | [`Claude.Answer`](../internal/relay/answer.go#L95) | Runs `claude -p` with the stage's model, in the run's own folder. [`childEnv`](../internal/relay/answer.go#L261) strips `ANTHROPIC_API_KEY`. Tools are all off, except web search and fetch for `scout`, `research` and `review`. |

Which model answers each stage (`DefaultModels` in [answer.go](../internal/relay/answer.go)):

| Stage | Model | Web | Used by |
|---|---|---|---|
| `triage` | Sonnet | no | 2: rating and filing articles |
| `review` | Sonnet | yes | 2: checking placements |
| `brief` | Opus | no | 2: writing the brief |
| `names` | Haiku | no | 2: new names |
| `themes` | Sonnet | no | 3a: sorting leaders into themes |
| `scout` | Opus | yes | 3a: early themes |
| `research` | Opus | yes | 3a: researching each theme |
| `verdicts` | Opus | no | 3c: BUY/HOLD/SELL |
| `analysis` | Opus | no | 6: `/analyse` |

`RELAY_ANSWER=session` swaps in [`Session.Answer`](../internal/relay/answer.go#L302), which waits for a person (or a session's subagent) to write the reply file.

---

## 13. Run cache

**Entry point:** [`Cache.Start`](../internal/runcache/runcache.go#L79)`(ctx, kind, subject)`, called by [`brief`](../internal/app/app.go#L338), [`handleAnalyse`](../internal/app/commands.go#L459) and [`sendIdeas`](../internal/app/ideas.go#L91).

**What it does:** keeps the **latest** run of each kind in `data/cache/<kind>/`, where kind is `brief`, `analysis` or `recommendations`. It holds each step's data as JSON ([`Save`](../internal/runcache/runcache.go#L118)), the messages sent, the model calls, and `run.json` ([`Finish`](../internal/runcache/runcache.go#L156)). Secrets are scrubbed. `scripts/sync-cache-from-fly.sh` brings it down from Fly, and a change to the brief, analysis or closer look should start from it rather than a new run.

---

## 14. Secret scrubbing

**Entry point:** [`logging.New`](../internal/logging/scrub.go#L35) wraps the log handler at start-up. [`logging.Scrub`](../internal/logging/scrub.go#L47) cleans the error text sent to the chat ([`HandleMessage`](../internal/app/commands.go#L61), [`reportFailure`](../internal/app/app.go#L950), the channel failures).

**What it does:** replaces every credential the configuration holds with `[redacted]`, because `net/http` puts request URLs, and so the bot token and API keys, into its error messages.

---

## 15. Command-line modes

All in [cmd/market-watch/main.go](../cmd/market-watch/main.go).

| Flag | Code | What it does |
|---|---|---|
| (none) | [`run`](../cmd/market-watch/main.go#L57) → [`Serve`](../internal/app/app.go#L728) | The service (1). |
| `--check` | [`runCheck`](../cmd/market-watch/main.go#L195) | Proves each part without spending anything: Telegram, the owner's chat, the channel, the feeds, Tavily (via its free usage call), one Massive session plus `history_missing_days`, Nasdaq's consensus and listings, the next brief's time, and the Claude Code version and credential. It never asks a model. The deploy script runs it on Fly. |
| `--once` | [`SendReport`](../internal/app/app.go#L315) | One brief plus the reactions, to the owner, then exits. **Calls models.** |
| `--once --share` | [`Publish`](../internal/app/app.go#L324) | The same, also to the channel, with the closer look straight after. **Calls models.** |
| `--clear` | [`runClear`](../cmd/market-watch/main.go#L161) → [`ClearChat`](../internal/app/commands.go#L193) | Deletes the bot's earlier messages. |
| `--fold` | [`runFold`](../cmd/market-watch/main.go#L134) → [`config.Fold`](../config/fold.go#L27) | Writes the Telegram edits in `data/prefs.yaml` into `config/companies.yaml` and `config/sources.yaml`, changing only the lines it must. It needs no credentials. |

---

## 16. Deploy and sync scripts

| Script | What it does | Links |
|---|---|---|
| [fly-deploy.sh](../scripts/fly-deploy.sh) | Creates the app and volume if they are missing, imports the secrets from `.env` (printing names only), runs `fly deploy`, then runs `--check` on the machine as the app user. | 15 (`--check`) |
| [sync-from-fly.sh](../scripts/sync-from-fly.sh) | Copies `prefs.yaml`, `covered.json`, `runs.json`, `candidates.json` and `scorecard.json` from Fly into `./data`, backing up what they replace to `data/.backup/<time>/`; a file Fly doesn't have is left alone. Then runs `--fold`. | 9, 15 (`--fold`) |
| [sync-cache-from-fly.sh](../scripts/sync-cache-from-fly.sh) | Copies the latest `brief`, `analysis` and/or `recommendations` run cache from Fly into `data/cache/`. | 13 |

---

## 17. Live tests

Tests that talk to real services, skipped unless their environment switch is set. Those marked **models** call Claude on the owner's plan, so ask before running them.

| Test | Switch | What it does |
|---|---|---|
| [`TestLiveBrief`](../internal/app/live_test.go#L35) | `LIVE_BRIEF=1` | A real brief to the owner's chat. **Models.** |
| [`TestLiveAnalysis`](../internal/app/live_test.go#L52) | `LIVE_ANALYSIS=<ticker>` | A real `/analyse` to the owner's chat. **Models.** |
| [`TestLiveMarket`](../internal/app/live_test.go#L115) | `LIVE_MARKET=1` | Fills `data/market` and logs the leaders, the popular and early industries and the day's moves. No models. |
| [`TestLiveThemes`](../internal/app/live_test.go#L177) | `LIVE_THEMES=1` | A week's themes plus the reactions to the owner only. The week isn't recorded, so the scheduled run still does it. **Models.** |
| [`TestLiveDefaultSourcesStillParse`](../internal/feed/live_test.go#L21) | `MARKET_WATCH_LIVE=1` | Every feed still parses. |
| [`TestLiveFundamentals`](../internal/fundamentals/live_test.go#L24) | `MARKET_WATCH_LIVE=1` | One company's accounts from EDGAR. |
| [`TestLivePrices`](../internal/prices/live_test.go#L17) | `MARKET_WATCH_LIVE=1` | Finnhub quotes. |
| [`TestLiveSearchBesideTheFeeds`](../internal/search/live_test.go#L27) | `SEARCH_LIVE=1` | Search against the feeds. Spends about 15 Tavily credits. |
| [`TestLiveBotTokenIsValid`](../internal/telegram/live_test.go#L16) | `MARKET_WATCH_LIVE=1` | The bot token. |
| [`TestLiveClaude`](../internal/relay/relay_test.go#L411) | `LIVE_CLAUDE=1` | One real `claude -p` call. **Models.** |

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → **4 Function by function** → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

**Next:** [5 Reviewing the code](REVIEW.md): how to review it, from seeing real output to the rules that must hold.
