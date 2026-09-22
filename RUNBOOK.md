# Running the brief and the analysis

Three ways a run can get its words. The difference is who writes them and
whether the run waits.

| | Who writes | Costs | Unattended |
|---|---|---|---|
| **Keyed run** | Claude, through the API, using `ANTHROPIC_API_KEY` | API credits | Yes |
| **Relay run** | Claude Code, answering files while the run waits | Nothing | No |
| **Pre-written run** | Nobody: a reply written earlier is handed over | Nothing | Yes, but fixed |

A keyed run is the bot as deployed. A relay run is the same pipeline with the
model calls answered by hand. A pre-written run asks nothing and waits for
nothing, and exists to test the plumbing: formatting, message splitting,
delivery.

## Starting a relay run

The brief, with all three of its model calls relayed:

```
RELAY_DIR=$TEMP/relay-brief go test ./internal/app -run TestRelayBrief -v -timeout 3h
```

One company's analysis:

```
RELAY_DIR=$TEMP/relay-mu FUNDAMENTALS_TICKER=MU go test ./internal/app -run TestRelayAnalysis -v -timeout 2h
```

Add `RELAY_AT=08:30` to hold the run until that time before it collects
anything — a **prepared run**, started the night before, whose requests are
waiting when the answering session next opens. The run holds for as long as its
`-timeout` allows, so set that generously.

Each run writes into its directory:

```
ledger.md                what was asked, what was answered, how big each was
01-triage-request.txt    a model call, waiting for an answer
01-triage-reply.txt      the answer; the run picks it up within two seconds
```

The stages are `triage` (sorting the day's articles), `brief` (writing it),
`names` (spotting new companies), and `analysis` for a company.

## Answering a relay run

The rule: **the answering session never reads a large request itself.** A day's
sorting is 40KB a batch, and the brief prompt is the largest single request even
after the caps in `internal/report/prompt.go` cut the general block and the
longest sections. Read those in the main session and there is no room left for
the work.

1. Read `ledger.md`. It says which stages are waiting and how large each is.
2. For each waiting request, spawn one subagent, told to:
   - read that one request file and nothing else,
   - write the reply file in the format the request's own instructions ask for,
   - append one line to `ledger.md` saying what it did, and
   - report back a single line.
3. Batches can be answered in parallel; they are independent.
4. When a stage's reply lands, the run continues on its own.

The reply formats are defined in the request's own `===== SYSTEM =====` block,
which is why a subagent needs no other context. In short: sorting replies are
`number|rating|watchlist ids` lines; the brief is `## OVERVIEW` and
`## SECTION: <id>` blocks; new names are
`name|ticker|exchange|article numbers|what happened` lines.

## When context is compacted

It will be, on a long run. The design assumes it:

- **Everything is on disk.** Each request and reply is a file, written before
  the next stage starts. Compaction loses the reasoning, never the artifacts.
- **The ledger is the memory.** After a compaction, an interruption or a
  restart, read `ledger.md`: `- [ ]` is a stage still waiting, `- [x]` one
  already answered, `- [!]` one abandoned when a run gave up.
- **Digests, not transcripts.** A subagent that has just rated 150 headlines
  should leave one line behind — "rated 150, 12 at 4 or higher, mostly Fed and
  oil" — not a summary of each.
- **Compact deliberately between stages**, not during one. The gap after a
  reply is written and before the next request appears is the safe moment.

If the answering session dies entirely, nothing is lost but time: the Go
process is still waiting on its reply file. Open a new session, read the
ledger, and carry on. If the process itself died, the finished replies are
still there for the next run to reuse.

## The prompts

Every instruction sent to a model is in
[internal/prompts/prompts.md](internal/prompts/prompts.md), one section per
`=== id ===` line, plus [method.md](internal/fundamentals/method.md), which
stays separate because it is written as an Agent Skill.

Editing prose there needs no Go. What must survive an edit is the markers the
replies are parsed by — `## OVERVIEW`, `## SECTION:`, the pipe-delimited lines,
the rating format. The loader checks for them at startup and refuses to run
without them, so a broken prompt fails immediately rather than at delivery.

`PROMPT_FILE=/path/to/prompts.md` uses another copy without rebuilding.
