**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → **5 Reviewing the code** → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

---

# Reviewing the code

How to review this repository from nothing, in order:

1. Check it builds.
2. See what it produces.
3. Read the code in a set order.
4. Check the rules that matter most, each against the code that enforces it and the test that pins it.
5. Try a change without spending anything.

It assumes you have read pages [3](ARCHITECTURE.md) and [4](FUNCTIONS.md) at least up to their diagrams. Keep [FUNCTIONS.md](FUNCTIONS.md) open beside the code: its step tables are the order the code runs in.

---

## 0. What you need

- **Go 1.24** ([go.mod](../go.mod)). On Windows, Git Bash is enough.
- **For the unit tests:** nothing else. No keys, no network.
- **For `--check` and the live runs:** a `.env` copied from [.env.example](../.env.example), which explains every setting. For anything that calls a model, Claude Code logged in (`claude /login`).

## 1. Check it builds and its tests pass

The same four steps CI runs on every push ([.github/workflows/test.yml](../.github/workflows/test.yml)):

```
gofmt -l ./cmd ./config ./internal    # prints nothing when everything is formatted
go vet ./...
go test ./...
go build ./...
```

Unit tests never touch the network or a model. The tests that do are skipped unless you switch them on (step 6).

## 2. See what it produces

Read real output before code: it tells you what the code is *for*.

**The latest real runs.** `scripts/sync-cache-from-fly.sh` copies the server's latest brief, analysis and closer look into `data/cache/`. (`data/` is not in git; it exists only on your machine and the server.)

| Folder | Read first | Then |
|---|---|---|
| `data/cache/brief/` | `messages.html`: the brief exactly as sent | `collected.json` (what arrived, what was kept and cut), `articles.json` (what reached the writer), `review.json` (what the review moved), `report.json`, `model/` (the prompt Opus was given and what it wrote), `run.json` |
| `data/cache/recommendations/` | `messages.html`: the closer look as sent | `look.json` (the brief's articles it started from), `leaders.json`, `industries.json`, `themes.json`, `research.json`, `moves.json`, `reactions.json`, `facts.json` (what each verdict rested on), `verdicts.json`, `shown.json`. A name saved twice gets `-2`. |
| `data/cache/analysis/` | `messages.html`: the last `/analyse` | `snapshot.json` (everything read about the company), `verdict.json`, `model/` |

**Every model call.** `data/relay/<time>-<kind>/` has one folder per run: `NN-stage-request.txt` (exactly what the model saw), `NN-stage-reply.txt` (what it answered) and a ledger. The last 40 runs are kept.

**The market's numbers, with no model.** `LIVE_MARKET=1 go test ./internal/app -run TestLiveMarket -v -timeout 4h` prints the leaders, the popular and early industries and the day's outsized moves. It needs `MASSIVE_API_KEY`. The first time, it downloads two years of bars into `data/market/`, which takes about 2 hours. After that it takes seconds.

**In Telegram.** The channel shows what readers see. The owner's chat also has `/stats` (what recent briefs did) and `/scorecard` (how the verdicts have done).

## 3. Read the code in this order

Each step builds on the one before. The **Look for** column says what to judge.

| # | Read | Why | Look for |
|---|---|---|---|
| 1 | [internal/model](../internal/model/) | The plain data everything passes around: `Article`, `Group`/`Company`, `Report`, `Idea`, `Quote`, `Trading`. It imports nothing of ours. | What each field means. Everything else reads and writes these. |
| 2 | [cmd/market-watch/main.go](../cmd/market-watch/main.go) | Start-up, the flags, and `runCheck`. | That a bad config or prompts file stops the process at start, not hours later. |
| 3 | [internal/app/app.go](../internal/app/app.go) | The `App` struct (every collaborator; a nil one turns its feature off), [`New`](../internal/app/app.go#L141), [`Serve`](../internal/app/app.go#L728) (the four loops), [`brief`](../internal/app/app.go#L338), and [`sendReport`](../internal/app/app.go#L425), the brief pipeline in order. | The order of the steps, and what happens when each fails (see [FUNCTIONS.md §2](FUNCTIONS.md#2-the-daily-brief)). |
| 4 | [internal/app/commands.go](../internal/app/commands.go) | [`HandleMessage`](../internal/app/commands.go#L61), the owner gate and router, then one handler per command. | Nothing reachable without passing the owner check. |
| 5 | The brief's packages, in pipeline order: [feed](../internal/feed/) (`Collect` in [collect.go](../internal/feed/collect.go#L115)), [triage](../internal/triage/), [report](../internal/report/), [discover](../internal/discover/), [telegram/render.go](../internal/telegram/render.go) | Gathering, sorting, writing, new names, rendering. | Deduplication, how articles are ranked and cut, how replies are parsed. |
| 6 | [app/look.go](../internal/app/look.go) → [app/ideas.go](../internal/app/ideas.go) → [app/themes.go](../internal/app/themes.go) → [app/reactions.go](../internal/app/reactions.go) | The closer look, in the order it runs ([FUNCTIONS.md §3](FUNCTIONS.md#3-worth-a-closer-look)). | The limits at the top of each file, and what is shown against what is only judged. |
| 7 | [internal/market](../internal/market/) | The market store ([sync.go](../internal/market/sync.go), [store.go](../internal/market/store.go)) and what it's read for ([screen.go](../internal/market/screen.go): leaders, industries, moves; [names.go](../internal/market/names.go): headline mentions). | Whether each measure means what its comment says. The thresholds are in `DefaultRules`. |
| 8 | [internal/ideas](../internal/ideas/) | The three theme stages ([themes.go](../internal/ideas/themes.go)), [valuation.go](../internal/ideas/valuation.go), [judge.go](../internal/ideas/judge.go), [scorecard.go](../internal/ideas/scorecard.go), [themelog.go](../internal/ideas/themelog.go). | The code's rules over the model's verdicts, and the scoring maths. |
| 9 | [internal/fundamentals](../internal/fundamentals/), [internal/sec](../internal/sec/), [internal/consensus](../internal/consensus/) | What a verdict and `/analyse` read: accounts, filings, analysts' expectations. | Which figures can be missing, and how a missing one is shown rather than guessed. |
| 10 | [internal/relay](../internal/relay/) | How a model is called and what it's allowed to do. | Tools off, API key stripped, every call written to disk. |
| 11 | [config/prompts.md](../config/prompts.md), [config/method.md](../config/method.md) | What the models are told: about half the behaviour lives here, not in Go. | That each prompt asks for exactly what its parser reads, and treats articles as data. |
| 12 | [internal/runcache](../internal/runcache/), [internal/history](../internal/history/), [internal/logging](../internal/logging/) | Bookkeeping and safety. | That nothing written or logged can carry a secret. |

**Tip:** each package's tests sit in the same folder, named as rules. Read the names before the code:

```
grep -h "^func Test" internal/ideas/*_test.go
```

## 4. The rules that must hold

These are the rules whose breaking would matter most, where each is enforced, and the test that fails if it breaks. Review these hardest.

| Rule | Enforced in | Pinned by |
|---|---|---|
| **Only the owner's chat can command the bot.** Every command draws on the owner's Claude plan. | [`HandleMessage`](../internal/app/commands.go#L61) | [`TestCommandsFromAnotherChatAreIgnored`](../internal/app/app_test.go#L614), [`TestTheFirstChatToStartBecomesTheOwner`](../internal/app/app_test.go#L652) |
| **Verdicts reach the channel only under the warning note.** If the note goes, the closer look must go back to owner-only. | [`channelNote`](../internal/telegram/ideas.go#L31), `lk.Share` in [`sendIdeas`](../internal/app/ideas.go#L91) | [`TestTheDailyCloserLookReachesTheChannelUnderItsWarning`](../internal/app/ideas_test.go#L147), [`TestACloserLookNotSharedStaysWithTheOwner`](../internal/app/ideas_test.go#L110), [`TestAnAnalysisShowsItsVerdictFirstAndWarnsTheChannel`](../internal/app/analysis_test.go#L30) |
| **No secret leaves the process** through logs, the chat or the run cache. | [`logging.New`](../internal/logging/scrub.go#L35), [`logging.Scrub`](../internal/logging/scrub.go#L47), [runcache](../internal/runcache/runcache.go) | [`TestHandlerScrubsATokenInsideAnError`](../internal/logging/scrub_test.go#L27), [`TestAFailureMessageCarriesNoCredentials`](../internal/app/app_test.go#L595), [`TestEverythingWrittenIsScrubbed`](../internal/runcache/runcache_test.go#L68), [`TestSecretsNameEveryCredential`](../config/config_test.go#L104) |
| **A model can't run commands.** Only `scout`, `research` and `review` may search the web; nothing uses an API key. | [`Claude.Answer`](../internal/relay/answer.go#L95), [`childEnv`](../internal/relay/answer.go#L261) | [`TestClaudeRunsHeadlessWithTheStagesModelAndNoTools`](../internal/relay/relay_test.go#L267), [`TestOnlyResearchAndTheReviewMaySearchTheWeb`](../internal/relay/relay_test.go#L345) |
| **Articles are data, not instructions.** A headline can't steer a model. | The prompts in [prompts.md](../config/prompts.md) | [`TestPromptTreatsArticlesAsData`](../internal/discover/discover_test.go#L271), [`TestTriagePromptDescribesWatchlistsAndTreatsArticlesAsData`](../internal/triage/triage_test.go#L221) |
| **No ticker is shown unverified.** One that can't be checked against OpenFIGI is dropped, or shown without its symbol. | [`FIGI.Verify`](../internal/discover/verify.go#L74), [`ideas.verify`](../internal/ideas/ideas.go#L85), [`VerifyRelated`](../internal/fundamentals/related.go#L90) | [`TestResearchKeepsCheckableNewCompanies`](../internal/ideas/ideas_test.go#L112), [`TestVerifyRelatedShowsNothingWhenTheCheckFails`](../internal/fundamentals/related_test.go#L92), [`TestAnUnverifiableNameSurvivesOnCorroboration`](../internal/discover/discover_test.go#L112) |
| **The model can't bend the valuation rules.** A closed BUY becomes a HOLD; no accounts means low confidence at most. | [`hold`](../internal/ideas/judge.go#L138), [`BuyClosed`](../internal/ideas/valuation.go#L109) | [`TestVerdictsHoldToTheRules`](../internal/ideas/ideas_test.go#L164), [`TestTheValuationGate`](../internal/ideas/ideas_test.go#L262) |
| **Scores are fair:** measured from the next open, against SPY, in dollars, pro-rated, and not before a session has happened. | [scorecard.go](../internal/ideas/scorecard.go): [`Settle`](../internal/ideas/scorecard.go#L322), [`Summary`](../internal/ideas/scorecard.go#L438) | [`TestAVerdictBeforeTheOpenIsMeasuredFromTheOpen`](../internal/ideas/scorecard_test.go#L92), [`TestTheScorecardMeasuresEachVerdictAgainstTheIndex`](../internal/ideas/scorecard_test.go#L30), [`TestASharePricedAbroadIsScoredInDollars`](../internal/ideas/scorecard_test.go#L154), [`TestAVerdictWithNoSessionSinceIsNotScored`](../internal/ideas/scorecard_test.go#L139) |
| **A failed brief loses nothing and says so.** The old brief is deleted only after the new one is written; what was covered is recorded only after delivery. | [`sendReport`](../internal/app/app.go#L425), [`reportFailure`](../internal/app/app.go#L950) | [`TestAFailedBriefIsReported`](../internal/app/app_test.go#L572), [`TestSendReportClearsThePreviousBriefWhenAsked`](../internal/app/app_test.go#L381), [`TestFailedVerdictsSendNothing`](../internal/app/ideas_test.go#L204) |
| **A broken prompts file stops the start**, rather than a reply that can't be parsed hours later. | [`CheckPrompts`](../config/prompts.go#L111) | [`TestTheEmbeddedFileIsComplete`](../config/prompts_test.go#L10), [`TestCheckNamesEveryFault`](../config/prompts_test.go#L24) |
| **The market screens pick what they say they pick.** | [`Leaders`](../internal/market/screen.go#L167), [`Industries`](../internal/market/screen.go#L267), [`Moves`](../internal/market/screen.go#L416), [`Mentions`](../internal/market/names.go#L77) | [`TestLeadersRankSteadyRisersAndLeaveOutJumps`](../internal/market/market_test.go#L214), [`TestIndustriesAreScoredPopularAndEarly`](../internal/market/market_test.go#L263), [`TestMovesAreMeasuredAgainstTheSharesUsual`](../internal/market/market_test.go#L311), [`TestPlainNamesAndMentions`](../internal/market/market_test.go#L339) |

## 5. Questions worth asking as you read

- **Does the model see the number the code computed?** Compare `data/cache/recommendations/facts.json` with the verdicts in `verdicts.json`.
- **What happens when a service is down?** Most steps are best-effort: a missing key or a failed call costs one section, never the brief. Check that each failure is logged and costs only what it should.
- **What does a run cost the plan?** Count the calls in a run's ledger in `data/relay/`. A weekly closer look is the most expensive run.
- **Are the thresholds sensible?** They are constants at the top of each file: [app/ideas.go](../internal/app/ideas.go), [app/themes.go](../internal/app/themes.go), [app/reactions.go](../internal/app/reactions.go), [ideas/ideas.go](../internal/ideas/ideas.go), and `DefaultRules` in [market/screen.go](../internal/market/screen.go).
- **Could a verdict see the future?** Every score must start from a price after the verdict was made.
- **Is anything shown that wasn't checked?** Tickers, prices and figures should each come from a named source.

## 6. Trying a change safely

| Command | What it does | Costs |
|---|---|---|
| `go test ./...` | All unit tests | Nothing |
| `go run ./cmd/market-watch --check` | Proves every key and service; asks no model | Nothing |
| `LIVE_MARKET=1 go test ./internal/app -run TestLiveMarket -v -timeout 4h` | The market's numbers on real data | Nothing but time (2 hours the first time) |
| `LIVE_THEMES=1 go test ./internal/app -run TestLiveThemes -v -timeout 2h` | A real week's themes and reactions, to the owner only | **The owner's Claude plan:** many Opus calls |
| `LIVE_BRIEF=1 go test ./internal/app -run TestLiveBrief -v -timeout 1h` | A real brief, to the owner | **Claude plan** |
| `LIVE_ANALYSIS=MU go test ./internal/app -run TestLiveAnalysis -v -timeout 1h` | A real `/analyse` | **Claude plan**, plus 2 Tavily credits |
| `SEARCH_LIVE=1 go test ./internal/search -v` | Search against the feeds | About 15 Tavily credits |

The full list of live tests is in [FUNCTIONS.md §17](FUNCTIONS.md#17-live-tests). Deploying is `scripts/fly-deploy.sh` ([FUNCTIONS.md §16](FUNCTIONS.md#16-deploy-and-sync-scripts)); it changes what readers receive, so it waits for a deliberate decision.

## 7. Writing down what you find

- **Bugs, ideas and things not yet built:** [TASKS.md](TASKS.md).
- **If you work with Claude Code:** it keeps `HANDOVER.md` at the repository root (on your machine only, not in git), with where the work stands and what comes next.

---

**Start here:** [1 README](../README.md) → [2 Glossary](GLOSSARY.md) → [3 How it works](ARCHITECTURE.md) → [4 Function by function](FUNCTIONS.md) → **5 Reviewing the code** → [6 Running it](RUNBOOK.md) → [7 Backlog](TASKS.md)

**Next:** [6 Running it](RUNBOOK.md): how to operate the service, change its prompts and lists, and deploy.
