# Backlog

What is agreed but not built. Each entry says what it is, what it needs from
you, and why it is worth doing.

## Waiting on you

- **Rotate the Telegram bot token and the Anthropic API key.** The bot token was
  printed in an error log earlier in development. Five minutes (BotFather
  `/revoke`, then the Anthropic Console), and only you can do it.
- **Two free price-data keys**, so share prices can go in the brief:
  - `finnhub.io` → *Get free API key* → `.env` as `FINNHUB_API_KEY=` (US prices)
  - `twelvedata.com` → free tier → `.env` as `TWELVEDATA_API_KEY=`
    (Hong Kong, Tokyo, London, Euronext, Singapore)
  I will test both for real coverage before committing to either.

## Phase 1 — share prices

Attach each watchlist company's move to its section, and index, sector and
commodity moves to the macro block. Needs the keys above. Covers your 93
tickers, using ADRs where a foreign company has one.

## Phase 2 — new names in the news

A section listing companies the day's news keeps mentioning that you do not
track. Extraction runs off triage, which already reads every article.

- Tickers verified against the SEC company file; anything unresolvable dropped.
- Bar: two independent outlets, or one article triage rated 4 or 5.
- Exchanges: US including ADRs, plus Hong Kong, Tokyo, London, Euronext and
  Singapore once the symbol lists are in.
- Private companies named and flagged as private, with listed companies exposed
  to them noted only where an article says so.
- Repeat tracking, so a name on its third day is visible.
- Framing is "new names in the news", never "buys": catalyst and price
  reaction, and you judge.

## Phase 3 — promotion

A bot command to move a suggested ticker into a watchlist, so good candidates
graduate into the tracked set.

## Reliability

- **Hosting.** The bot only runs while a Claude Code session is open, and has
  already missed three days that way. Needs a Dockerfile and a small host with a
  persistent volume for `data/`.
- **Failure alerts.** A failed scheduled brief only writes to a log nobody
  reads. It should message you, and so should a weekly note of feeds that
  errored or returned nothing.

## Brief quality

- **Stop repeating stories across days.** Articles up to seven days old are
  eligible and nothing records what earlier briefs covered, so a Friday story
  can be written up again on Monday as new. The database path in the config is
  defined but unused.
- **Citations.** Tag each claim with its article number, rendered as a link, so
  checking a statement is one tap instead of a hunt.
- **Weekend schedule.** Briefs fire on Saturday and Sunday, when markets are
  closed and the news is Friday's. Either skip, or make Sunday a weekly recap.

## /analyse — further

- **Companies with no US listing.** The analysis reads SEC filings, so Tencent,
  Keyence and anything without a US listing are not covered. Japan's EDINET is a
  free XBRL API and would cover Tokyo; Hong Kong and mainland Europe would need
  a paid vendor (EODHD or similar, roughly $20-80 a month).
- **Share price in the analysis.** With a price key, the valuation questions the
  analysis currently refuses could be answered as multiples of what is filed.
  Still not advice, but "earnings against price" would become available.

## Housekeeping

- **Persist the run statistics.** Triage numbers (`cut_rated_4_plus`,
  `trivial_removed`, `newly_matched`) only reach a terminal log, so trends are
  invisible. Write them to `data/` and add a `/stats` command.
- **CI.** A GitHub Actions workflow running the test suite on push.
- **Nothing is pushed.** Every commit so far is local only.

## Done

- Triage: a small model rates and places every article before the cap.
- Market levels from FRED: yields, the curve, fed funds, S&P 500 and VIX.
- `SOURCE_LINKS=off|short|full`.
- The brief written for a non-specialist, with terms explained.
- `/analyse <ticker>`: SEC filings read and written up, agentic, any SEC filer
  including foreign ones with a US listing, in their own currency, with the
  current year so far beside the full years.
