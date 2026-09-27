**Start here:** [1 README](../README.md) → **2 Glossary** → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

---

# Glossary

The words the code, the docs and the messages use, in plain terms. Where a term lives in one place in the code, the link goes there.

Skim this once. Then keep it open beside the other pages and come back whenever a word stops making sense.

---

## What the reader sees

**Brief.** The daily message. It is sent on weekdays at 07:30 New York time, two hours before the US market opens. That is 19:30 in Singapore while the US is on daylight saving time (until 1 November), and 20:30 after. It has an overview, then one section per sector, and every claim carries a numbered link to its article. Built by [`sendReport`](../internal/app/app.go#L425).

**Sector, section, watchlist.** The brief's sections, such as chips or banks. Each is described in plain words in [config/sectors.yaml](../config/sectors.yaml). The companies each one follows are listed in [config/companies.yaml](../config/companies.yaml). "Followed" means listed there.

**Moves line.** The line under a section's heading showing its followed shares' biggest moves on the day ([report/moves.go](../internal/report/moves.go)).

**Movers.** Followed shares that moved at least 3 percentage points more than the S&P 500 fund, in either direction. Up to 5 get a news search of their own asking why ([movers.go](../internal/app/movers.go)).

**Covered, repeat.** A story an earlier brief already carried. It is marked, not dropped, so the brief reports it only as an update. The record is `covered.json` ([history.go](../internal/history/history.go)).

**New names in the news.** Companies the day's stories were about that no sector follows. Each ticker is checked against the exchange before it is shown ([discover](../internal/discover/)).

**Worth a closer look, the closer look, recommendations.** The message sent about 20 minutes after the brief, with BUY, HOLD or SELL verdicts on companies no sector follows. It has two parts, *themes* (weekly) and *reactions* (daily). [`sendIdeas`](../internal/app/ideas.go#L91).

**Theme.** A group of companies with one story behind them, such as "AI memory demand". There are up to 3 **popular** themes each week, sorted by Sonnet from the market's leaders, and up to 2 **early** themes, found by Opus with the web from industries that are starting to turn. [`runThemes`](../internal/app/themes.go#L72).

**Pick.** A company proposed by a theme's research and judged. Only BUYs and SELLs are shown, up to 10 a week.

**Reaction.** A share that moved at least 3 times its *usual daily move*, on at least twice its usual trading, where the brief's articles explain why. The verdict asks whether the market over- or under-reacted. Up to 3 a day. [`runReactions`](../internal/app/reactions.go#L45).

**Earlier picks.** The last 8 weeks' theme picks, listed under the week's themes with how each has done since.

**Owner.** The one Telegram chat the bot answers: the first chat to send `/start`. Commands from any other chat are ignored.

**Channel.** A Telegram channel other people can read but not write to (`TELEGRAM_CHANNEL_ID`). The daily brief and its closer look are posted there; `/share` posts the last brief or analysis.

**Channel note, `channelNote`.** The warning at the head of the channel's closer look: a model wrote it, nobody checked it, it is not advice. Verdicts may go to the channel only because of it. [telegram/ideas.go](../internal/telegram/ideas.go#L31).

---

## Market measures

**S&P 500, SPY, the index, the benchmark.** The 500 largest US companies. SPY is the fund that tracks it, and every return here is measured against SPY's.

**Points, "+59 pts".** Percentage points better (or worse) than SPY over the same stretch. "+59 pts over 12 months" means the share's 12-month return was 59 percentage points above SPY's.

**Session.** One trading day.

**12-1.** The return over the last 12 months *leaving out* the latest month. Shares' latest month tends to reverse, so it's taken out.

**2y.** The return over about two years: in practice 23 months (`TwoYears - Month` sessions), because the stored history is a few days short of two full years.

**50-day and 200-day average.** The average closing price over the last 50 or 200 sessions. "Above its 200-day average" is the usual test of a share in an uptrend.

**Breadth.** The share of an industry's companies above their 200-day average. The early test uses breadth at the 50-day: how many are above their 50-day average now, against a month ago.

**Usual daily move.** The average size of a share's daily move, up or down, over the last 60 sessions ([`Series.Usual`](../internal/market/panel.go#L194)). "7.1 times usual" means today's move was 7.1 times that.

**Value traded, trading.** Shares traded times price, in dollars. "2.3 times the trading" means 2.3 times its usual day's value traded.

**Eligible.** The floor a US company must clear to be considered: worth at least US$2bn, priced at least US$5, at least US$20m traded a day, and 13 months of history (`DefaultRules` in [screen.go](../internal/market/screen.go)). Funds, blank-cheque companies, warrants and preferred shares are left out.

**Leaders.** The 150 eligible companies ranked best on four measures: 2y, 12-1, 6 months, and the year's return for each unit of its swing. Each must also be above its 200-day average, must not have made most of its year's rise in one day, and must not be pinned to a takeover offer. [`Leaders`](../internal/market/screen.go#L167).

**Industry.** Nasdaq's industry label for a company, such as "Semiconductors". It is read from Nasdaq's list of every US listing ([consensus/listings.go](../internal/consensus/listings.go)).

**Popular industry.** One whose shares have already risen. It is ranked on its median member's 6- and 12-month return against SPY, its breadth, money flowing in against a year ago, and how often its companies are named in recent headlines. [`Industries`](../internal/market/screen.go#L267).

**Early industry.** One starting to turn while its year still lags. Its 12 months are behind the typical industry's, its 3 months are ahead of the typical industry's, and its breadth at the 50-day rose by more than the typical industry's did. "Typical" means the median industry, so the test still works in a month when the whole market falls.

**Named in N recent headlines, mentions.** How many of the last three weeks' brief headlines name one of the industry's companies ([`Mentions`](../internal/market/names.go#L77)).

**Multiples.** P/E (price to earnings), P/S (price to sales) and EV/EBIT (the whole business's value to its operating profit). A pick's multiples are compared with its theme's median and with its own last five years.

**P/S per growth point.** Price to sales divided by revenue growth in percent: what a share costs against how fast its sales are growing.

**Warning signs.** Any of: more than 40% above its 200-day average; above the analysts' average price target; insiders sold a net 0.25% or more of the company in three months; 10% or more of the shares sold short. [`WarningSigns`](../internal/ideas/valuation.go#L188).

---

## Verdicts and the scorecard

**Verdict: BUY, HOLD, SELL.** BUY means the share should beat SPY by at least 5 points over 12 months, in US dollars. SELL means it should trail SPY by at least that much. HOLD is anything between. HOLDs are not shown.

**Confidence.** High, medium or low, given by the model. The code caps it at low when no accounts were read (every Singapore company, and anything that doesn't file with the SEC).

**Buy closed, overruled.** The code's valuation rules, which the model can't bend ([`BuyClosed`](../internal/ideas/valuation.go#L109), [`hold`](../internal/ideas/judge.go#L138)). A BUY becomes a HOLD, with the reason shown, if either holds:
- it is dearer than its theme on every multiple and its growth doesn't pay for it;
- it is dearer on every multiple and shows 2 or more warning signs.

**Catalyst, sensitivity, case, risk.** Parts of a verdict:
- catalyst: what is coming that could move the share;
- sensitivity: how much the earnings swing with the business's main lever;
- case: the argument;
- risk: the single biggest risk.

**Scorecard.** Every BUY and SELL shown, and every `/analyse` verdict, with how each has done against SPY ([scorecard.go](../internal/ideas/scorecard.go)). It is kept in `scorecard.json`. It was wiped clean on 27 September 2026, so it starts from the run of 28 September.

**Entry.** The price a verdict is measured from: the next session's open after the verdict, for the share and SPY alike. Using a later price would let the verdict see the future.

**Clearly.** The bar a verdict must clear to count as right: 5 points a year against SPY, pro-rated, so about 0.4 points after a month (`Clearly` in [scorecard.go](../internal/ideas/scorecard.go)). A verdict is graded once it is 7 days old (`MinAge`).

**Source.** How a verdict was found: `theme`, `reaction`, `analysis` (from `/analyse`), or `news` (the old closer look, before 27 September 2026).

**8-week no-repeat.** A company given the same verdict within the last 8 weeks isn't shown again as a pick. It appears under earlier picks instead (`RepeatWindow`).

---

## Filings and where the data comes from

**SEC, EDGAR.** The US securities regulator, and its free filings database.

**8-K.** A US company's report of a material event. Item 2.02 is results, and the press release is usually attached as exhibit 99.1.

**10-K, 20-F.** The annual report of a US company (10-K) or a foreign company with a US listing (20-F).

**6-K.** A foreign company's report of news, its version of the 8-K.

**XBRL.** The tagged-data format filings are made in. The accounts in `/analyse` and the verdicts are read from it ([fundamentals](../internal/fundamentals/)).

**CIK.** The SEC's number for a company.

**OpenFIGI, FIGI.** A free registry of securities. Every ticker shown to a reader is checked against it first, so the service never shows a symbol it can't confirm.

**Consensus.** What analysts expect: forecasts, their revisions, price targets, ratings, results against forecast. It also covers insider trades, short interest and fund holdings. All are read from the data behind Nasdaq's website ([consensus](../internal/consensus/)).

**Massive.** The service giving every US listing's daily bar for a date: open, high, low, close and volume. The free plan allows 5 requests a minute and 2 years back. It fills the **market store** ([prices/massive.go](../internal/prices/massive.go)).

**Finnhub.** Live share quotes and company news ([prices/prices.go](../internal/prices/prices.go)).

**Chart source.** Yahoo's keyless daily-price endpoint. It covers listings outside the US, in their own currency, and fills in what Finnhub misses ([prices/history.go](../internal/prices/history.go)).

**FRED.** The St. Louis Fed's data: yields, fed funds, the VIX, inflation, oil, copper, the dollar ([prices/fred.go](../internal/prices/fred.go)).

**Backdrop.** The FRED figures shown above every verdict and analysis.

**Tavily.** A news search engine, run beside the feeds for the brief, plus 2 searches per `/analyse` ([search](../internal/search/)). It is billed in credits.

**STI.** Singapore's Straits Times Index. Its 30 companies are scored beside the US market's, in dollars ([config/singapore.yaml](../config/singapore.yaml)).

---

## Inside the code

**The relay.** The only way the app calls a model. Each call is a request file, answered by a fresh headless `claude -p` on the owner's Claude subscription, with the reply written beside it. There is no API key. [relay](../internal/relay/).

**Stage.** The name of a kind of model call, which fixes its model and whether it may use the web: `triage`, `review`, `brief`, `names`, `themes`, `scout`, `research`, `verdicts`, `analysis`. There is a table in [FUNCTIONS.md, section 12](FUNCTIONS.md#12-model-calls-the-relay).

**Run, run folder, ledger.** One brief, closer look or analysis, with all its model calls in one folder, `data/relay/<time>-<kind>/`. The ledger is a checklist of its calls.

**Run cache.** The latest run of each kind (`brief`, `analysis`, `recommendations`), with each step's data as JSON, in `data/cache/<kind>/`. It is the first place to look when judging a run ([runcache](../internal/runcache/)).

**Triage, sorting.** Sonnet rating every article 1 to 5 and placing it in up to 2 sectors ([triage.go](../internal/triage/triage.go)).

**Review.** A second Sonnet pass, with the web, that moves misplaced articles ([review.go](../internal/triage/review.go)).

**Top-up, reserve.** A section with fewer than 10 articles is offered the ones rated 3, which are kept "in reserve", but only where the review agrees ([topup.go](../internal/triage/topup.go)).

**Prefs, edits.** `prefs.yaml` on the server holds the owner's chat, the last brief's message ids, and the watchlist and feed changes made from Telegram ("edits").

**Fold.** Writing those edits into the files in `config/` (`--fold`, which `scripts/sync-from-fly.sh` runs).

**Data volume, data folder.** Where everything the service keeps lives: `/data` on Fly, `./data` locally (not in git). See the table in [ARCHITECTURE.md, "What lives on disk"](ARCHITECTURE.md#what-lives-on-disk).

**Look, pending look.** The closer look waiting its 20 minutes after the brief, in `pending-look.json` ([look.go](../internal/app/look.go)).

**Market store, panel, series.** The market store is the two years of daily bars on disk, one file per session. A panel is a stretch of them loaded into memory, and a series is one share's row ([market](../internal/market/)).

**Fly, machine.** The hosting service and the single small server the app runs on (`joseph-market-watch`, in Singapore).

---

**Start here:** [1 README](../README.md) → **2 Glossary** → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

**Next:** [3 How it works](ARCHITECTURE.md). Read its first three parts to see the whole system as the story of one day.
