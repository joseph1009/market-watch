# Backlog

What is agreed but not built. Each entry says what it is, what it needs from
you, and why it is worth doing.

## Waiting on you

- **Deploy it.** `Dockerfile` and `fly.toml` are written but **unverified**: the
  Docker daemon was not running here, so the image has never been built. Steps:
  1. `fly launch --no-deploy` (or `fly apps create market-watch`)
  2. `fly volumes create market_watch_data --size 1 --region sin`
  3. `fly secrets set TELEGRAM_BOT_TOKEN=... ANTHROPIC_API_KEY=... FRED_API_KEY=... FINNHUB_API_KEY=... TELEGRAM_CHAT_ID=509509492 USER_AGENT="Market Watch you@example.com"`
  4. `fly deploy`
  Expect the first build to need a fix or two, since it has not been run.
- **Push the repository.** Every commit is still local only.

## Reliability

- **Weekly source health.** A failed brief now messages you, and `/stats` names
  feeds that fail regularly, but nothing volunteers it: you have to ask. A
  weekly message would close that.
- **`marketwatch-top` returns HTTP 400.** One feed of 44, failing consistently.
  Worth replacing or turning off.

## Brief quality

- **Prices outside the US.** Both free tiers turned out to be US-only: Twelve
  Data answers London with "available starting with the Grow plan" and does not
  resolve Hong Kong or Tokyo at all. A company quoted elsewhere therefore has no
  move in the brief. Fixing it means a paid plan (Twelve Data Grow, EODHD, or
  similar at roughly $20-80 a month).
- **Promotion.** A command to move a name from "new names in the news" straight
  into a watchlist. `/watchlist add <group> <ticker>` already does the work; this
  would just save the typing.

## /analyse — further

- **Share price in the analysis.** Now possible: with a Finnhub key the analysis
  could carry price against earnings and book value. It currently refuses
  valuation outright, which was right when it had no price and is no longer.
- **Companies with no US listing.** The analysis reads SEC filings, so Tencent,
  Keyence and anything without a US listing are not covered. Japan's EDINET is a
  free XBRL API and would cover Tokyo; Hong Kong and mainland Europe need a paid
  vendor.

## Done

- Triage: a small model rates and places every article before the cap.
- Market levels from FRED: yields, the curve, fed funds, S&P 500, VIX.
- `SOURCE_LINKS=off|short|full`.
- The brief written for a non-specialist, with terms explained, in bullets.
- `/analyse <ticker>`: SEC filings read and written up, agentic, any SEC filer
  including foreign ones with a US listing, in their own currency, with the
  current year so far beside the full years.
- Citations: every claim carries a link to the article behind it.
- Repeats: stories earlier briefs carried are marked, not reported again.
- Weekends: no brief on days the market was shut.
- New names in the news, with every ticker checked against the exchange.
- Share prices: benchmark funds and the companies in today's news.
- Failure alerts: a brief that fails says so in the chat.
- `/stats`: what recent briefs cost and did, kept on the data volume.
- CI: gofmt, vet, tests and build on every push.
