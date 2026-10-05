**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → **3 How it works** → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

---

# The shape of the code

This page says what every file does, what its functions are for, and how a
day's work travels through them.

This page is the map. [RUNBOOK.md](RUNBOOK.md) says how to operate the
service, and [TASKS.md](TASKS.md) says what isn't built yet. What the service
follows (the sectors, the companies and the feeds) lives in files in
[config/](../config/). So does every instruction it gives a model. Those files
are meant to be read and changed without touching Go.

**On a first read,** read "The short version", "The map" and "The flow of a
day". Then go on to [4 Function by function](FUNCTIONS.md). Come back to "File
by file" when you are reading the code itself. Any word you don't know is in
the [Glossary](GLOSSARY.md).

---

## The short version

The service wakes up on a schedule. It prices every company it follows. It
reads about forty news feeds and runs about fifteen news searches, plus one
more for each share that moved well beyond the market. It also reads the SEC's
recent filings.

It throws away what is old or duplicated. A model rates every article and
files it under the sectors it bears on. A second pass checks where each one
landed. A large model then writes a brief from what is left. The brief is laid
out as Telegram messages and sent to one chat.

Then the service starts a closer look, which gives buy, hold or sell verdicts.
On Mondays it picks up to ten companies, found in two years of prices for the
whole market. They sit under two kinds of theme: the ones the market has been
paying for, and industries growing before their shares have caught up. Every
day it also picks up to three shares that moved far more than usual on the
news. When the closer look is done, the brief and the closer look go to a
channel for other readers, as one post.

In the background, it keeps those two years of daily prices on its disk.
Between briefs it answers commands in the chat. The biggest one reads a
company's SEC filings, results and analysts' expectations, and writes up the
case for and against it.

Every call to a model goes through the **relay**. The relay writes the call to
a file on disk. A Claude Code process, running without a screen, answers it,
and the answer is written beside it.

---

## The map

| Package | What it owns |
|---|---|
| [cmd/market-watch](../cmd/market-watch/) | The program itself: flags, start-up and shutdown |
| [internal/app](../internal/app/) | The service. It connects everything and decides the order things happen in |
| [config](../config/) | What you edit without Go (the sectors, the companies, the feeds, the prompts, the analysis method), plus the Go that reads them, the environment, and what is kept on the data volume |
| [internal/model](../internal/model/) | The plain data everything else passes around. It depends on nothing |
| [internal/feed](../internal/feed/) | Fetching and reading RSS, removing duplicates, matching articles to sectors by company, and ranking |
| [internal/sec](../internal/sec/) | EDGAR: recent filings as articles, and the annual report and latest results release for `/analyse` and the closer look |
| [internal/search](../internal/search/) | Tavily: news found by searching, one search for each sector and one for each share that moved, as articles |
| [internal/triage](../internal/triage/) | Opus rating and filing every article, then reviewing where each one landed |
| [internal/report](../internal/report/) | Building the brief's prompt, and reading the brief back out of the reply |
| [internal/discover](../internal/discover/) | "New names in the news", and checking every ticker against an exchange |
| [internal/ideas](../internal/ideas/) | "Worth a closer look": the week's themes and their research, the valuation checks, the verdicts, and the scorecard that grades them |
| [internal/market](../internal/market/) | Two years of the whole US market's daily prices on disk, and what they show: the leaders, the popular and early industries, and the day's outsized moves |
| [internal/consensus](../internal/consensus/) | What analysts expect of a company, and what its insiders, short sellers and funds have done, from the data behind Nasdaq's website |
| [internal/fundamentals](../internal/fundamentals/) | Reading XBRL accounts out of EDGAR and turning them into a table |
| [internal/prices](../internal/prices/) | Market data: a live price feed, a daily price history, the whole US market's day and its splits from Massive, company news, and FRED's yields, rates, inflation and commodities |
| [internal/telegram](../internal/telegram/) | The Telegram client, and all the layout of messages and summaries |
| [internal/pages](../internal/pages/) | The web pages that long messages are sent as. They hold tables, charts and cards laid out from the same data as the messages. They are kept for a month and served at a random address |
| [internal/relay](../internal/relay/) | Every model call. It writes the request, runs Claude Code and keeps the reply |
| [internal/mcp](../internal/mcp/) | A small MCP server, so Claude Code can call tools of the service's own. `/analyse` uses it |
| [internal/runcache](../internal/runcache/) | What the latest brief, analysis and closer look were made from and sent, one folder each |
| [internal/history](../internal/history/) | What earlier briefs covered, and what each run cost |
| [internal/logging](../internal/logging/) | A log handler that scrubs secrets out of every line |

Dependencies point inward. `model` imports nothing of ours. `app` imports
nearly everything. Nothing imports `app` except `main`.

---

## The flow of a day

This is the main event. `Serve` runs two goroutines, the scheduler and the bot
poller. This section is what the scheduler sets off.

### 1. Waking up

[`RunScheduler`](../internal/app/app.go#L820) works out the next run afresh
each time, rather than ticking at a fixed interval. That keeps the schedule
pinned to 07:30 US Eastern, two hours before the open, even across a change to
or from daylight saving. When the timer fires, it calls
[`Publish`](../internal/app/app.go#L409) → [`brief(ctx, share: true)`](../internal/app/app.go#L425).

`brief` does three things before any work starts:

- It takes `a.running`, a lock. So a `/now` that arrives during a brief waits,
  instead of starting a second run.
- It calls [`Relay.Begin`](../internal/relay/relay.go#L98). That creates a
  folder for this run and puts it on the context. Every model call the run
  makes lands in that folder, numbered in order.
- It calls [`sendReport`](../internal/app/app.go#L512), which is the pipeline.

### 2. Gathering

[`collectPrices`](../internal/app/prices.go#L46) starts first, in the
background. It reads the twelve benchmark funds and every watchlist share
from Finnhub, through [`prices.Client.Fetch`](../internal/prices/prices.go#L71),
at the free tier's pace of about one a second. For 95 shares that takes about
two minutes. That is why it runs alongside the filings and searches rather than
after them. It prices every share, not only those in the news, so a share that
moved with no story behind it is still seen. After three failed requests it
gives up on Finnhub. Whatever Finnhub didn't price is then read from the daily
charts instead, four at a time, by [`fromCharts`](../internal/app/prices.go#L71).
On 24 September 2026 Finnhub priced only three shares of 107 before the time
allowed for the scan ran out.

[`collectFilings`](../internal/app/filings.go#L25) runs next, so that filings
are treated like any other news rather than added at the end. It calls
[`sec.Client.Collect`](../internal/sec/sec.go#L107). That looks each watchlist
ticker up in EDGAR's ticker index and reads its recent filings. It keeps only
the 8-K item codes that matter ([`materialCodes`](../internal/sec/sec.go#L270)),
and turns each filing into a `model.Article` with
[`Filing.article`](../internal/sec/sec.go#L173).

[`collectSearch`](../internal/app/search.go#L26) runs next, for the same
reason. [`search.Queries`](../internal/search/queries.go#L56) builds the
searches. There are three general ones (markets, the economy, Asia), then one
for each sector, worded from the first sentence of its description.
[`search.Client.Collect`](../internal/search/search.go#L93) runs them. Each one
is a Tavily news search, limited to the publications in
[`search.Outlets`](../internal/search/outlets.go#L44). It reaches back to the
previous brief ([`searchSince`](../internal/app/search.go#L56)), so Monday's
covers the weekend. Every result becomes an article whose source id is `web:`
plus the outlet's domain, such as `web:reuters.com`. It is ranked with that
outlet's weight. Search is an extra, like the filings. Without a key, or on a
day Tavily is down, the brief comes from the feeds alone.

When the prices are in, [`movers`](../internal/app/movers.go#L35) picks the
watchlist shares that moved at least three percentage points more than the
S&P 500 fund, up or down. It picks at most five, the biggest moves first, and
none from a session the previous brief already reported. Because the moves are
measured against the market, a sell-off doesn't set off a search for every
share that fell with it. [`searchMovers`](../internal/app/movers.go#L73) then
asks why each one moved, for example "Why did McDonald's (MCD) shares fall
today?". The question comes from
[`search.MoverQuery`](../internal/search/queries.go#L135). It uses the company's
name as [config/companies.yaml](../config/companies.yaml) gives it, or as the
SEC files it where that file has none.
[`search.Merge`](../internal/search/search.go#L273) adds the results to the
other searches'.

Alongside the prices, [`marketMoves`](../internal/app/movers.go#L132) looks
past the watchlist. It reads the whole market's history and picks the last
session's five biggest moves against each share's usual, among companies worth
US$2bn or more, followed or not. These are the moves the closer look's
reactions start from (`market.Moves`). Each moved at least three times its
usual daily move, on at least twice its usual trading. A session the previous
brief already reported gives none.

Then [`feed.Collect`](../internal/feed/collect.go#L115), the heart of the
gathering, runs these steps:

1. [`Fetcher.Fetch`](../internal/feed/fetch.go#L62) fetches every feed that is
   switched on, all at once. Each response goes through
   [`feed.Parse`](../internal/feed/parse.go#L37). It handles RSS and Atom, bad
   character sets, HTML in summaries, and a dozen date formats. A feed that
   fails becomes a `SourceError` and doesn't stop the run.
2. The filings and search results are added.
3. [`DropStale`](../internal/feed/collect.go#L228) removes anything older than
   a week.
4. [`Dedupe`](../internal/feed/collect.go#L251) removes stories that appear
   twice. It first compares the cleaned-up web addresses, then compares titles
   using the word overlap in [`similar.go`](../internal/feed/similar.go). It
   keeps whichever copy came from the source with the higher weight. The kept
   copy remembers the sources of the copies folded into it (`Article.Also`).
   That is how the run record can tell a story only search found from one the
   feeds had too.
5. [`Match`](../internal/feed/collect.go#L404) tags each article with the
   sectors whose companies it names, by ticker or by name, as
   [config/companies.yaml](../config/companies.yaml) lists them. There are no
   keywords. A story that names no followed company is placed by judgment, or
   not at all.
6. [`Result.triage`](../internal/feed/collect.go#L170) hands everything to
   [`triage.Triager.Triage`](../internal/triage/triage.go#L107). That sends the
   articles to Opus in batches of 60, two batches at a time. For each article
   it gets back a rating from 1 to 5 and up to two sectors, judged against the
   descriptions in [config/sectors.yaml](../config/sectors.yaml). Articles rated
   1 that no sector claims are dropped here. If the sorting fails, the failure
   is logged and the run carries on with name matches alone.

   An article stays in the feeds for days, so most of a day's articles were
   sorted the evening before. The sorting remembers each verdict for eight
   days ([memory.go](../internal/triage/memory.go), `sorted.json`) and gives
   it again, so only articles it hasn't seen go to Opus. A verdict counts only
   under the prompt it was given under. That prompt holds the sectors'
   descriptions and companies, so changing either sorts everything again. The
   log line `triaged` says how many came from memory (`from_memory`).
7. [`Limit`](../internal/feed/collect.go#L597) ranks the articles by
   [`score`](../internal/feed/collect.go#L548) (rating, sector match, source
   weight and how recent it is) and cuts the list to `MAX_ARTICLES`.

Then [`history.Store.Mark`](../internal/history/history.go#L63) flags stories
that earlier briefs already carried. That way today's brief can say how a story
has moved on, rather than report it again from scratch. These stories are
marked, not dropped. Quietly removing a running story would leave the reader
with a new development and nothing to connect it to.

Next, [`triage.TopUp`](../internal/triage/topup.go#L27) fills the sections that
are short of news. The sorting places an article only when it rates it 4 or 5.
When it rates one 3, it keeps the sectors it would have chosen in
`Article.Reserve`. A section with fewer than `ThinSection` (10) articles is
filled from those, strongest first. It uses only articles that no section holds
and that no earlier brief carried. On 24 September no crypto story was rated
above 3, and without this step the crypto section went missing. A top-up is
only a proposal. The review has to confirm it, so top-ups only happen when the
review runs.

Then [`triage.Reviewer.Review`](../internal/triage/review.go#L62) takes a
second look. The sorting judged sixty articles at a time, each on its own. The
review sees every article that could reach the brief, with where it now sits.
That means every article rated 3 or more, plus any left unrated by a failed
batch. It moves the ones that belong elsewhere, such as a broker's note on an
oil company that landed in Financials. It is Opus too. It may search the web to
learn what an unfamiliar company does. It changes only where articles sit,
never their ratings. Top-ups are marked in its list. A top-up stays only if the
review names its section. If the review says nothing, says "-", names another
section, or its batch fails, the top-up is withdrawn. So a section stays thin
rather than padded. What the review moved, and which top-ups it kept, go into
the run record. `/stats` shows a few of each, so you can judge it.

### 3. Context, not content

[`collectCalendar`](../internal/app/calendar.go#L38) runs alongside the prices
from the start. It reads what is due. The week's economic releases come from
ForexFactory's weekly export
([`calendar.ForexFactory.Week`](../internal/calendar/calendar.go#L41)), each
with its forecast and previous figure.
[`calendar.Key`](../internal/calendar/calendar.go#L109) keeps the US releases
rated high or medium, and the high ones from the other large economies. The
companies due to report over the next five weekdays come from Nasdaq's earnings
calendar ([`consensus.Client.Earnings`](../internal/consensus/earnings.go#L18)),
followed companies first, each with what analysts expect a share to earn. The
writer gets all this as a "Coming up" block and ends the overview with what to
watch. The reader sees it as a block of its own under the overview. It was
added on 1 October 2026, after a brief mentioned the PCE inflation release and
Micron's results only as "the day's tests".

[`collectLevels`](../internal/app/filings.go#L88) reads FRED through
[`prices.FRED.Fetch`](../internal/prices/fred.go#L147). It gets bond yields,
the yield curve, the Fed's rate, the S&P, the VIX and inflation. Inflation is
the consumer price index and its core, asked for as the change from a year
earlier.

The prices read at the start go to the brief whole. For the movers,
[`trendsFor`](../internal/app/movers.go#L173) also reads each one's price
history from the chart source. That gives its 50- and 200-day averages, its
range over the year, and the day's volume against its usual. With these, the
brief can say what kind of move it was.

Both of these are optional. A failure here costs the reference numbers, never
the brief.

### 4. Writing

[`report.Generator.Generate`](../internal/report/generate.go#L73) does four
things:

- [`splitByCoverage`](../internal/report/prompt.go#L84) decides which
  watchlists have enough news for a section, and which are just quiet.
- [`buildPrompt`](../internal/report/prompt.go#L119) puts the prompt together.
  It starts with the market levels
  ([`renderMarketData`](../internal/report/prompt.go#L348)) and the prices with
  the movers' history ([`renderPrices`](../internal/report/prompt.go#L386)),
  then the moves across the market
  ([`renderMarketMoves`](../internal/report/prompt.go#L363)).
  Then come each active section's articles under its line of biggest moves
  ([`sectionMoves`](../internal/report/moves.go#L30)). Last comes the general
  news the overview may draw on. Every article gets a citation number from
  [`numbering`](../internal/report/prompt.go#L253). The writer is told that the
  reader sees the moves line, so it explains the moves rather than listing
  them.
- The call goes through `Completer`, which is the relay.
- [`parseResponse`](../internal/report/parse.go#L22) splits the reply at the
  `## OVERVIEW` and `## SECTION: <id>` markers into a `model.Report`. Each
  section gets the same biggest moves the prompt showed.

### 5. New names

[`discover.Finder.Find`](../internal/discover/discover.go#L64) asks Opus which
companies the day's stories were about that no watchlist follows. The reply is
a table with its columns split by `|`, and
[`parse`](../internal/discover/discover.go#L135) reads it.

Then comes the part that matters.
[`Finder.verify`](../internal/discover/discover.go#L291) checks every ticker
against OpenFIGI through [`FIGI.Verify`](../internal/discover/verify.go#L60).
It confirms two things: that the symbol exists on the exchange claimed, and
that the registered name is the same company
([`SameCompany`](../internal/discover/verify.go#L153)). If the check fails, it
returns nothing rather than unchecked tickers.

[`discover.Store.Note`](../internal/discover/store.go#L54) counts how many days
a name has been in the news.
[`priceCandidates`](../internal/app/prices.go#L277) adds each one's move on the
day. US names are priced from the live price feed, and everything else from the
chart source.

### 6. Layout and delivery

Since 1 October 2026 the brief has been written in plainer English, with about
twice the room. Each sub-heading and sector heading has an emoji
([config/sectors.yaml](../config/sectors.yaml)). The key figure in a bullet is
marked `**so**`, and [`highlight`](../internal/telegram/render.go#L419) shows
it in bold. [`linkTerms`](../internal/telegram/terms.go#L19) links the first
mention in a section of each term in
[config/glossary.yaml](../config/glossary.yaml) to a page that explains it. It
leaves tags, links and bold text alone.
[`renderCalendar`](../internal/telegram/calendar.go#L25) lays out the look
ahead.

[`telegram.RenderWith`](../internal/telegram/render.go#L95) turns the report
into Telegram's HTML. It lays out the overview, each section under its line of
biggest moves, the new names
([`renderCandidates`](../internal/telegram/render.go#L790)), the quiet
watchlists, the source links, and a footer of token counts. Citations become
links through [`linkCitations`](../internal/telegram/render.go#L747).

The brief is written as sub-headings, each a `### ` line, over one-sentence
bullets. [`paragraphs`](../internal/telegram/render.go#L281) keeps each
sub-heading with its bullets. [`bullets`](../internal/telegram/render.go#L376)
makes the sub-heading bold and turns each `- ` into a bullet, with a blank line
between bullets. The section headings are in capitals so they stand above the
sub-headings. A block written the older way, as a label and a dash, still gets
its label in bold ([`emphasizeLabel`](../internal/telegram/render.go#L320)).

[`pack`](../internal/telegram/render.go#L440) then spreads the pieces across
messages, within Telegram's limit of 4,096 characters. It breaks between
sections rather than in the middle of a thought. It never leaves a heading
alone at the end of a message.

[`send`](../internal/app/pages.go#L39) sends them. Since 1 October 2026, with
`PAGES_URL` set, that is one message: a summary with a "📖 Read the full brief"
button that opens the whole brief as a web page. The summary comes from
[`BriefSummary`](../internal/telegram/summary.go#L56). It holds the overview's
opening line, the writer's `## IN SHORT` bullets, and the next 24 hours'
releases and results, each written as "… @ time".

The page is laid out from the report, not from the messages, by
[`BriefDoc`](../internal/telegram/pagedocs.go#L43). It shows the markets as a
table and a bar chart, from the FRED readings and the benchmark funds
([`pageMarket`](../internal/app/pages.go#L125)). Then it shows the overview,
what is coming up as a table for each day, and each sector with its biggest
moves and its own sources. The wording is the same as the messages'.
[`pages.Render`](../internal/pages/render.go#L35) lets through only Telegram's
tags in it.

[`pages.Store.Publish`](../internal/pages/pages.go#L52) keeps the page on the
volume for 30 days, under a random 128-bit address. The service serves it
itself ([`pages.Store.Handler`](../internal/pages/pages.go#L109)). It answers
`GET /r/<id>` and nothing else. The pages have no scripts and ask search
engines not to index them. If the page or the summary fails, the messages go
out in full, as they always used to. The closer look, `/analyse` and
`/industry` go out the same way, each with its own summary. Message ids are
saved in prefs, so the next run can delete them if `REPLACE_PREVIOUS` is on.

Afterwards, [`history.Store.Record`](../internal/history/history.go#L89) marks
what this brief covered. It does this only after delivery, because a brief that
never arrived hasn't covered anything. Then
[`history.Runs.Add`](../internal/history/runs.go#L121) records what the run
cost and did, for `/stats`. When search is on,
[`recordSearch`](../internal/app/search.go#L80) adds what search contributed.
It counts how many kept stories no feed carried, and how many of the brief's
citations came from search alone. It also lists, by source, the cited stories
that no search found. The citations are read back out of the text by
[`Report.Referenced`](../internal/model/report.go#L114). These numbers are what
will decide whether search can take over from the media feeds.

### 7. The channel

Once the closer look is done, [`shareBrief`](../internal/app/channel.go#L127)
posts the brief and the closer look to the channel as one post. The post holds
both summaries, one after the other, with a button under them for each one's
page ([`sendTogether`](../internal/app/pages.go)). The owner asked for one post
instead of two (2026-10-01). On a day with no closer look, the brief goes
alone. With pages off, or when the two summaries are too long for one message,
each goes on its own as before. If a `/share` posted the brief while the closer
look was still running, the closer look then goes alone
([`shareIdeas`](../internal/app/channel.go)).

The channel only goes one way. Its readers can't reach `HandleMessage`. The bot
takes commands only from the owner's chat, and from the owner's other chats
listed in `TELEGRAM_COMMAND_CHATS` and `TELEGRAM_CONTROL_CHATS`.
[`needs`](../internal/app/commands.go#L200) says which command each kind of chat
may use.

### 8. Worth a closer look

[`brief`](../internal/app/app.go#L425) starts the closer look as soon as the
owner has the brief. When it is done, it posts both to the channel. Until
2026-10-01 the closer look waited twenty minutes after the brief, queued on the
data volume. In those days the channel got the brief at once and the closer
look later, as a post of its own.

[`sendIdeas`](../internal/app/ideas.go) brings the market's history up to date
([`topUpMarket`](../internal/app/marketdata.go)). It reads Nasdaq's list of
every US listing ([`listings`](../internal/app/marketdata.go), kept for a day).
Then it does the following.

1. **The themes, on the scheduled run's first look of the week**, within 75
   minutes ([`runThemes`](../internal/app/themes.go)).
   - [`market.Measure`](../internal/market/screen.go) reads every listing's
     returns, averages, swing and trading from the two years held in
     [`market.Store`](../internal/market/store.go).
     [`singaporeStocks`](../internal/app/marketdata.go) adds the Straits Times
     Index's thirty companies from the chart source, in dollars.
   - [`market.Leaders`](../internal/market/screen.go) keeps the 150 best by
     their average rank on four measures against the S&P 500. It only
     considers companies that are eligible and above their 200-day average,
     whose rise didn't come in one day, and that aren't pinned to a takeover
     offer.
   - [`market.Industries`](../internal/market/screen.go) scores every industry
     as popular and as early, using how often the headlines name them, from
     [`market.Mentions`](../internal/market/names.go).
   - [`ideas.Sorter.Sort`](../internal/ideas/themes.go) (Opus) sorts the
     leaders into up to three themes.
     [`ideas.Scout.Find`](../internal/ideas/themes.go) (Opus **with the web**)
     finds up to two industries growing before their shares have caught up.
   - [`ideas.Researcher.Research`](../internal/ideas/themes.go) (Opus **with
     the web**, two themes at a time) finds the part of each theme the market
     hasn't paid for, and up to four companies in it, checked against
     OpenFIGI.
   - Up to sixteen companies are judged across the themes.
     [`value`](../internal/app/themes.go) sets each one's multiples against its
     theme's medians and its own last five year ends
     ([`fundamentals.Snapshot.Multiples`](../internal/fundamentals/multiples.go)).
     The theme's medians come from the accounts of its best members, read for
     the purpose. `value` also counts the warning signs, and says where a BUY is
     ruled out ([`ideas.Valuation.BuyClosed`](../internal/ideas/valuation.go)).
   - Up to ten BUYs and SELLs are shown, the most confident first when there
     are more. The earlier picks of the last eight weeks are listed with how
     each has done ([`earlierPicks`](../internal/app/themes.go)).
   - The week is saved to [`ideas.ThemeLog`](../internal/ideas/themelog.go).
     So it runs only once, and next week's research is given this week's.
2. **The reactions, every day** ([`runReactions`](../internal/app/reactions.go)).
   [`market.Moves`](../internal/market/screen.go) finds the shares that moved
   at least three times their usual daily move, on at least twice their usual
   trading. [`fundamentals.Relevant`](../internal/fundamentals/news.go) keeps
   those that the brief's articles explain, with results first. Up to six are
   judged, and up to three BUYs or SELLs are shown.
3. **The facts for each company.** [`ideaFacts`](../internal/app/ideas.go),
   four companies at a time, gathers what a verdict should rest on. For a US
   SEC filer, that is what `/analyse` reads: five years of accounts, the
   business description and recent filings, what analysts expect, the results
   release at the same length, and the last fortnight's news. The news is the
   feed's stories plus one news search
   ([`addIdeaNews`](../internal/app/ideas.go#L378)). For anything else, it is
   the price and trading history and the same news, with a note that the
   accounts are missing.
4. **The verdicts.** [`ideas.Judge.Judge`](../internal/ideas/judge.go) uses
   Opus with web search, three companies a call, two calls at a time, with the
   market backdrop shown above them. It checks the claim each case rests on in
   two independent sources, and names them in a CHECKED line. Each company gets
   a BUY, HOLD or SELL, with its case, what is coming, the lever it depends on,
   two to four numbers and the biggest risk. A theme pick also gets its price
   against its theme and its own history. A reaction gets what changed, how the
   share moved, and whether the move was justified. `hold` then applies the two
   rules that belong to the code. A BUY that the valuation rules out becomes a
   HOLD. A verdict without accounts, or whose case rests on "one source only",
   gets low confidence at most. HOLDs are not shown (`Idea.Shown`).
5. **The layout.** [`telegram.RenderPicks`](../internal/telegram/ideas.go) lays
   them out. First come the themes, each with its numbers, what the research
   found, and a popular or early label. Then the reactions, then the earlier
   picks. They go to the owner, and also to the channel under its warning note
   when the brief went there. Each part of a verdict is a paragraph of its own.
   The numbers are listed one to a line. A coloured mark comes before each
   company, and a short rule separates one company from the next. A day with
   nothing to show sends nothing.
6. **The scorecard.** [`recordVerdicts`](../internal/app/ideas.go) writes each
   verdict shown to the scorecard, with its source and theme, and the dollar's
   exchange rate where the share trades abroad. `/scorecard` grades it against
   the S&P 500 in US dollars once it is a week old. It measures both from the
   first price after the verdict, which is the next session's open. It writes
   that entry price down once the session has happened.

Alongside the scheduler, the bot and the closer looks,
[`RunMarket`](../internal/app/marketdata.go) keeps the store full. It fetches
ten days at a time, newest first, at Massive's limit of five requests a minute.
So the first fill takes two hours, and a new session is fetched within minutes
of Massive making it available.

---

## The other journeys

### Start-up

[`main`](../cmd/market-watch/main.go#L33) reads five flags: `--once`,
`--share`, `--check`, `--clear` and `--fold`, and `--mcp-accounts`, which
only Claude Code uses (below). It then calls
[`run`](../cmd/market-watch/main.go#L73), which does the following.

- [`config.Load`](../config/config.go#L206) reads the environment, and `.env`
  through [`LoadDotEnv`](../config/dotenv.go#L22). It reports every missing
  variable at once, rather than one per run.
- [`config.LoadPrompts`](../config/prompts.go#L61) checks that the prompts file
  still has every marker the replies are read by. A missing section stops the
  program here, rather than four hours later when a reply can't be read.
- It wraps the log handler in [`logging.New`](../internal/logging/scrub.go#L35),
  so every line passes through the scrubber. This is needed because `net/http`
  puts the request's address into connection errors. The bot token is in that
  address, and so are the Finnhub and FRED keys in theirs. The scrubber removes
  every credential the settings hold (`config.Secrets`). Error text sent to the
  chat is scrubbed of the same list.
- [`app.New`](../internal/app/app.go#L190) builds the service. It loads the
  lists from `config/`, and the changes made to them from Telegram from the
  data volume.
- It installs a SIGTERM handler, so a brief that is being sent finishes its
  delivery.

`--check` runs [`runCheck`](../cmd/market-watch/main.go#L229). It checks
Telegram, the channel, the feeds, the search key, the schedule and Claude Code,
and reports on each separately. It proves the search key with
[`search.Client.Usage`](../internal/search/search.go#L302), which costs
nothing, whereas a test search would spend a credit. Tavily's count of credits
used runs late, so the run record keeps its own count, from each search's
reply. The deploy script runs `--check` on the machine after each deploy.

`--fold` runs [`runFold`](../cmd/market-watch/main.go#L151) and nothing else.
[scripts/sync-from-fly.sh](../scripts/sync-from-fly.sh) has just copied the
Telegram changes to the watchlist and feeds into `./data`. `--fold` writes them
into the files in `config/`. It needs no credentials and sends nothing.

`--mcp-accounts <CIK>` runs [`runAccountTools`](../cmd/market-watch/main.go#L346)
and nothing else. Claude Code starts the program this way for an analysis
call, and talks to it over standard input and output
([`mcp.Serve`](../internal/mcp/mcp.go#L55)). It serves that one company's
account tools until Claude Code closes its input. `--tools-log` names a file
for it to write each call to.

### The bot loop

[`Client.Poll`](../internal/telegram/updates.go#L77) keeps asking Telegram for
new messages (`getUpdates`). It hands each one to
[`HandleMessage`](../internal/app/commands.go#L70), which checks that the
sender is allowed and then routes on the command:

| Command | Handler | What it does |
|---|---|---|
| `/start` | [`handleStart`](../internal/app/commands.go#L227) | Registers the chat as the owner's, once |
| `/now` | [`handleNow`](../internal/app/commands.go#L247) | A brief for the owner only, which waits for `/share`. Runs in the background, alone |
| `/share` | [`handleShare`](../internal/app/channel.go#L196) | Posts whatever arrived last to the channel |
| `/analyse` | [`handleAnalyse`](../internal/app/commands.go#L589) | Writes up a company (see below) |
| `/industry` | [`handleIndustry`](../internal/app/industry.go#L21) | How an industry fits together, where it is heading, and companies to look into (see below) |
| `/scorecard` | [`handleScorecard`](../internal/app/ideas.go#L469) | How the verdicts have done against the index |
| `/stats` | [`handleStats`](../internal/app/commands.go#L845) | What recent runs found and did |
| `/usage` | [`handleUsage`](../internal/app/usage.go#L51) | What is left of the Claude plan, window by window, and of the month's search credits |
| `/watchlist` | [`handleWatchlist`](../internal/app/commands.go#L330) | Follow a company or stop following it; list or drop the changes made here |
| `/sources` | [`handleSources`](../internal/app/commands.go#L413) | Turn a feed on or off |
| `/schedule` | [`handleSchedule`](../internal/app/commands.go#L319) | When the next brief is due |
| `/clear` | [`handleClear`](../internal/app/commands.go#L276) | Delete the bot's earlier messages |

Most commands answer at once, so they are handled in turn. `/analyse` and
`/industry` take minutes, so once they know what to look at they run in the
background ([`background`](../internal/app/jobs.go)). Up to three run side by
side, and the bot keeps reading new messages meanwhile. Each one has its own
relay run and its own cache folder. Each model call is a new Claude Code
process that keeps no session, so one job can't see what another asked.

The limit of three is for the server's memory. Each job is a Claude Code
process, the machine has 1 GB, and the brief has always run three at once.
Jobs start in the order they were asked for. Before a job starts, `admit`
reads the plan. Once any window is 85% used, the chat is warned once, and the
jobs run one at a time. A job that has to wait tells the chat why. When a
chat's jobs are all done, it gets one short message saying how much of each
window is used.

A brief runs alone. It waits for the jobs already running to finish, and no
job starts until it is done (`hold` and `release`). `/now` runs in the
background too, so the bot keeps answering while a brief is written. A
report that falls back to several messages is sent under a lock, so two
reports finishing together don't interleave.

### `/analyse`

`/analyse` on its own asks which company. The chat's next message is taken as
the ticker ([`ask`](../internal/app/ask.go), `answering`). The question opens a
reply box, which also lets the answer through in a group chat. It waits ten
minutes, and any command sent instead drops it. A dropped question is deleted.
Otherwise Telegram would reopen the reply box every time the chat is opened,
until the question was answered. An answer that doesn't look like a ticker gets
the question again. `/analyse NVDA` still works in one line.

[`handleAnalyse`](../internal/app/commands.go#L589) runs within its own time
limit, so it doesn't inherit whatever time the caller had left. It does the
following.

1. [`fundamentals.Client.Fetch`](../internal/fundamentals/metrics.go#L256)
   looks the ticker up in EDGAR. It reads five years of XBRL figures through
   [`xbrl.Client.Concept`](../internal/fundamentals/xbrl.go#L135) and builds a
   `Snapshot`. It handles both US GAAP and IFRS tag names
   ([`metrics.go`](../internal/fundamentals/metrics.go#L59)), and picks the
   currency the filer reports in. It prefers later filings over earlier ones
   that were restated ([`supersedes`](../internal/fundamentals/metrics.go#L520)).
   It builds the current year so far beside the full years
   ([`buildYTD`](../internal/fundamentals/metrics.go#L628)), using only
   interim periods that end after the latest annual report.
2. [`quoteFor`](../internal/app/commands.go#L855) adds the share price, so the
   filed figures can become multiples.
3. Three optional reads come next.
   - [`AddBusiness`](../internal/fundamentals/business.go#L39) takes the
     business description from the latest annual report.
   - [`tradingFor`](../internal/app/prices.go#L146) reads the daily price
     history, and [`prices.Summarise`](../internal/prices/history.go#L281)
     turns it into returns, moving averages, the year's range, VWAP and
     volatility. It also reads the S&P 500's moves over the same stretches, so
     the analysis can say whether the share led the market or lagged it.
   - [`addNews`](../internal/app/prices.go#L232) adds what has been written in
     the last month. That is the news feed's company headlines and two Tavily
     searches ([`searchCompany`](../internal/app/research.go#L214)), which cost
     two credits. [`Relevant`](../internal/fundamentals/news.go#L92) keeps only
     the pieces that actually name the company. Of the twelve places, the
     searches get first call on eight and the feed gets four
     ([`SetNews`](../internal/fundamentals/news.go#L74)). Sorted by date, the
     feed's daily share-price items used to take all twelve.
4. Three more optional reads.
   [`addExpectations`](../internal/app/research.go#L115) adds what analysts
   expect, and what insiders, short sellers and funds have done
   ([`consensus.Client.Fetch`](../internal/consensus/consensus.go#L156)).
   [`addRelease`](../internal/app/research.go#L143) adds the company's latest
   results release ([`sec.Client.EarningsRelease`](../internal/sec/release.go#L40)).
   [`addReleaseFigures`](../internal/app/research.go#L162) adds the release's
   own figures, when the filings don't reach its quarter yet. A release comes
   weeks before the 10-Q or 10-K with the same figures. Until 2026-10-04 the
   tables, the twelve months, the multiples and the balance sheet all stopped
   at the last filing in those weeks. A quick model call (stage `release`)
   copies the GAAP figures out of the release's tables
   ([`ReleaseReader`](../internal/fundamentals/release.go#L77)).
   [`Snapshot.AddRelease`](../internal/fundamentals/release.go#L112) then
   checks the release's columns for periods already filed against the
   filings. Revenue and net income must match in at least one, and no
   revenue, profit or earnings a share may differ. Only then does it add the
   new periods, marked `FromRelease`, and build the years, quarters and
   twelve months again. The balance sheet has its own test
   ([`releaseBalance`](../internal/fundamentals/release.go#L240)): its assets,
   equity and cash must match. A line that doesn't match keeps its filed
   figure, shown with its own date. The model's tables and the page mark the
   release's columns with a *.
   [`backdrop`](../internal/app/research.go#L193) adds commodities, the dollar
   and the cost of money, from FRED.
   [`addPeers`](../internal/app/research.go#L64) sets the company beside the
   others in its industry group of Nasdaq's list, worth US$500m or more
   ([`Client.Peers`](../internal/fundamentals/peers.go#L177)). The SEC's frames
   API gives one figure for every filer for one calendar year or quarter in a
   single request. Each company is put on its latest twelve months filed: its
   last full year, plus the quarters filed since, less the same quarters a
   year before. A fourth quarter is rarely filed on its own, so four quarters
   are summed only where the year cannot be bridged. The company itself is on
   the twelve months the page's box uses, with the box's P/E and price to
   sales, so the two agree
   ([`Snapshot.PeerFigures`](../internal/fundamentals/peers.go#L121)). Cash
   flows are filed year to date, so the free cash flow margin stays on the
   calendar year. Each frame is kept under `frames/` on the data volume, a
   week, or 90 days once its period ended over a year ago. The comparison
   covers growth, margins, research spending, the P/E and price to sales.
   Each line gives the company's figure, the group's median and 25th to 75th
   percentile, and how many it beats. Only US-GAAP figures in dollars are compared.
   The verdicts get the same comparison, and the analysis page shows it as a
   table.
5. [`Snapshot.Table`](../internal/fundamentals/table.go#L46) lays the figures
   out as a table with fixed-width columns.
   [`Analyzer.Analyze`](../internal/fundamentals/analyze.go#L65) sends it to
   Opus, with [`method.md`](../config/method.md), the house method for reading
   accounts, added to the system prompt. The method includes CAN SLIM's
   questions as a reference, with where each one misleads. Since 2026-10-02
   the analysis centres on the case for and against the company. It also
   covers where the company is heading and whether its figures hold up.
6. [`SplitRelated`](../internal/fundamentals/related.go#L54) cuts the
   "companies to read next to it" table out of the text.
   [`VerifyRelated`](../internal/fundamentals/related.go#L90) checks those
   tickers against OpenFIGI before any of them is shown.
   [`SplitVerdict`](../internal/fundamentals/verdict.go) takes out THE VERDICT.
   That is the last section the analysis writes: BUY, HOLD or SELL against the
   S&P 500 over twelve months, the same call the closer look makes, with a
   confidence and a short reason. It is kept short and shown last.
   [`SplitShort`](../internal/fundamentals/verdict.go) takes out IN SHORT,
   the analysis's own summary in plain words. It says what the company does,
   who buys it, what is coming, the figures that matter most, and one point
   for and one against, in the analysis's own order. It is the
   chat's summary, and the full analysis leaves it out.
   [`SplitSources`](../internal/fundamentals/sources.go) takes out SOURCES,
   the web pages the analysis drew on, and any address written into the text
   anyway. They are shown as numbered footnotes at the end, in the chat and
   on the page. The text cites them by number, [3], as the brief does. The
   numbers are redone to follow the footnotes, and each links to its page.
   [`PlainHeadings`](../internal/fundamentals/verdict.go) first takes the bold
   marks off a section heading written as "**THE BUSINESS**".
   [`SplitTerms`](../internal/fundamentals/verdict.go) takes out TERMS, the
   words the analysis used that a reader might not know. The analysis does
   not define them. [`terms.Filter`](../internal/terms/terms.go) drops the
   names and everyday words among them, and
   [`terms.Linked`](../internal/terms/terms.go) links each to its glossary
   page, or to a Google search for its meaning and field where the glossary
   has none. The brief lists its terms the same way, under `## TERMS`. A term
   two reports have listed is checked by the `terms` stage and, if it passes,
   learned: kept in `terms.json` on the volume, linked wherever it appears,
   and folded into the glossary by `--fold`.
7. [`RenderAnalysis`](../internal/telegram/render.go) and
   [`RenderRelated`](../internal/telegram/related.go#L23) lay it out.
   `RenderAnalysis` rules off each section with a heading in capitals, makes
   its sub-headings bold, and shows the verdict last. It is laid out twice,
   once for the owner and once for the channel. The channel's copy carries the
   warning under the verdict, and is what `/share` posts.
   [`recordAnalysis`](../internal/app/commands.go) writes the verdict to the
   scorecard, marked as coming from an analysis. The same verdict on the same
   share within a day counts once.

Since 1 October 2026 the accounts have also carried the latest five quarters,
each three months on its own, and the twelve months the last four make
([`buildQuarters`](../internal/fundamentals/quarters.go#L44)). A quarter's
income is taken as filed. Its cash flow is the difference between two running
totals. A fourth quarter is the year minus its first nine months. Its
earnings a share are its profit over its own diluted shares, which are the
year's average less the first three quarters'. The table
shows the quarters in a block of their own
([`quarterTable`](../internal/fundamentals/quarters.go#L196)), and the analysis
starts with them. The analysis may search the web, for the last fortnight's
news and for a foreign filer's own latest results.

### `/industry`

`/industry` on its own asks which industry, the same way `/analyse` asks which
company. The next message is the topic, however many words it has.
`/industry robotics` still works in one line.

[`handleIndustry`](../internal/app/industry.go#L21) asks
[`industry.Explainer.Explain`](../internal/industry/industry.go#L65), which is
Opus with web search, under `industry.system`. It explains how the industry
fits together, part by part. It also says what people are saying about the
industry, what is coming, and what follows from it. It lists three to six
listed companies to look into for each part, spread across countries, each
shown with its flag and country.
It cites the pages it drew on by number, [3], as `/analyse` does, and lists
them under `SOURCES`. [`SplitSources`](../internal/fundamentals/sources.go)
takes that list out first and renumbers the citations to match.
[`industry.Split`](../internal/industry/industry.go#L94) cuts the
`COMPANIES BY PART` table out.
[`industry.Verify`](../internal/industry/industry.go#L121) checks every ticker
against OpenFIGI through `VerifyRelated`.
[`RenderIndustry`](../internal/telegram/industry.go#L33) lays it out in the
brief's style, with the companies grouped by part and the sources as
numbered footnotes at the end, which the citations link to. The page does the
same. `/share` posts the channel's copy.

### Every model call

There is one path, and this is it.
[`Relay.Begin`](../internal/relay/relay.go#L98) opens a run folder and puts it
on the context. Then [`Run.Ask`](../internal/relay/relay.go#L229):

1. writes `NN-stage-request.txt`, which holds the system prompt and the prompt;
2. notes it in the run's ledger, a checklist in markdown;
3. hands it to an `Answerer`;
4. writes `NN-stage-reply.txt` beside it, and ticks the ledger line.

The answerer is normally [`Claude`](../internal/relay/answer.go#L85). It runs
`claude -p` as a fresh process for each call, with `--no-session-persistence`.
The system prompt goes in a temporary file, because Windows limits a command
line to 32K characters. The working folder is the run's own, so the call sees
no `CLAUDE.md` and no project settings. `ANTHROPIC_API_KEY` is removed from the
child process's environment ([`childEnv`](../internal/relay/answer.go#L395)),
so the subscription is used rather than API credit.

Its output is Claude Code's stream (`--output-format stream-json`), which
[`parseStream`](../internal/relay/answer.go#L238) reads. It gets the result,
and the plan's standing (`rate_limit_event`: how much of each window is used,
and when it resets). `OnLimits` passes that to `/usage`. When the last reading
is more than five minutes old, `/usage` takes a new one with
[`CheckLimits`](../internal/relay/answer.go#L304), a one-word call to Haiku.

Tools are off for every stage except six: `scout`, `research`, `review`,
`verdicts`, `analysis` and `industry`. Those get web search and web fetch and
nothing else: no shell, no files, and no MCP server of this machine's. A
headline in a feed should never be able to steer a model into running a
command.

The one exception is the analysis, which also gets three tools of the
service's own (`ANALYSIS_TOOLS`, on by default). Until 22 September an agent
of ours could look up any figure a company files and calculate exactly. It
called the API directly, so it went when the API did. Now the same tools are
served to Claude Code as an MCP server.
[`analysisTools`](../internal/app/tools.go#L20) puts a
[`relay.Tools`](../internal/relay/tools.go#L14) on the analysis call's
context. `Claude.Answer` writes it to a config file for `--mcp-config`, and
approves the tools by name. The server is this program, started with
`--mcp-accounts` and the company's CIK. Its tools
([`fundamentals.AccountTools`](../internal/fundamentals/tools.go#L46)) are:
- `find_concepts`, which searches the tags the company reports;
- `read_concept`, which reads one tag's years, quarters and balance dates;
- `compute`, the calculator.

The lookups are limited to 16 a call, in the server, and calculations to 50.
Each call is written to a `-tools.txt` file beside the request and reply,
copied into the run cache, and counted in the ledger. A stage without tools
never gets the server, whatever its context carries.

The other answerer, [`Session`](../internal/relay/answer.go#L429), waits for a
person to write the reply file. That is how you watch a run or answer it by
hand.

---

## File by file

### cmd/market-watch

**[main.go](../cmd/market-watch/main.go)** is the program. `main` reads the
flags. `run` loads the settings, checks the prompts, builds the logger and the
app, and acts on the flags. `runClear` deletes the bot's earlier messages
without starting the service. `runCheck` reports on each part in turn.
`runFold` writes the Telegram changes into `config/`. `stageModels` lists which
model answers which stage.

### internal/app

**[app.go](../internal/app/app.go)** holds the service and the brief pipeline.
`App` holds every collaborator. Most of them can be nil, so a missing key turns
off one feature rather than the whole run. `New` builds it. `Prefs` and
`UpdatePrefs` guard the preferences behind a lock. `SendReport`, `Publish`,
`publishScheduled` and `brief` are the ways into a run: the brief, then its
closer look, then both to the channel. `sendReport` is the pipeline itself.
`RunScheduler` fires the daily brief. `Serve` runs it alongside the market sync
and the bot poller, and returns when any of them fails. `reportFailure` tells
the owner when a brief has failed. The helpers are `sourceMode`,
`watchedNames`, `companyNames`, `newRelay`, `placedExamples`, `movedExamples`,
`clipRunes`, `namesRemembered` and `now`.

**[commands.go](../internal/app/commands.go)** holds the bot's commands.
`BotCommands` publishes the menu. `HandleMessage` checks the sender and routes
the message, and `splitCommand` reads it. There is one handler for each
command, as in the table above. `companyFrom` reads "PLTR Palantir" or "SK
Hynix" as a company. It takes a first word shaped like a ticker as the symbol
(`isTicker`). `renderWatchlists`, `renderEdits`, `renderSources`, `groupIDs`
and `escape` lay out the replies. `quoteFor` fetches one price for `/analyse`.

**[prices.go](../internal/app/prices.go)** does everything to do with prices.
`collectPrices` reads the benchmarks and every watchlist share. `fromCharts`
reads from the chart source whatever the live price feed missed. `tradingFor`
reads one listing's history for the analysis, with the S&P 500's beside it.
`marketFor` returns both the history and the last price from one fetch, which
is what gives a non-US listing a price at all. `seriesFor` is the fetch.
`summarise` turns a history into figures, and refuses one that is out of date.
`addNews` adds company news, from the feed and from `searchCompany`.
`priceCandidates` prices the new names: US names from the live price feed, and
everything else from the chart source. The time limits live here: `scanBudget`
150s, `chartBudget` 45s, `quoteBudget` 90s, `historyBudget` 25s and
`newsBudget` 25s.

**[movers.go](../internal/app/movers.go)** handles the shares that moved on
their own. `movers` picks those that moved at least `moverGap` (three
percentage points) more than the S&P 500 fund, at most five of them.
`searchMovers` searches for why. It names each company as
`config/companies.yaml` does, or by `companyName` from the SEC's index where
that file has no name. `trendsFor` reads their price histories side by side.

**[ideas.go](../internal/app/ideas.go)** is "worth a closer look". `look` is
what it starts from, and `lookFrom` makes one from a brief. `sendIdeas` runs
the whole sequence and delivers it. `following` is what the watchlists follow,
which the closer look leaves out. `factsFor` runs `ideaFacts` four at a time,
into a `sheet` for each company. `judge` asks for the verdicts. `best` cuts the
list to a limit, the most confident first. `recordVerdicts` writes the
verdicts to the scorecard. `pathFor` reads a chart's sessions for scoring.
`handleScorecard` answers `/scorecard`.

**[themes.go](../internal/app/themes.go)** holds the week's themes. It has
`runThemes`, `loadPanel` (two years, for companies worth at least US$1bn), and
`researchThemes`, two at a time. It has `themeFigures`, plus `value` and
`readAccounts` for the valuation. It also has `pastMultiples`, from five years
of month-end prices, and `earlierPicks`.

**[reactions.go](../internal/app/reactions.go)** has `runReactions`, the day's
outsized moves that the news explains. `resultsWords` puts results first.
`recentMoves` reads the market's last 120 days and finds the latest session's
outsized moves. The brief's line under the overview uses it too.

**[marketdata.go](../internal/app/marketdata.go)** keeps the market data.
`RunMarket` keeps the market's history full in the background. `topUpMarket`
brings it up to date before a closer look. `listings` reads Nasdaq's list at
most once a day. `singaporeStocks` measures the Straits Times Index in dollars.
`panelPath` turns a stored series into a path for the scorecard.

**[ask.go](../internal/app/ask.go)** is for a command that asks for what it
needs. `ask` sends the question and remembers it for the chat. `answering`
takes it back when the next message comes. `forget` drops it when a command
comes instead, and `expire` drops it when ten minutes pass. `withdraw` deletes
a dropped question from the chat. `asTicker` reads a ticker the way people
type it.

**[research.go](../internal/app/research.go)** holds what `/analyse` and the
closer look read beside the accounts: `addExpectations`, `addRelease`,
`addPeers`, `backdrop`, and `searchCompany`, which is `/analyse`'s two
searches.

**[jobs.go](../internal/app/jobs.go)** runs `/analyse` and `/industry` in the
background. `background` puts a job in line and starts it. `admit` waits for
its turn: three at a time, one at a time once the plan is 85% used, and none
while a brief holds them back. `hold` and `release` are the brief's side.
`detach` runs `/now` in the background. `jobDone` sends the plan's standing
once a chat's jobs are all done.

**[usage.go](../internal/app/usage.go)** is `/usage`. `planWatch` keeps the
plan's latest standing from any call. `planNow` takes a new reading when the
last one is more than five minutes old. `planText` and `searchText` write the
two blocks.

**[channel.go](../internal/app/channel.go)** is the channel for other readers.
`remember` and `latest` hold the last delivery in memory. `share` posts it
once, guarding against a double post, along with anything sent with it.
`shareBrief` is what the daily run calls, with the closer look. `shareIdeas`
posts a closer look on its own. `handleShare` is the command.

**[search.go](../internal/app/search.go)** runs news search beside the feeds.
`collectSearch` runs the searches within two minutes, and logs what they found
and cost. `searchSince` is where they start: the previous brief, at least a day
back and at most a week. `recordSearch` writes the comparison with the feeds
into the run record. It uses `onlySearch` and `anySearch` to sort each story by
who carried it. `searchFailure` is the one id that a failed round of searches
is recorded under.

**[filings.go](../internal/app/filings.go)** handles SEC filings and FRED.
`collectFilings` gathers filings as articles. `watchedTickers` is every symbol
across every sector. `SECSourceEntry` gives filings a source entry, so they can
be scored like anything else. `collectLevels` reads FRED.

### config

Everything here can be read and changed without writing Go. It is built into
the program, so a change reaches the service with the next deploy.

**[sectors.yaml](../config/sectors.yaml)** lists the sections of the brief, in
order, each described in plain words. The sorting and the review judge an
article against the description. The news search uses its first sentence.

**[companies.yaml](../config/companies.yaml)** lists the companies followed, by
sector. Each has its ticker and the names headlines use. It also says whether
the ticker or the name is too ordinary a word to match on. It is the one list
the whole service reads, for prices, filings, the moves line, matching, and the
checks for "already followed".

**[sources.yaml](../config/sources.yaml)** lists the feeds, each with its
weight, whether it is read, and the history of why.

**[prompts.md](../config/prompts.md)** holds every instruction the service
gives a model, one `=== id ===` section each. Its `writing` section, the
plain-writing rules, is added to every prompt whose words the reader sees.

**[method.md](../config/method.md)** is the house method for reading accounts,
with CAN SLIM's questions and where each one misleads. It is in Agent Skill
format and is added to the analysis's system prompt. It is the only copy.

**[config.go](../config/config.go)** holds the program's settings, read
entirely from the environment. `Load` reads and checks them. `NextRun` works
out when the next brief is due, skipping weekends. `ClockTime`,
`ParseClockTime` and `Next` handle the schedule's time. The `envOr`, `envInt`,
`envBool` and `envDuration` functions each read one variable, with a default.

**[watchlist.go](../config/watchlist.go)** reads the three lists. `Watchlist`
puts sectors.yaml and companies.yaml together as `[]model.Group`. It refuses a
company in an unknown sector, or a ticker listed twice. `Feeds` reads
sources.yaml. `Edits`, `applyEdits` and `pruneEdits` are the changes made from
Telegram and how they sit on top of the files. An edit that the files already
say is dropped. That is how the list kept on the server empties itself once a
sync has been deployed. `applySwitches` and `pruneSwitches` do the same for
feeds.

**[prefs.go](../config/prefs.go)** is what is kept on the data volume: the chat
id, the last brief's message ids, and the Telegram changes. `LoadPrefs` reads
it and works out `Groups` and `Sources`, the lists in force. `AddCompany`,
`RemoveCompany`, `ResetEdits` and `SwitchFeed` are what the commands change.
`Save` writes it in one step, so it is never left half written. A file from
before the lists moved out of it still loads. Its old copies are ignored and
dropped at the next save.

**[fold.go](../config/fold.go)** has `Fold`. It writes the Telegram changes
into companies.yaml and sources.yaml line by line. So a sync changes only the
lines it must, and leaves comments and blank lines alone.

**[prompts.go](../config/prompts.go)** reads the prompts. `Prompt` reads a
section of prompts.md, and adds the writing rules to the ones the reader sees.
`RenderPrompt` fills in a template. `CheckPrompts` checks at start-up that each
section still has the markers its reply is read by. `PROMPT_FILE` points to an
outside copy, so you can edit prompts without a rebuild.

**[method.go](../config/method.go)** has `Method`, the method with its skill
header removed.

**[dotenv.go](../config/dotenv.go)** reads `.env` for runs on your machine.

### internal/model

This is plain data with almost no behaviour, and everything imports it.

**[article.go](../internal/model/article.go)** has `Article`, plus `ArticleID`
and `CanonicalURL`. These strip tracking parameters from web addresses, so the
same story from two places is recognised as one. `Also` holds the sources whose
copies were folded into an article, and `Carriers` lists every source a story
arrived from.

**[group.go](../internal/model/group.go)** has `Group`, a sector, with its
description and its companies. `Company` is one company followed, with the
names it is matched by and `Match`, which keeps an ordinary word from being
matched. `Symbols`, `MatchSymbols`, `MatchNames` and `Has` read them.

**[report.go](../internal/model/report.go)** has `Report`, `Section` (with the
`Movers` shown under its heading) and `Usage`. `Referenced` returns the
articles the text actually cites. `Cited`, by contrast, is everything the model
was offered.

**[candidate.go](../internal/model/candidate.go)** has `Candidate`, a new name
in the news, and `Symbol`, which writes it as `700.HK` or `NVDA`.

**[idea.go](../internal/model/idea.go)** has `Idea` and the verdict constants.

**[quote.go](../internal/model/quote.go)** has `Quote`, with `Move` (the
percentage) and `Unit` (the currency). `Moves` writes a list of them as one
line.

**[trading.go](../internal/model/trading.go)** has `Trading`, what a share has
been doing: returns, averages, range, volume and volatility. `Market` holds the
S&P 500's returns over the same stretches, and `MarketOver` finds one. `Stale`
refuses a history that stops weeks ago.

**[source.go](../internal/model/source.go)** has `Source`, a feed.

### internal/feed

**[fetch.go](../internal/feed/fetch.go)** fetches the feeds. `Fetcher.Fetch`
fetches every source at once. `fetchOne` and `normalize` handle one each.
`SourceError` names the feed that failed, without failing the run.

**[parse.go](../internal/feed/parse.go)** turns RSS and Atom into `Item`s. Most
of the file guards against bad input. `looksLikeHTML` catches a feed that has
turned into a web page. `stripComments` and `charsetReader` handle broken XML
and Latin-1 text. `parseTime` tries a dozen date formats. `stripHTML` and
`unescape` clean the summaries. `Summarize` is exported, so search snippets are
cleaned and cut to the same 400 characters as feed summaries.

**[collect.go](../internal/feed/collect.go)** is the gathering pipeline.
`Collect` runs it, and `Result` is what it reports. It holds `DropStale` and
`Dedupe`, with its helpers `dedupeByURL`, `dedupeByTitle`, `clusterByTitle`,
`merge` (which also records the folded copy's source in `Also`) and `better`.
It also holds `Match` (with `mentionsTicker` and `mentionsWord`), `score` and
`Limit`.

**[similar.go](../internal/feed/similar.go)** is the title test that removing
duplicates uses: `titleTokens`, `stripOutletSuffix`, `similarity` and
`sameStory`. `contradicts` stops "X buys Y" from being merged with "X denies
buying Y".

### internal/sec

**[sec.go](../internal/sec/sec.go)** reads EDGAR. `Client.Collect` turns recent
important filings into articles. `tickerIndex` maps symbols to CIKs. `recent`
reads a company's filings, and `materialFilings` picks the 8-Ks out of them.
`materialCodes` keeps only the 8-K items that matter. `LookupCIK`, `Recent`
(important 8-Ks, and a foreign filer's 6-Ks), `AnnualReport` and
`BusinessSection` serve `/analyse`. `MainTicker` says whether a ticker is its
company's main listing. That keeps a bank's notes out of the market's movers.

**[release.go](../internal/sec/release.go)** has `EarningsRelease`. It finds
the latest 8-K under Item 2.02 and reads the press release filed with it. That
is the document the filing's index page lists as EX-99.1, whatever the file is
called (`releaseURL`). It is returned as text, cut to a length. A foreign filer
reports on 6-K, which has no item codes. For those, it takes the 6-K whose
cover lists results.

**[covers.go](../internal/sec/covers.go)** reads a 6-K's cover page, which
lists its attachments by title (`coverExhibits`, `exhibits`, at most
`maxCovers` a search). It picks the one announcing results (`resultsExhibit`).
That finds Alibaba's, JD's and PDD's. Some filers have no release found. At Sea
the cover gives no title. At TSMC and Novo Nordisk the 6-K is the announcement
itself. `announcements` turns a foreign filer's 6-Ks into what it has announced
lately, using the same titles. It leaves out the Hong Kong share returns,
meeting notices and other routine filings (`routineTitle`).

**[business.go](../internal/sec/business.go)** takes the business description
out of an annual report's HTML. For a 10-K that is Item 1. For a foreign
filer's 20-F it is part B of Item 4, Business Overview (`businessText`,
`longestSection`, `plainText`).

### internal/search

The media half of the feed list is the half that breaks. Outlets move their
feeds, put them behind bot protection, or never offer one. This package asks
Tavily for the news instead. It doesn't replace the government and company
feeds, which carry the documents themselves rather than articles about them.

**[search.go](../internal/search/search.go)** is the Tavily client.
`Client.Collect` runs the searches four at a time, and keeps each article only
once across them. `searchOne` sends one search: a `news` search at `basic`
depth, which costs one credit, for twenty results, limited to the outlets, over
the past day or since the previous brief. It turns the results into articles.
Tavily returns a short passage from each page, not the whole page. A result
with no date is dropped, since it is a quote page or a section front rather
than a report. So is a dated page that isn't one story (see pages.go). `Usage`
reads the month's credit use for free. `statusError` turns Tavily's refusals
into advice, including `ErrOutOfCredits` for its 432 and 433 codes. `parseDate`
and `cleanSnippet` handle Tavily's date format and its extracted text.

**[pages.go](../internal/search/pages.go)** has `isStory`, which keeps a result
only if it is one report. It drops section fronts, topic fronts, quote pages
and live blogs by their address. It drops the recording of a whole programme by
its title, which is only a show's name and a date ("Post Market Wrap:
September 23, 2026"). A live blog goes because its headline changes with its
latest entry, so the link stops leading to what the brief cited. A video clip
about one story is kept. In the first brief written with search, three clips
carried facts that no article did.

**[outlets.go](../internal/search/outlets.go)** has `Outlets`, the
publications a search may return. Each has the name the brief shows and a
weight on the feed list's scale. `Domains` sends them as the search's limit,
and `Sources` gives the scorer a weight for each. `IDPrefix` (`web:`) and
`IsSearch` mark a source id as a search result. `outletFor` maps a web address
to its outlet. `trimOutlet` takes " - Reuters" off the end of a headline.
Otherwise every headline from one outlet would look a little alike when
duplicates are removed.

**[queries.go](../internal/search/queries.go)** builds the searches. `Queries`
builds the round: `General` first, then one search for each sector from
`groupQuery`. That uses the first sentence of the sector's description, or the
companies' names when there is none. `MaxQueries` limits a round to twenty, so
adding sectors can't run up the bill. `MoverQuery` asks why one share moved, for
ten results rather than twenty. `PlainName` takes the corporate words off a
name as the SEC files it. `MaxMoverQueries` limits mover searches to five a
brief.

**[live_test.go](../internal/search/live_test.go)**, with `SEARCH_LIVE=1`, runs
the real searches beside the real feeds. It reports what each found that the
other didn't. It spends about fifteen credits and sends nothing.

### internal/triage

**[triage.go](../internal/triage/triage.go)** has `Triager.Triage`. It sends
the day's articles to Opus in batches and applies what comes back.
`describeSectors` writes out the sectors the model files articles under, and
the review uses the same text. `parse` reads the `number|rating|watchlist ids`
replies. `apply` writes the ratings and sectors onto the articles. It keeps the
sectors chosen for articles rated 3 in `Article.Reserve`.

**[memory.go](../internal/triage/memory.go)** has `Memory`, the sorting's
earlier verdicts by article id, kept in `sorted.json` for eight days. A verdict
is given again only under the same sorting prompt.

**[review.go](../internal/triage/review.go)** has `Reviewer.Review`. It shows
the articles that could reach the brief, with where each sits, and applies the
moves that come back (`parseReview`, `applyReview`). `Move` is one of those
moves, kept for the run record. `MinReviewRating` is 3.

**[topup.go](../internal/triage/topup.go)** has `TopUp`. It fills a section
with fewer than `ThinSection` (10) articles from the sectors the sorting kept
in reserve for articles rated `ReserveRating` (3). `applyReview` settles each
one.

### internal/report

**[generate.go](../internal/report/generate.go)** has `Generator.Generate`. It
chooses the sections, builds the prompt, makes the call and assembles the
`Report`.

**[prompt.go](../internal/report/prompt.go)** has `buildPrompt` and its parts.
It also has the limits that decide what is worth writing about:
`MinSectionArticles` 3, `MaxSectionArticles` 25, `MinGeneralRating` 4 and
`MinSectionRating` 3. `market` carries what was measured rather than written.

**[moves.go](../internal/report/moves.go)** has `sectionMoves`, which picks a
watchlist's biggest moves of the day. A move must be at least `MinMoveShown`
(1%), and at most `MaxMovesShown` (four) are shown. `trend` describes a mover
against its own history.

**[parse.go](../internal/report/parse.go)** has `parseResponse`, which splits
the reply at its markers.

### internal/discover

**[discover.go](../internal/discover/discover.go)** has `Finder.Find`. It asks,
reads the reply, merges duplicates, and removes names already followed. Then it
checks the tickers, applies the bar for evidence (`withEvidence`) and cuts the
list to a limit.

**[verify.go](../internal/discover/verify.go)** has `FIGI.Verify`, which checks
tickers against OpenFIGI. `Exchanges` lists the exchange codes a ticker may be
checked under. It is `model.Markets`, which also gives each exchange's country
and the flag shown beside its tickers. `SameCompany` compares a registered name
with a claimed one, ignoring endings like "Inc".

**[store.go](../internal/discover/store.go)** has `Store.Note`, which counts
how many days a name has been in the news, and `Save`. Names are kept for sixty
days.

### internal/ideas

**[ideas.go](../internal/ideas/ideas.go)** holds the limits: `PicksShown`
(ten), `ReactionsShown` (three), `ReactionsJudged` (six), `PopularThemes`
(three), `EarlyThemes` (two), `PerTheme` (four), `PicksJudged` (sixteen) and
`RepeatWindow` (eight weeks). It also has `PickExchanges` and `verify`.

**[themes.go](../internal/ideas/themes.go)** has `Sorter.Sort`, `Scout.Find`
and `Researcher.Research`, the three stages of the week's themes, with their
prompts, `parseThemes` and `parseResearch`. `IndustryLine` writes out an
industry's figures.

**[themelog.go](../internal/ideas/themelog.go)** has `ThemeLog`, with
`DoneThisWeek`, `Names` of last week's themes, and `Previous` research on a
theme with the same name.

**[valuation.go](../internal/ideas/valuation.go)** has `Valuation`: a
company's `Multiples` against its theme's `Medians` and its own history.
`BuyClosed` applies the two rules. `Facts` writes it out for the verdict, and
`WarningSigns` counts the warning signs.

**[judge.go](../internal/ideas/judge.go)** has `Judge.Judge`, which turns facts
into verdicts. It sends `DefaultBatch` (three) companies a call, two calls at a
time. `judgePrompt` lists only the articles that the batch depends on.
`parseVerdicts` and `normaliseVerdict` read the reply. `hold` applies the
code's rules.

**[scorecard.go](../internal/ideas/scorecard.go)** has `Record` and
`Scorecard.Add`. `Due` says which verdicts are old enough to grade.
`Currencies` says whose exchange rates they need. `Settle` writes down each
verdict's entry price, the first open after it, from a `Path` of sessions.
`Summary` measures each one against the S&P 500 in US dollars from that entry.
It splits the BUYs and SELLs by confidence and by how the company was found
(`Source`). `MinAge` is a week. `Clearly` is the margin a BUY or SELL must
reach: five points a year, scaled to the time passed.

### internal/fundamentals

**[metrics.go](../internal/fundamentals/metrics.go)** reads the accounts.
`Client.Fetch` builds a `Snapshot`. The `concept` tables map each figure to its
US GAAP and IFRS tag names. It also has `buildYears`, `buildBalance` and
`buildYTD`. `supersedes` prefers a later filing to an earlier one that was
restated. `reportingCurrency` picks the currency the filer reports in.
`Balance.CashPot` adds cash to short-term investments only where both are of
one date, so no figure mixes two balance sheets.

**[splits.go](../internal/fundamentals/splits.go)** puts figures a share, and
counts of shares, filed before a stock split on the split's basis. A later
filing restates the periods it shows, but an older year no later filing
shows keeps its old basis: NVIDIA's year to January 2022 read US$3.85 a share
beside US$0.17 a year later. The splits are the ones the company tags, taken
only where the share count filed just after the tag's date is about the
ratio times the one filed just before, since the tags' dates are untidy.

**[xbrl.go](../internal/fundamentals/xbrl.go)** is the EDGAR XBRL client.
`Client.Concept` reads one figure's history. `Annual`, `Quarterly` and
`Instant` filter the readings by period. `Tags` and `Search` list what a filer
reports. `pace` holds every request to seven a second across all the
goroutines that use the client, since the closer look reads four companies at
once.

**[table.go](../internal/fundamentals/table.go)** has `Snapshot.Table`. It
lays the figures out in fixed-width columns. Then come the expectations, the
results release and the backdrop, where they were read. It also has
`valuation`, `trailing` and `revenueGrowth`.

**[analyze.go](../internal/fundamentals/analyze.go)** has `Analyzer.Analyze`,
which makes the call, with `config.Method` added to the system prompt.

**[market.go](../internal/fundamentals/market.go)** writes the trading history
and the news into the prompt as text. Each price move is shown beside the
S&P 500's, where it was read.

**[news.go](../internal/fundamentals/news.go)** has `AddNews`, `SetNews` and
`Relevant`. `Relevant` keeps only the pieces that actually name the company,
and spreads them across days. `SetNews` gives the searches first call on
`searchPlaces` of the places, and the feed the rest.

**[business.go](../internal/fundamentals/business.go)** has `AddBusiness`.

**[related.go](../internal/fundamentals/related.go)** has `SplitRelated`,
`VerifyRelated` and `RelatedFor`.

**[tools.go](../internal/fundamentals/tools.go)** has `AccountTools`, the
analysis's three tools for one company, with the lookup limit.

**[peers.go](../internal/fundamentals/peers.go)** has `Client.Peers`, which
reads the SEC's frames and places a company in its industry group, and
`PeerGroup.Facts`, which writes that out for the analysis and the verdicts.

**[compute.go](../internal/fundamentals/compute.go)** has a small calculator
for expressions, and `Number`, which formats a figure for a reader.

### internal/prices

**[prices.go](../internal/prices/prices.go)** is the Finnhub price client.
`Client.Fetch` works through a list of symbols at a steady pace, up to
`maxSymbols` (150). It gives each one five seconds, and stops after
`maxFailures` (three) failed requests, handing the rest back unasked. An
unknown symbol, which comes back at once with zeros, doesn't count as a
failure. `Benchmarks` is the twelve funds. `MarketSymbol` is the one a share's
move is measured against. `Index` and `LabelFor` help with the layout.

**[history.go](../internal/prices/history.go)** is the chart source, which
needs no key. `History.Fetch` returns a `Series` of daily prices in the local
currency. `Latest` turns the last two closes into a price. It is dated by when
the last price was set (`Series.Traded`), not by the session's day.
`Summarise` turns the whole series into a `model.Trading`.

**[symbols.go](../internal/prices/symbols.go)** has `ChartSymbol`, which spells
a listing the way the chart source does: `0700.HK`, `7203.T`, `BRK-B`. For an
exchange the source doesn't cover, it returns nothing. A guess could return
another company's prices.

**[news.go](../internal/prices/news.go)** has `News.Company`, which reads
company news.

**[massive.go](../internal/prices/massive.go)** reads the whole US market's
day. `Massive.Session` reads every listing's prices for one date, spaced out to
the free plan's five requests a minute. `Splits` reads the splits over a
stretch of days. `CommonShare` tells a common share's symbol apart from a
warrant's, a unit's, a right's or a preferred share's.

**[fred.go](../internal/prices/fred.go)** has `FRED.Fetch`. It reads the
`Indicators`: the ten-year and two-year yields, the yield curve, the Fed's
rate, the S&P, the VIX and inflation. It returns a `Reading` for each, with its
latest, previous and week-ago values. A series can ask FRED to transform it.
For example, `Units: pc1` asks for the change from a year earlier, which turns
the price index into inflation. A `Monthly` series is compared with last month
and has no week-ago value. `Backdrop` is a second list, for the closer look and
`/analyse`: the ten-year yield, oil, gas, copper, the dollar, the credit spread
and the breakeven inflation rate. `Reading.Line` writes a reading out, and
`DescribeMove` puts its direction into words.

### internal/market

**[store.go](../internal/market/store.go)** has `Store`. It keeps one gzipped
file per session in `data/market/`, a marker for a weekday with no trading,
and `splits.json`. `Load` reads a stretch of sessions into a `Panel`. It
applies any splits the source hadn't applied when it served that day.

**[sync.go](../internal/market/sync.go)** has `Store.Sync`. It fetches what is
missing, newest first, within `Reach` (two years). It keeps common shares that
traded at a price of at least a dollar and a value of at least a million
dollars. `Missing` counts what is left to fetch.

**[panel.go](../internal/market/panel.go)** has `Panel` and `Series`, with
`Return`, `Average`, `Volatility`, `Usual` (the average daily move),
`BiggestDay` and `DollarVolume`.

**[screen.go](../internal/market/screen.go)** has `Listing` and `Rules`
(`DefaultRules`: US$2bn, US$5, US$20m a day, thirteen months), `Stock` and
`Measure`. It has `Leaders`. It has `Industry`, `Industries`, `Popular` and
`Early`. And it has `Move` and `Moves`.

**[names.go](../internal/market/names.go)** has `PlainName` and `Mentions`,
which counts the headlines that name each company.

### internal/consensus

**[consensus.go](../internal/consensus/consensus.go)** has `Client.Fetch`. It
reads six of Nasdaq's data addresses for one company, one after another:
earnings forecasts by quarter and year with the changes of the last four
weeks, the price target and ratings, results against forecasts, insider
trades, short interest, and fund holdings. `Estimates`, the forecasts alone, is
what `--check` asks for. `num` reads a figure however the site writes it.

**[listings.go](../internal/consensus/listings.go)** has `Client.Listings`,
which reads Nasdaq's screener: every US listing, its market value, sector and
industry.

**[facts.go](../internal/consensus/facts.go)** has `Report.Facts`, which writes
it all out for a verdict or an analysis. It turns the forecasts into multiples
of today's price.

### internal/calendar

**[calendar.go](../internal/calendar/calendar.go)** has `ForexFactory.Week`,
which reads the week's releases from ForexFactory's weekly export. `Key` keeps
the ones a reader of the US market needs.

### internal/pages

**[pages.go](../internal/pages/pages.go)** has `Store.Publish`, which writes a
page under a random id and deletes pages a month old. `Store.Handler` and
`Store.Serve` answer `GET /r/<id>` and nothing else.

**[doc.go](../internal/pages/doc.go)** has `Doc` and its parts: sections,
tables, bar and column charts, a line chart (`Lines`), paired columns
(`Pairs`), a price's range, facts, a company's card, an industry's chain, and
folded sources. The charts are HTML, CSS and inline SVG, with no scripts and
no images. The line chart draws the price's last year against its 50- and
200-day averages, from `model.Trading.Path`, on the analysis page and on each
closer-look card. The paired columns set a company's cash from operations
beside its capital spending, year by year, with the free cash flow under
each year.

**[render.go](../internal/pages/render.go)** has `Render` and `Fragment`. They
lay a page out from its `Doc`, or from Telegram's messages when it has none.
They escape everything except Telegram's tags.

### internal/industry

**[industry.go](../internal/industry/industry.go)** has `Explainer.Explain`,
which asks for an industry's map. `Split` and `Verify` take out its companies
and check them.

### internal/telegram

**[client.go](../internal/telegram/client.go)** is the Telegram client. It has
`SendMessage`, `Send`, `SendReport`, `Broadcast`, `DeleteMessages`,
`SweepMessages`, `Me` and `CanPost`. `do` handles rate limits and retries.
`redact` keeps the bot token out of errors.

**[updates.go](../internal/telegram/updates.go)** has `Poll`, which keeps
asking for messages. `SetMyCommands` publishes the menu. `DrainUpdates`
throws away commands sent while the program was down.

**[render.go](../internal/telegram/render.go)** lays the brief out as
messages. `RenderWith` puts the pieces together, and `pack` spreads them across
messages. `paragraphs` and `bullets` turn the `### ` sub-headings and `- `
points into bold lines and bullets (`emphasizeLabel` handles the older
label-and-dash blocks). `linkCitations` turns `[3]` into a link. Each section's
heading carries its line of biggest moves, and the overview's heading the
line of moves across the market. `renderCandidates` draws the new
names, `renderSources` the links, and `renderFooter` the token counts.
`RenderPlain` does the same for an analysis, and `RenderAnalysis` puts the
verdict last.

**[summary.go](../internal/telegram/summary.go)** writes the one-message
summaries. `AnalysisSummary` gives the analysis's IN SHORT section, with its
glossary terms linked, then the verdict in one line. An analysis without that section gives the best point
of each group in the case for and the case against instead.

**[ideas.go](../internal/telegram/ideas.go)** has `RenderPicks` and
`renderIdea`, the closer look's layout: `Picks` of `ThemeView`s, reactions
and `EarlierPick`s.

**[related.go](../internal/telegram/related.go)** has `RenderRelated` and
`RelatedList`, used by the analysis and anything else with a list of
companies.

### internal/relay

**[relay.go](../internal/relay/relay.go)** is the run. `Relay.Begin` opens a
folder, and `prune` keeps the last forty. `Run.Ask` writes the request, gets it
answered and writes the reply. It also copies both into the run cache that the
context carries. `Run.Note` keeps the ledger. `Stage` and `Plain` fit a relay
to the `Completer` interfaces the other packages expect.

**[answer.go](../internal/relay/answer.go)** is who answers. `Claude.Answer`
runs `claude -p`. `DefaultModels` maps every stage to Opus 5.5
(`claude-opus-5-5`). `webStages` are the stages with web search. `childEnv`
removes the API key. `parseStream` reads the result and the plan's `Limits`
from the stream. `CheckLimits` takes a reading for `/usage`. `Session.Answer`
waits for a person.

**[tools.go](../internal/relay/tools.go)** is a tool server offered to one
call. `WithTools` puts it on a context, and `mcpConfig` writes it out for
`--mcp-config`.

### internal/mcp

**[mcp.go](../internal/mcp/mcp.go)** has `Serve`, which speaks the Model
Context Protocol on standard input and output: `initialize`, `tools/list`,
`tools/call` and `ping`, one JSON message a line. A tool's error goes back to
the model as its result, for it to read and try again.

### internal/history

**[history.go](../internal/history/history.go)** keeps what earlier briefs
covered. `Store.Mark` flags repeats, `Seen` counts them, and `Record` writes
them down. They are kept for three weeks.

**[runs.go](../internal/history/runs.go)** has `Runs.Add`, which records a
run. `Summary` writes out `/stats`, with a few of the sorting's placements, the
review's moves and the top-ups it kept, from the latest run. The last thirty
runs are kept. `searchSummary` is the news-search block of `/stats`. It shows
articles and credits for each brief, stories only search found, and the cited
stories it missed, by source, most missed first.

### internal/runcache

**[runcache.go](../internal/runcache/runcache.go)** keeps the latest run of
each kind, to read afterwards. `Cache.Start` gives a new run a folder of its
own under `.running/` and puts an `Entry` on the context. `From` finds it again
deep in a pipeline. When the run finishes, `keep` moves it into its kind's
folder (`brief`, `analysis`, `recommendations` or `industry`). Two runs of one
kind can overlap and finish in either order. So `keep` compares the end
times in `run.json`, and a run that ended before the one already kept is
thrown away. The folder always holds the run that finished last. A run cut
short, by a restart say, stays
under `.running/` until the next run of its kind starts. `Save` writes a step's data as JSON,
numbering a name that is used twice. `Text` writes the messages and the model
calls. `Fail` and `Finish` write `run.json`. Everything written is scrubbed of
the settings' secrets. Every method does nothing on a nil entry, so a run
without a cache works the same as before. `sendReport`, `handleAnalyse` and
`sendIdeas` are what save into it. `feed.Result.Arrived` and `Cut` exist for
it. RUNBOOK.md lists the files.

### internal/logging

**[scrub.go](../internal/logging/scrub.go)** is a `slog.Handler`. It replaces
every known secret with `[redacted]` in every message, attribute and group. So
a token can't leak through an error message nobody thought about.

---

## What lives on disk

On Fly this is the `market_watch_data` volume at `/data`. On your machine it
is `./data`.

| File | Written by | Holds |
|---|---|---|
| `prefs.yaml` | [config/prefs.go](../config/prefs.go) | The owner's chat id, the last brief's message ids, and the watchlist and feed changes made from Telegram |
| `covered.json` | [history/history.go](../internal/history/history.go) | Which stories earlier briefs carried, going back three weeks |
| `sorted.json` | [triage/memory.go](../internal/triage/memory.go) | The sorting's verdict on each article it has read in the last eight days, so it isn't asked again |
| `runs.json` | [history/runs.go](../internal/history/runs.go) | The last thirty runs, for `/stats` |
| `candidates.json` | [discover/store.go](../internal/discover/store.go) | New names, and how many days each has been in the news |
| `scorecard.json` | [ideas/scorecard.go](../internal/ideas/scorecard.go) | Every verdict, plus the prices it is measured from once its next session has opened |
| `themes.json` | [ideas/themelog.go](../internal/ideas/themelog.go) | The last half-year of weekly themes, with what each one's research said and what was picked |
| `market/` | [market/store.go](../internal/market/store.go) | Two years of the US market's daily prices, one file for each session, forty megabytes in all; the splits since; and Nasdaq's list, at most a day old |
| `frames/` | [fundamentals/peers.go](../internal/fundamentals/peers.go) | The SEC's figures for every filer for a calendar year or quarter, one file a figure and period, read again after a week (90 days for a period over a year old) |
| `pages/` | [pages/pages.go](../internal/pages/pages.go) | The web pages the summaries link to, one file each, deleted after 30 days |
| `relay/` | [relay/relay.go](../internal/relay/relay.go) | The last forty runs: every request, every reply, and a ledger for each run |

On your machine, [scripts/sync-from-fly.sh](../scripts/sync-from-fly.sh)
copies these down from Fly. It keeps what they replace in `data/.backup/`.

---

## Configuration

All settings are environment variables, read once by
[`config.Load`](../config/config.go#L206). The deployed values are in
[fly.toml](../fly.toml). The secrets are Fly secrets, set from `.env` by
[scripts/fly-deploy.sh](../scripts/fly-deploy.sh) without being printed.
[.env.example](../.env.example) explains every one. What the service follows
is not a setting in this sense. It is the files in [config/](../config/).

A missing **secret** usually turns off one feature rather than failing the
run.
- No `FRED_API_KEY` means no market-levels block.
- No `TAVILY_API_KEY` means no news searches, so the brief comes from the
  feeds alone.
- No `FINNHUB_API_KEY` means no US prices. So there are no lines of biggest
  moves, no searches for shares that moved, and no company news. But listings
  outside the US are still priced for the analysis, because the chart source
  needs no key.
- No `MASSIVE_API_KEY` means no market history, and so no closer look at all.
- `CONSENSUS=false` stops asking Nasdaq. The verdicts and analyses then go
  without what analysts expect.
- No `USER_AGENT` means no SEC filings, because EDGAR refuses a request that
  doesn't say who is asking (403).

---

## A few things worth knowing

**Nil means off.** Most of `App`'s fields are pointers that may be nil, and
each nil turns off one feature. This is on purpose. A service missing one key
should lose one section, not refuse to start.

**The relay is the only way to a model.** There is no API client anywhere in
this repository. Every call is a file, a process without a screen, and a file
back.

**Checking tickers is not optional.** Every ticker the service shows a reader
has been checked against OpenFIGI first. That covers new names, closer-look
companies, and the analysis's related list. If the check fails, the service
shows nothing rather than an unchecked symbol.

**Order matters in `sendReport`.**
- Prices come first, in the background. They are the slowest thing gathered,
  and they decide which shares get a search of their own.
- Filings and searches come before the feeds, so their duplicates are removed
  along with everything else's.
- The review comes after the cut, so it reads only what can reach the brief.
- The last brief is cleared after the new one is written. So a failed run
  doesn't also throw away the brief it failed to replace.
- What was covered is recorded after delivery, not before.

**A verdict published is not a verdict checked.** `sendIdeas` always sends to
the owner. It also sends to the channel when the brief went there. So the daily
run shares both, and a `/now` shares neither. The channel's copy is laid out
separately, under a note saying a model wrote it, that nobody checked it, and
that it is not advice to act on. That note is the condition the section is
published under, not a formality. See `channelNote` in
`internal/telegram/ideas.go`, and the section in RUNBOOK.md.

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → **3 How it works** → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

**Next:** [4 Function by function](FUNCTIONS.md), each feature on its own, with its starting point and its code in the order it runs.
