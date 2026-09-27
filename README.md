# Market Watch

A daily brief on the US stock market, written by Claude and delivered on
Telegram two hours before the US open -- the evening in Singapore -- with a
link behind every claim.

Each morning, US time, it reads about forty news feeds and runs about fifteen
news searches. It also reads the SEC's recent filings and prices every company
it follows. A model sorts the day's articles into the reader's sectors, and a
second pass checks where each one landed. Opus then writes the brief, section
by section, from what survived. Half an hour later it sends a closer look,
each company with a buy, hold or sell verdict: on Mondays up to ten found from
two years of the whole market's prices -- under the themes the market has been
paying for, and the industries growing before their shares have followed --
and every day up to three shares that moved far beyond their usual on the
news. Between briefs the bot answers commands, the largest
of which reads a company's filed accounts, results and analysts' expectations
and writes them up.

Every model call goes through Claude Code, run headless on the machine, on a
Claude subscription. There is no API key and no API spend.

## Start here

New to this, or back after long enough to have forgotten it? Read these in
order. Every page carries this path at its top and bottom, with the page you
are on in bold, so you can always click on to the next.

| Step | Read | What you will know after it |
|---|---|---|
| 1 | This page | What the service does, and where the files are |
| 2 | [Glossary](docs/GLOSSARY.md) | The words: brief, closer look, theme, reaction, verdict, scorecard, relay, and the market measures |
| 3 | [How it works](docs/ARCHITECTURE.md): its first three parts | The whole system as the story of one day |
| 4 | [Function by function](docs/FUNCTIONS.md) | For each feature: what starts it, its code in the order it runs, and what it feeds or reads |
| 5 | [Reviewing the code](docs/REVIEW.md) | How to review it: see real output, read the code in a set order, check the rules that matter most |
| 6 | [Running it](docs/RUNBOOK.md) | Operating it, changing its prompts and lists, deploying |
| 7 | [Backlog](docs/TASKS.md) | What isn't built yet, and what's known to be weak |

Steps 1 to 4 take about an hour, and give you the whole flow. Step 5 is the
code review itself. Resuming work with Claude Code? It keeps `HANDOVER.md` at
the repository root, on your machine only and not in git, with where the work
stands and what comes next.

## Where things are

| | |
|---|---|
| [config/](config/) | What it follows and how it talks to the models, in files you edit without Go: [sectors.yaml](config/sectors.yaml), [companies.yaml](config/companies.yaml), [sources.yaml](config/sources.yaml), [prompts.md](config/prompts.md) and [method.md](config/method.md) |
| [cmd/market-watch/](cmd/market-watch/) | The program |
| [internal/](internal/) | Everything else, one package per job |
| [scripts/](scripts/) | Deploying to Fly, and syncing its state and its latest runs' data back down |
| [docs/](docs/) | The pages in [Start here](#start-here): [GLOSSARY.md](docs/GLOSSARY.md), the words; [ARCHITECTURE.md](docs/ARCHITECTURE.md), how the code fits together; [FUNCTIONS.md](docs/FUNCTIONS.md), each feature's entry point and sequence; [REVIEW.md](docs/REVIEW.md), how to review it; [RUNBOOK.md](docs/RUNBOOK.md), how to run and change it; [TASKS.md](docs/TASKS.md), what is not built yet |

## Running it

Copy `.env.example` to `.env` and fill in what it asks for. The only required
setting is a Telegram bot token, and every optional key turns on one feature.
Then:

```
go run ./cmd/market-watch --check    # confirm the settings, spending nothing
go run ./cmd/market-watch --once     # send one brief now
go run ./cmd/market-watch            # run the schedule and the bot
```

It runs as a long-lived service on Fly.io, deployed with
`scripts/fly-deploy.sh`. To change what the brief follows, edit the files in
`config/`, or use `/watchlist` in Telegram and bring those changes into the
files with `scripts/sync-from-fly.sh`. [RUNBOOK.md](docs/RUNBOOK.md) covers both.

---

**Start here:** **1 README** → [2 Glossary](docs/GLOSSARY.md) → [3 How it works](docs/ARCHITECTURE.md) → [4 Function by function](docs/FUNCTIONS.md) → [5 Reviewing the code](docs/REVIEW.md) → [6 Running it](docs/RUNBOOK.md) → [7 Backlog](docs/TASKS.md)

**Next:** [2 Glossary](docs/GLOSSARY.md): the words the rest of the pages use.
