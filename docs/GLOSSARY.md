**Start here:** [1 README](../README.md) → **2 Glossary** → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

---

# Glossary

These are the words the code, the docs and the messages use, in plain terms. Where a term lives in one place in the code, its link goes there.

Skim this page once. Then keep it open beside the other pages, and come back whenever a word stops making sense.

---

## What the reader sees

**Brief.** The daily message. It goes out on weekdays at 07:30 New York time, two hours before the US market opens. In Singapore that is 19:30 while the US is on daylight saving time (until 1 November), and 20:30 after that. It starts with an overview, then has one section for each sector. Every claim carries a numbered link to its article. [`sendReport`](../internal/app/app.go#L525) builds it.

**Sector, section, watchlist.** These all mean the brief's sections, such as chips or banks. Each one is described in plain words in [config/sectors.yaml](../config/sectors.yaml). The companies each one follows are listed in [config/companies.yaml](../config/companies.yaml). A "followed" company is one listed there.

**Board, gaps.** The market in its parts on the brief's page: the broad market, the 11 sectors, four industries, and commodities, rates, the dollar and Bitcoin ([prices/board.go](../internal/prices/board.go)). A gap is one fund's move less another's, such as equal weight less the S&P 500. It is set against its usual size over the past year, and is unusual at one and a half times that or more. The unusual ones go in the lines "Stood out on the last session" and "Stood out over the past month" under the overview.

**Moves line.** The line under a section's heading. It shows the biggest moves of that section's shares on the day ([report/moves.go](../internal/report/moves.go)). The overview has one too, "Across the market". It shows the day's biggest moves among all US companies worth US$2bn or more, each against the share's usual ([movers.go](../internal/app/movers.go)).

**Industry group, peers.** The companies in the same industry as Nasdaq's list files them, worth US$500m or more. `/analyse` and the verdicts set a company's growth, margins and multiples beside theirs, each on its latest twelve months filed with the SEC ([peers.go](../internal/fundamentals/peers.go)).

**Movers.** Followed shares that moved at least 3 percentage points more than the S&P 500 fund, up or down. Up to 5 of them get a news search of their own, asking why they moved ([movers.go](../internal/app/movers.go)).

**Covered, repeat.** A story that an earlier brief already carried. It is marked rather than dropped, so the brief reports it only as an update. The record is kept in `covered.json` ([history.go](../internal/history/history.go)).

**New names in the news.** Companies that the day's stories were about but that no sector follows. Each ticker is checked against its exchange before it is shown ([discover](../internal/discover/)).

**Worth a closer look, the closer look, recommendations.** The message sent straight after the brief. In the channel it is part of the same post as the brief. It gives BUY, HOLD or SELL verdicts on companies that no sector follows. It has two parts, *themes* (weekly) and *reactions* (daily). [`sendIdeas`](../internal/app/ideas.go#L88) sends it.

**Theme.** A group of companies with one story behind them, such as "AI memory demand". Each week there are up to 3 **popular** themes, which Opus sorts out of the market's leaders. There are also up to 2 **early** themes, which Opus finds with the web among industries that are starting to turn. [`runThemes`](../internal/app/themes.go#L72) runs them.

**Pick.** A company that a theme's research proposed and that was then judged. Only BUYs and SELLs are shown, up to 10 a week.

**Reaction.** A share that moved at least 3 times its *usual daily move*, on at least twice its usual trading, where the brief's articles explain why. Its verdict asks whether the market over- or under-reacted. There are up to 3 a day. [`runReactions`](../internal/app/reactions.go#L68) finds them.

**Earlier picks.** The theme picks from the last 8 weeks. They are listed under the week's themes, with how each has done since.

**Owner.** The Telegram chat the brief goes to. It is the only chat that can use every command. It is set by `TELEGRAM_MASTER_CHAT_ID`, or else it is the first chat to send `/start`. The owner can name other chats too. Chats in `TELEGRAM_COMMAND_CHATS` may use the commands that only answer questions. Chats in `TELEGRAM_CONTROL_CHATS` may also use `/now`, `/watchlist` and `/sources`. The bot ignores commands from any other chat.

**Channel.** A Telegram channel that other people can read but not write in (`TELEGRAM_CHANNEL_ID`). The daily brief and its closer look are posted there. `/share` posts the last brief or analysis there too.

**Channel note, `channelNote`.** The warning at the top of the channel's closer look. It says that a model wrote it, that nobody checked it, and that it is not advice. Verdicts may go to the channel only because this note is there. [telegram/ideas.go](../internal/telegram/ideas.go#L31).

---

**Coming up.** The block under the overview. It lists what is due from now to the end of the week. Economic releases come from ForexFactory's calendar, with their time, forecast and previous figure (🔴 high impact, 🟠 medium). Company results come from Nasdaq's earnings calendar, with what analysts expect a share to earn (⭐ marks a company you follow). The overview's last sub-heading, **What to watch**, says which of these matter and why ([app/calendar.go](../internal/app/calendar.go)).

**Linked terms.** Finance words in the brief and in /analyse. The first time one appears in a section of the brief, or anywhere in an analysis, it links to an explanation on Investopedia, Wikipedia or Corporate Finance Institute. Neither the brief nor the analysis defines these words, so the link is the definition. Each also lists the other terms it used, and those the list lacks link to a Google search for their meaning. A term two reports have listed, and a check has passed, is **learned**: linked wherever it appears from then on, and added to the list by the sync. The list is in [config/glossary.yaml](../config/glossary.yaml), and each link in it has been checked.

**`/analyse`.** The command that writes up one company. It covers what the company does, where it is heading, whether its figures hold up, and the case for and against it. A short verdict comes last.

**`/industry`.** The command that explains how an industry fits together, part by part. It also covers what people are saying about the industry, what is coming, and what follows from it. It ends with listed companies to look into in each part ([industry.go](../internal/industry/industry.go)).

---

## Market measures

**S&P 500, SPY, the index, the benchmark.** The S&P 500 is the 500 largest US companies. SPY is the fund that tracks it. Every return here is measured against SPY's.

**Points, "+59 pts".** Percentage points better or worse than SPY over the same stretch. "+59 pts over 12 months" means the share's 12-month return was 59 percentage points above SPY's.

**Session.** One trading day.

**12-1.** The return over the last 12 months, *leaving out* the latest month. A share's latest month tends to reverse, so it is taken out.

**2y.** The return over about two years. In practice it is 23 months (`TwoYears - Month` sessions), because the stored history is a few days short of two full years.

**50-day and 200-day average.** The average closing price over the last 50 or 200 sessions. A share above its 200-day average is usually taken to be in an uptrend.

**Breadth.** The share of an industry's companies that are above their 200-day average. The test for early industries uses breadth at the 50-day average instead. It compares how many are above their 50-day average now with a month ago.

**Usual daily move.** The average size of a share's daily move, up or down, over the last 60 sessions ([`Series.Usual`](../internal/market/panel.go#L183)). "7.1 times usual" means today's move was 7.1 times that size.

**Value traded, trading.** The number of shares traded times the price, in dollars. "2.3 times the trading" means 2.3 times the value it trades on a usual day.

**Eligible.** The bar a US company must clear to be considered at all. It must be worth at least US$2bn, cost at least US$5 a share, trade at least US$20m a day, and have 13 months of history (`DefaultRules` in [screen.go](../internal/market/screen.go)). Funds, blank-cheque companies, warrants and preferred shares are left out.

**Leaders.** The 150 eligible companies that rank best on four measures. The measures are the 2y return, the 12-1 return, the 6-month return, and the year's return for each unit of the share's swing. Each leader must also be above its 200-day average. It must not have made most of its year's rise in one day, and it must not be pinned to a takeover offer. [`Leaders`](../internal/market/screen.go#L167).

**Industry.** Nasdaq's industry label for a company, such as "Semiconductors". It is read from Nasdaq's list of every US listing ([consensus/listings.go](../internal/consensus/listings.go)).

**Popular industry.** An industry whose shares have already risen. It is ranked on four things. These are its typical member's 6- and 12-month return against SPY, its breadth, how much more money is flowing in than a year ago, and how often its companies are named in recent headlines. [`Industries`](../internal/market/screen.go#L267).

**Early industry.** An industry that is starting to turn while its year still lags. Three tests must all pass. Its 12-month return is behind the typical industry's. Its 3-month return is ahead of the typical industry's. And its breadth at the 50-day average rose by more than the typical industry's did. "Typical" means the median industry, so the test still works in a month when the whole market falls.

**Named in N recent headlines, mentions.** How many of the last three weeks' brief headlines name one of the industry's companies ([`Mentions`](../internal/market/names.go#L77)).

**Multiples.** P/E is the price against earnings. P/S is the price against sales. EV/EBIT is the value of the whole business against its operating profit. A pick's multiples are compared with its theme's median and with its own last five years.

**P/S per growth point.** Price to sales divided by revenue growth in percent. It shows what a share costs against how fast its sales are growing.

**Warning signs.** Any of these four. The share is more than 40% above its 200-day average. It is above the analysts' average price target. Insiders sold a net 0.25% or more of the company in three months. Or 10% or more of the shares are sold short. [`WarningSigns`](../internal/ideas/valuation.go#L188).

---

## Verdicts and the scorecard

**Verdict: BUY, HOLD, SELL.** BUY means the share should beat SPY by at least 5 points over 12 months, in US dollars. SELL means it should trail SPY by at least that much. HOLD is anything in between. The closer look does not show HOLDs.

**Confidence.** High, medium or low. The model gives it. The code caps it at low when no accounts were read. That covers every Singapore company, and anything that does not file with the SEC.

**Buy closed, overruled.** The code's valuation rules, which the model cannot bend ([`BuyClosed`](../internal/ideas/valuation.go#L109), [`hold`](../internal/ideas/judge.go#L139)). A BUY becomes a HOLD, with the reason shown, in either of two cases:
- it costs more than its theme on every multiple, and its growth doesn't make up for it;
- it costs more than its theme on every multiple, and it shows 2 or more warning signs.

**Catalyst, sensitivity, case, risk.** Parts of a verdict:
- catalyst: something coming that could move the share;
- sensitivity: how much the earnings swing with the business's main driver;
- case: the argument;
- risk: the single biggest risk.

**Scorecard.** Every BUY and SELL shown, and every `/analyse` verdict, with how each has done against SPY ([scorecard.go](../internal/ideas/scorecard.go)). It is kept in `scorecard.json`. It was wiped clean on 27 September 2026, so it starts from the run of 28 September.

**Entry.** The price a verdict is measured from. It is the opening price of the next session after the verdict, for the share and for SPY alike. Using a later price would let the verdict see the future.

**Clearly.** The bar a verdict must clear to count as right. It is 5 points a year against SPY, scaled to the time passed, so about 0.4 points after a month (`Clearly` in [scorecard.go](../internal/ideas/scorecard.go)). A verdict is graded once it is 7 days old (`MinAge`).

**Checked, one source only.** The line in a verdict that names the independent sources its case was checked in. A verdict must find the claim it rests on in two sources. If it finds only one, it says "one source only", and the code holds it to low confidence ([`hold`](../internal/ideas/judge.go#L139)).

**Source.** How a verdict was found. It is `theme`, `reaction`, `analysis` (from `/analyse`), or `news` (the old closer look, before 27 September 2026).

**8-week no-repeat.** A company that was given the same verdict in the last 8 weeks is not shown again as a pick. It appears under earlier picks instead (`RepeatWindow`).

---

## Filings and where the data comes from

**SEC, EDGAR.** The SEC is the US securities regulator. EDGAR is its free database of filings.

**8-K.** A US company's report of an important event. Item 2.02 is results, and the press release is usually attached as exhibit 99.1.

**10-K, 20-F.** The annual report. A US company files a 10-K. A foreign company with a US listing files a 20-F.

**6-K.** A foreign company's report of news. It is the foreign version of the 8-K.

**XBRL.** The tagged data format that filings are made in. The accounts in `/analyse` and in the verdicts are read from it ([fundamentals](../internal/fundamentals/)).

**Quarter, TTM.** A company's latest quarters, each three months on its own, read from its filings for `/analyse`. TTM means the trailing twelve months, which is the last four quarters added together ([quarters.go](../internal/fundamentals/quarters.go)).

**CIK.** The SEC's number for a company.

**OpenFIGI, FIGI.** A free registry of securities. Every ticker shown to a reader is checked against it first. So the service never shows a symbol it cannot confirm.

**Consensus.** What analysts expect. It covers forecasts and how they have changed, price targets, ratings, and results against forecasts. It also covers insider trades, short interest and fund holdings. All of it is read from the data behind Nasdaq's website ([consensus](../internal/consensus/)).

**Massive.** The service that gives the daily bar of every US listing for a date: open, high, low, close and volume. The free plan allows 5 requests a minute and goes 2 years back. It fills the **market store** ([prices/massive.go](../internal/prices/massive.go)).

**Finnhub.** Live share prices and company news ([prices/prices.go](../internal/prices/prices.go)).

**Chart source.** Yahoo's daily price data, which needs no key. It covers listings outside the US, in their own currency, and fills in what Finnhub misses ([prices/history.go](../internal/prices/history.go)).

**FRED.** The St. Louis Fed's data: bond yields, the Fed's rate, the VIX, inflation, oil, copper and the dollar ([prices/fred.go](../internal/prices/fred.go)).

**Backdrop.** The FRED figures shown above every verdict and analysis.

**Tavily.** A news search engine, billed in credits ([search](../internal/search/)). The brief runs about 20 searches beside the feeds. The closer look runs 1 for each company it judges, and `/analyse` runs 2. The models' own web searches are separate. They are Claude's, and they don't use Tavily credits.

**STI.** Singapore's Straits Times Index. Its 30 companies are scored beside the US market's, in dollars ([config/singapore.yaml](../config/singapore.yaml)).

---

## Inside the code

**The relay.** The only way the app calls a model. Each call is a request file. A fresh `claude -p` answers it, running without a screen on the owner's Claude subscription, and writes the reply beside it. There is no API key. [relay](../internal/relay/).

**Stage.** The name of a kind of model call. It fixes which model answers and whether it may use the web. The stages are `triage`, `review`, `brief`, `names`, `themes`, `scout`, `research`, `verdicts`, `analysis` and `industry`. There is a table in [FUNCTIONS.md, section 12](FUNCTIONS.md#12-model-calls-the-relay).

**Run, run folder, ledger.** A run is one brief, closer look or analysis. All its model calls sit in one folder, `data/relay/<time>-<kind>/`. The ledger is a checklist of those calls.

**Run cache.** The latest run of each kind (`brief`, `analysis`, `recommendations`), with each step's data saved as JSON in `data/cache/<kind>/`. It is the first place to look when judging a run ([runcache](../internal/runcache/)).

**Triage, sorting.** Opus rates every article from 1 to 5 and places it in up to 2 sectors ([triage.go](../internal/triage/triage.go)).

**Review.** A second Opus pass, which may use the web. It moves articles that were placed in the wrong sector ([review.go](../internal/triage/review.go)).

**Top-up, reserve.** Articles rated 3 are kept "in reserve". A section with fewer than 10 articles is offered them, but only where the review agrees ([topup.go](../internal/triage/topup.go)).

**Prefs, edits.** `prefs.yaml` on the server. It holds the owner's chat, the last brief's message ids, and the watchlist and feed changes made from Telegram, which are called "edits".

**Fold.** Writing those edits into the files in `config/`. The `--fold` flag does it, and `scripts/sync-from-fly.sh` runs it.

**Data volume, data folder.** Where everything the service keeps is stored: `/data` on Fly, and `./data` on your machine (not in git). See the table in [ARCHITECTURE.md, "What lives on disk"](ARCHITECTURE.md#what-lives-on-disk).

**Look.** What the closer look starts from: the brief's articles, and whether this is the scheduled run ([ideas.go](../internal/app/ideas.go)). Until 2026-10-01 it waited 20 minutes after the brief, in `pending-look.json`. That wait is gone.

**Question, answer.** A command that asks for what it needs. For example, `/analyse` on its own asks which company, and the chat's next message within 10 minutes is taken as the ticker. If the question goes unanswered, or another command comes first, the bot deletes the question ([ask.go](../internal/app/ask.go)).

**Market store, panel, series.** The market store is the two years of daily bars on disk, one file for each session. A panel is a stretch of them loaded into memory. A series is one share's row in it ([market](../internal/market/)).

**Fly, machine.** Fly is the hosting service. The machine is the single small server the app runs on (`joseph-market-watch`, in Singapore).

---

**Start here:** [1 README](../README.md) → **2 Glossary** → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → [5 Reviewing the code](REVIEW.md) → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

**Next:** [3 How it works](ARCHITECTURE.md). Read its first three parts to see the whole system as the story of one day.
