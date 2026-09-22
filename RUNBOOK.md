# Running the brief and the analysis

Every model call the service makes goes through the **relay**. The call is
written to a file, answered, and the answer written beside it, with a ledger
saying what was asked and what came back. There is no other path: no API key,
no direct API calls.

Who answers is set by `RELAY_ANSWER`:

| `RELAY_ANSWER` | Who answers | Waits for a person | Used for |
|---|---|---|---|
| `claude` (default) | Claude Code, run headless, one fresh process per call | No | The service as deployed, and any run from a terminal |
| `session` | Whoever writes the reply file, usually a Claude Code session using subagents | Yes | A run you want to watch or answer yourself |

Both draw on your Claude subscription, not on API credit.

A **pre-written run** hands the delivery a reply written earlier, asks nothing
and waits for nothing. It exists to check formatting and delivery.

## The stages

A brief makes three kinds of call, and an analysis makes one:

| Stage | What it does | Prompt | Default model | Override | Reply format |
|---|---|---|---|---|---|
| `triage` | Rates every article 1-5 and places it in up to two watchlists | `triage.system` | Haiku | `MODEL_TRIAGE` | `number\|rating\|watchlist ids` |
| `brief` | Writes the brief | `brief.system` | Opus | `MODEL_BRIEF` | `## OVERVIEW` then `## SECTION: <id>` blocks |
| `names` | Names the companies the day was about that no watchlist tracks | `discover.system` | Haiku | `MODEL_NAMES` | `name\|ticker\|exchange\|article numbers\|what happened` |
| `analysis` | Writes up one company's accounts for `/analyse` | `analysis.system`, the method, and `analysis.related` | Opus | `MODEL_ANALYSIS` | Plain text with capitalised headings |

Sorting sends the day's articles in batches of 60, two at a time
(`RELAY_CONCURRENCY`). When a person is answering, the batches are 150 each, so
there are fewer files to deal with.

## The prompts

Every instruction sent to a model is in
[internal/prompts/prompts.md](internal/prompts/prompts.md), one section per
`=== id ===` line. The analysis also carries
[internal/fundamentals/method.md](internal/fundamentals/method.md), the
playbook for reading accounts.

Editing the prose needs no Go. What has to survive an edit are the markers the
replies are parsed by: `## OVERVIEW`, `## SECTION:`, the pipe-delimited lines,
the rating format. The loader checks for them at startup and refuses to run
without them, so a broken prompt fails at once, not at delivery.

`PROMPT_FILE=/path/to/prompts.md` uses another copy without rebuilding.

To see exactly what a stage was sent, open its request file in the run
directory. It is the whole call: the `===== SYSTEM =====` block, then the
`===== PROMPT =====` block.

## Where a run's files go

Each brief and each analysis gets its own directory under `RELAY_DIR`
(`DATA_DIR/relay` by default). The last forty are kept.

```
data/relay/20260922-203000-brief/
  ledger.md               what was asked, what was answered, by which model, how big
  01-triage-request.txt   a call
  01-triage-reply.txt     its answer
  ...
  05-brief-request.txt
  05-brief-reply.txt
  06-names-request.txt
  06-names-reply.txt
data/relay/20260922-211500-analysis-mu/
  ledger.md
  01-analysis-request.txt
  01-analysis-reply.txt
```

In the ledger, `- [ ]` is a call waiting for an answer, `- [x]` one answered,
and `- [!]` one that failed or was abandoned, with the reason.

## Running one by hand

A brief, answered by Claude Code, sent to the chat:

```
go run ./cmd/market-watch --once
```

The same from the tests, which is also how an analysis is run:

```
LIVE_BRIEF=1 go test ./internal/app -run TestLiveBrief -v -timeout 1h
LIVE_ANALYSIS=MU go test ./internal/app -run TestLiveAnalysis -v -timeout 1h
```

`go run ./cmd/market-watch --check` confirms the bot, the feeds and that Claude
Code is installed, and shows which model will answer each stage. It asks no
model anything.

## Answering a run yourself

Set `RELAY_ANSWER=session` and each call waits for its reply file:

```
LIVE_BRIEF=1 RELAY_ANSWER=session go test ./internal/app -run TestLiveBrief -v -timeout 3h
```

Add `RELAY_AT=08:30` for a **prepared run**: started the night before, it
holds until 08:30, collects the news, and leaves its requests waiting for the
next time a session is open. Set `-timeout` to cover the hold.

The rule: **the answering session never reads a large request itself.** A day's
sorting is 40KB a batch, and the brief's request is the largest of all. Read
those in the main session and there is no room left for the work.

1. Read `ledger.md` in the newest run directory. It says which stages are
   waiting and how large each is.
2. For each waiting request, spawn one subagent with the model that stage
   would get (Haiku to sort and spot names, Opus to write), told to:
   - read that one request file and nothing else,
   - write the reply file in the format the request's own `===== SYSTEM =====`
     block asks for,
   - append one line to `ledger.md` saying what it did, and
   - report back a single line.
3. Batches can be answered in parallel; they are independent.
4. When a stage's reply lands, the run carries on by itself within two seconds.

The reply format is defined in each request's own system block, which is why a
subagent needs no other context.

### When the session's context is compacted

It will be, on a long run. The design assumes it:

- **Everything is on disk.** Each request and reply is a file, written before
  the next stage starts. Compaction loses the reasoning, never the files.
- **The ledger is the memory.** After a compaction, an interruption or a
  restart, read `ledger.md`.
- **Digests, not transcripts.** A subagent that has just rated 150 headlines
  should leave one line behind, such as "rated 150, 12 at 4 or higher, mostly
  Fed and oil", not a summary of each.
- **Compact between stages**, not during one. The gap after a reply is written
  and before the next request appears is the safe moment.

If the answering session dies, nothing is lost but time: the Go process is
still waiting on its reply file. Open a new session, read the ledger, and carry
on.

## A pre-written run

```
CANNED_REPLY=analysis.txt FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestCannedAnalysis -v
```

This sends an analysis written earlier through the real `/analyse` delivery:
real filings, prices, rendering and chat, and no model call.

## Logging in

On a desktop where you have run `/login` in Claude Code, nothing more is
needed. On a server there is nobody to log in, so:

1. On your own machine, run `claude setup-token`. It opens the browser, then
   prints a token that lasts a year.
2. Set it as `CLAUDE_CODE_OAUTH_TOKEN` where the service runs.

The token uses your subscription, and calls count against your plan's usage
limits, the same ones your own Claude Code use draws on. It is for your use
only. The bot answers only the chat that registered it, so nobody else's
messages can use it.

`ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` are never passed to Claude Code,
even when they are set. Claude Code would otherwise prefer them to the
subscription.

## A channel for other readers

Other people can read the brief in a Telegram channel. The bot posts there;
readers can only read. It still takes commands from your chat alone, so the
channel shares the brief and nothing else.

What goes to the channel:

- **The daily brief**, by itself, as soon as it reaches you. `--once --share`
  does the same for a brief run by hand.
- **`/share`**, whichever of your brief or analysis arrived last. A brief from
  `/now` or `--once`, and every `/analyse`, stays in your chat until you share
  it, so you can read it first. The bot remembers only the latest one, and
  forgets it on a restart.

In the channel, only the first message of each post makes a sound; the rest
arrive silently. Nothing is deleted there: `REPLACE_PREVIOUS` clears your chat
only, and readers keep every day's brief.

Readers cannot ask the bot for anything, and that is deliberate. Every brief
and analysis runs on your Claude subscription, and Anthropic's terms do not
allow routing other people's requests through a Pro or Max plan. A brief that
was written for you and then posted is your own use. An analysis someone else
asked for would not be. If readers ever need to run their own, that needs an
API key, paid per call.

Setting one up:

1. Create the channel in Telegram. Private, with an invite link, is simplest.
2. Add the bot as an admin with the right to post messages.
3. Find the channel's id. Open the channel at <https://web.telegram.org/a/>:
   the address ends in `#-100` and a string of digits, and that whole number is
   the id. If it does not start with `-100`, put `-100` in front of the digits.
4. Put it in `.env` as `TELEGRAM_CHANNEL_ID=-100...`, and on Fly run
   `scripts/fly-deploy.sh` again, which sends it as a secret.
5. `--check` says `channel ok` with the channel's title, or says what is
   missing.

## Deploying to Fly

The image carries Claude Code, installed from Anthropic's signed apt
repository, and runs every call through it as on a desktop. It logs in with
`CLAUDE_CODE_OAUTH_TOKEN`.

1. Install flyctl and log in: `fly auth login`.
2. On your own machine, run `claude setup-token` and put the token in `.env` as
   `CLAUDE_CODE_OAUTH_TOKEN=...`.
3. Make sure `.env` has `TELEGRAM_CHAT_ID`. It pins the one chat the bot will
   answer; without it, whoever sends `/start` first becomes the owner.
4. Stop any copy of the service running elsewhere. Two copies polling one bot
   token fight over every message, and two schedulers send two briefs. A local
   `--once` or `LIVE_BRIEF` run is fine; `market-watch` left running is not,
   and neither is a container left over from testing the image: `docker ps`
   shows one. On the server, `telegram poll failed ... 409 Conflict` in the
   logs every ten seconds means another copy is running somewhere.
5. From the repository root, run `scripts/fly-deploy.sh`. It creates the app and
   its volume if they are missing, sets the secrets from `.env` without printing
   them, deploys, and runs `--check` on the machine.

Deploying again later is the same script.

### Moving the data up

The volume starts empty. The service seeds its own watchlists, but it knows
nothing of what earlier briefs covered, so its first brief may repeat them. To
carry the local record over:

```
fly ssh sftp shell -a joseph-market-watch
put data/prefs.yaml /data/prefs.yaml
put data/covered.json /data/covered.json
put data/runs.json /data/runs.json
put data/candidates.json /data/candidates.json
```

Then give the files to the service's user and restart:

```
fly ssh console -a joseph-market-watch -C "chown 65532:65532 /data/prefs.yaml /data/covered.json /data/runs.json /data/candidates.json"
fly apps restart joseph-market-watch
```

### Looking at a run on the server

```
fly ssh console -a joseph-market-watch -C "ls /data/relay"
fly ssh console -a joseph-market-watch -C "cat /data/relay/<run>/ledger.md"
fly logs -a joseph-market-watch
```
