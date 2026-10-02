# Market Watch

Market Watch is a daily brief on the US stock market. Claude writes it, and a
Telegram bot sends it at 07:30 New York time, two hours before the US market
opens. In Singapore that is the evening. Every claim in it links to its source.

## What it does

Each weekday morning, US time, it gathers the news. It reads about forty news
feeds, runs about fifteen news searches and reads the SEC's latest filings. It
also checks the price of every company it follows.

A model then sorts the day's articles into your sectors, and a second pass
checks where each one landed. Opus writes the brief from what is left, one
section at a time. The brief also looks ahead. It lists the economic releases
and company results due today and this week, and what is expected of them.

Straight after the brief comes the closer look. It names a few companies and
gives each a buy, hold or sell verdict. On Mondays it picks up to ten. It finds
them in two years of prices for the whole market, under two kinds of theme: the
ones the market has been paying for, and industries that are growing before
their shares have caught up. Every weekday it also picks up to three shares
that moved far more than usual on the news.

Between briefs, the bot answers commands. The biggest one, `/analyse`, reads a
company's accounts, its latest quarters and results, and what analysts expect.
It then writes the case for and against the company. Another, `/industry`,
explains how a whole industry fits together, where it is heading, and which
companies to look into.

Every model call goes through Claude Code, which runs on the server without a
screen and uses a Claude subscription. There is no API key, so there is no API
bill.

## Start here

Are you new to this, or back after long enough to have forgotten it? Read
these pages in order. Each page shows this path at its top and bottom, with
the page you are on in bold, so you can always click through to the next one.

| Step | Read | What you will know after it |
|---|---|---|
| 1 | This page | What the service does, and where the files are |
| 2 | [Glossary](docs/GLOSSARY.md) | The words: brief, closer look, theme, reaction, verdict, scorecard, relay, and the market measures |
| 3 | [How it works](docs/ARCHITECTURE.md), its first three parts | The whole system, told as the story of one day |
| 4 | [Function by function](docs/FUNCTIONS.md) | For each feature: what starts it, its code in the order it runs, and what it feeds or reads |
| 5 | [Reviewing the code](docs/REVIEW.md) | How to review it: see real output, read the code in a set order, and check the rules that matter most |
| 6 | [Running it](docs/RUNBOOK.md) | How to operate it, change its prompts and lists, and deploy it |
| 7 | [Backlog](docs/TASKS.md) | What isn't built yet, and what is known to be weak |

Steps 1 to 4 take about an hour and give you the whole flow. Step 5 is the
code review itself.

Are you picking the work up again with Claude Code? It keeps a file called
`HANDOVER.md` at the root of the repository. That file says where the work
stands and what comes next. It lives on your machine only and is not in git.

## Where things are

| | |
|---|---|
| [config/](config/) | What it follows and how it talks to the models. You can edit these files without touching Go: [sectors.yaml](config/sectors.yaml), [companies.yaml](config/companies.yaml), [sources.yaml](config/sources.yaml), [prompts.md](config/prompts.md) and [method.md](config/method.md) |
| [cmd/market-watch/](cmd/market-watch/) | The program |
| [internal/](internal/) | Everything else, one package for each job |
| [scripts/](scripts/) | Deploying to Fly, and copying its state and its latest runs back to your machine |
| [docs/](docs/) | The pages in [Start here](#start-here). [GLOSSARY.md](docs/GLOSSARY.md) explains the words. [ARCHITECTURE.md](docs/ARCHITECTURE.md) explains how the code fits together. [FUNCTIONS.md](docs/FUNCTIONS.md) gives each feature's starting point and steps. [REVIEW.md](docs/REVIEW.md) says how to review it. [RUNBOOK.md](docs/RUNBOOK.md) says how to run and change it. [TASKS.md](docs/TASKS.md) lists what is not built yet |

## Running it

Copy `.env.example` to `.env` and fill in what it asks for. Only one setting
is required, the Telegram bot token. Each optional key turns on one feature.
Then run one of these:

```
go run ./cmd/market-watch --check    # check the settings without spending anything
go run ./cmd/market-watch --once     # send one brief now
go run ./cmd/market-watch            # run the schedule and the bot
```

It runs all the time as a service on Fly.io, and `scripts/fly-deploy.sh`
deploys it. You can change what the brief follows in two ways. You can edit
the files in `config/`. Or you can use `/watchlist` in Telegram, then copy
those changes into the files with `scripts/sync-from-fly.sh`.
[RUNBOOK.md](docs/RUNBOOK.md) covers both.

---

**Start here:** **1 README** → [2 Glossary](docs/GLOSSARY.md) → [3 How it works](docs/ARCHITECTURE.md) → [4 Function by function](docs/FUNCTIONS.md) → [5 Reviewing the code](docs/REVIEW.md) → [6 Running it](docs/RUNBOOK.md) → [7 Backlog](docs/TASKS.md)

**Next:** [2 Glossary](docs/GLOSSARY.md), the words the other pages use.
