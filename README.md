# Market Watch

A daily brief on the US stock market, written by Claude and delivered on
Telegram the next morning, Singapore time, with a link behind every claim.

Each evening, US time, it reads about forty news feeds and runs about fifteen
news searches. It also reads the SEC's recent filings and prices every company
it follows. A model sorts the day's articles into the reader's sectors, and a
second pass checks where each one landed. Opus then writes the brief, section
by section, from what survived. Between briefs the bot answers commands, the
largest of which reads a company's filed accounts and writes them up.

Every model call goes through Claude Code, run headless on the machine, on a
Claude subscription. There is no API key and no API spend.

## Where things are

| | |
|---|---|
| [config/](config/) | What it follows and how it talks to the models, in files you edit without Go: [sectors.yaml](config/sectors.yaml), [companies.yaml](config/companies.yaml), [sources.yaml](config/sources.yaml), [prompts.md](config/prompts.md) and [method.md](config/method.md) |
| [cmd/market-watch/](cmd/market-watch/) | The program |
| [internal/](internal/) | Everything else, one package per job |
| [scripts/](scripts/) | Deploying to Fly, and syncing its state back down |
| [docs/](docs/) | [ARCHITECTURE.md](docs/ARCHITECTURE.md), how the code fits together; [RUNBOOK.md](docs/RUNBOOK.md), how to run and change it; [TASKS.md](docs/TASKS.md), what is not built yet |

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
