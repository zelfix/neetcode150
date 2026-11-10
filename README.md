# neetcode150

[![CI](https://github.com/zelfix/neetcode150/actions/workflows/ci.yml/badge.svg)](https://github.com/zelfix/neetcode150/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**🇷🇺 [Читать по-русски](README.ru.md)**

A local study kit for the [NeetCode 150](https://neetcode.io/practice), built to be worked
through with a coding agent as a **tutor** rather than an answer sheet.

All 150 problems live on disk as Markdown. A single Go binary serves a reading site that
tracks what you have solved and brings it back for review on a widening schedule. A
written tutoring protocol makes the agent walk you through each problem one hint at a time
— and refuse to show you code until you have actually tried.

The protocol is plain Markdown in [`AGENTS.md`](AGENTS.md) and
[`tutor/TUTOR.md`](tutor/TUTOR.md), so it works with any agent that can read a repository.
Nothing here is tied to one vendor.

There are no solutions in this repository, and that is the point. See
[What is not here](#what-is-not-here).

## Requirements

- **Go 1.26+** — `go version` to check.
- **A coding agent** (optional) — for the tutoring flow. See [Agent setup](#agent-setup).
  The CLI and the study site work fine on their own.

## Getting started

```sh
git clone https://github.com/zelfix/neetcode150.git
cd neetcode150

go run ./cmd/nc fetch     # one-off: download statements and Go signatures (a few minutes)
go run ./cmd/nc serve     # http://127.0.0.1:8150
```

`fetch` pulls the problem list from
[neetcode-gh/leetcode](https://github.com/neetcode-gh/leetcode) and each statement from
LeetCode's public GraphQL API, writing them into `content/problems/`. It is idempotent: a
re-run only fills in what is missing and never overwrites a file you have edited. The seven
problems LeetCode keeps behind Premium ship with the repo, written from scratch.

Build a binary if you would rather not type `go run`:

```sh
go build -o nc ./cmd/nc
./nc serve
```

The rest of this README assumes `nc` is on your path. If you skipped the build step, read
`nc` as `go run ./cmd/nc`.

## Solving in the browser

`nc serve` has a workspace at `/problems/<slug>/solve`: a chat on the left, an editor
and test output on the right. The statement is the first chat message, rendered directly
from the fetched document without an AI request. Scroll up to reread it. The **My notes**
tab on the right autosaves your Markdown and preserves it across attempts. After a completed
attempt, the chat stays open for Go idioms, allocation choices and trade-offs. These
follow-up conversations do not restart the timer or change the saved rating. Useful
optional advice is kept with the review in History. Clicking a problem in the roadmap,
dashboard, pattern page or review queue opens this workspace directly. The statement is
visible first; the editor stays blank and inactive until **Start**. If an earlier solution
exists, it is kept under a collapsed previous-solutions section. Starting archives that
solution, creates a blank scaffold and starts the timer. An attempt already in progress opens
with its current code and running timer. Cmd+S saves, Cmd+Enter saves and runs
`go test` on that one package. Results show individual tests and pass/fail/skip counts;
missing or entirely skipped tests do not count as a pass. An empty editor is rejected
instead of testing an older saved file.

The workspace timer stays visible in the header. **Cancel** saves the current editor and
stops the clock; the draft remains available under previous solutions, and **Start** begins
another attempt.

**Submit** runs the same tests and starts a tutor follow-up about your reasoning. Answer in
the tutor chat; the tutor decides whether it needs another question or has enough evidence
to rate the sitting. It then uses your answers, elapsed time, earlier attempts and test
results to update progress and save the submitted source with its feedback in the history.

The workspace also has a tutor panel. Choose which installed agent CLI to use when starting
the site: `nc serve --tutor-provider claude` (the default) or
`nc serve --tutor-provider codex`. The matching CLI must be installed and logged in. The
tutor runs read-only by default; `--tutor-mode full` opts into unrestricted access. The
Claude-only `--tutor-budget <usd>` sets a per-session ceiling.

The editor is a plain textarea. Draft saves preserve your text, including incomplete
syntax, without diagnostics or cursor changes. Explicit test runs and submissions use
`gofmt`. Parse errors are shown when you run or submit, rather than while you are typing.
No third-party editor code is served to your browser.

A problem that comes back for review can be re-solved from the same page: archiving puts
the previous attempt in SQLite and hands you a blank scaffold, so the answer is not
sitting in the file you are meant to be writing. Nothing is destroyed — `nc diff <problem>`
reads the old attempt back once the tests pass, which is the comparison the review flow
asks for. The offer only appears once a problem is finished; re-solving work in progress
would archive something half-written.

The test pane is read-only on purpose: a test you can edit from the same window is a test
you can make pass by editing it.

This is the one part of the site that writes files and runs code, so those endpoints are
guarded. A request has to prove it came from one of this site's own pages (`Sec-Fetch-Site`,
or `Origin` for older clients) and carry a JSON body. Binding to localhost is not that
proof: any page open in your browser can POST to `127.0.0.1`, and a form-encoded POST needs
no preflight, so without the check another site could write Go into your solution and ask
this server to run it. The older status and rating endpoints are behind the same check.

## Working a problem

```sh
nc next                   # what to do now — reviews first, then the roadmap
nc start two-sum          # scaffold solutions/0001-two-sum/ and mark it in progress
# ... write your solution ...
go test ./solutions/0001-two-sum/
nc rate two-sum 4         # how it went; sets the next review date
```

`nc start` writes a `solution.go` carrying the official Go signature and whatever node
types the problem needs, plus populated table-driven tests. All 150 problems have worked
examples and additional corner cases in `internal/testbank/cases/`. The standard-library
helper in `internal/testkit/` handles lists, trees, graphs, stateful operation sequences,
in-place changes and answers with several valid orderings. Codec tests check round trips
without imposing a serialization format. There are no reference solution implementations.

Run `nc tests --all` to add these suites to existing workspaces, or `nc tests 128` for one
problem. Personal tests and solution files are preserved; this command does not start
attempts or change progress. Unstarted problems get their tests on **Start**. The browser's
**Tests** tab shows every test file. Add your own cases locally as you explore a problem;
the supplied cases check behavior, not asymptotic complexity.

## Running it for other people

One instance, each person signing in with their own link, tests in a container — see
[DEPLOY.md](DEPLOY.md). The tutor there must be the `deepseek` provider: the CLI ones run
an agent rooted at the repository, so a guest's tutor could read everybody's work.

## Agent setup

The instructions live in two files at the root, both plain Markdown:

- **[`AGENTS.md`](AGENTS.md)** — what the repo is, the commands, the conventions, and the
  one hard rule: do not write the user's solutions.
- **[`tutor/TUTOR.md`](tutor/TUTOR.md)** — the tutoring protocol, read when you ask to be
  coached.

[`AGENTS.md`](AGENTS.md) is the cross-tool convention, so most agents pick it up with no
setup at all. Agents that look for their own filename get a two-line pointer file instead,
already included:

| Your agent | Reads | Setup |
|---|---|---|
| OpenAI Codex, Cursor, Aider, Zed, and others following the convention | `AGENTS.md` | none |
| Claude Code | `CLAUDE.md` → `AGENTS.md` | none — also has a `/neetcode-tutor` command |
| Gemini CLI | `GEMINI.md` → `AGENTS.md` | none |
| GitHub Copilot | `.github/copilot-instructions.md` | none |
| Anything else | — | see below |

Conventions move around, so check your tool's docs for the filename it wants and point it
at `AGENTS.md` in one line:

```sh
ln -s AGENTS.md .windsurfrules          # or .clinerules, or whatever yours is called
```

And for an agent with no repo-instructions support at all, or a plain chat window, paste
this:

```
Read AGENTS.md and tutor/TUTOR.md in this repository, then act as my tutor for
the next problem. Do not write my solution for me.
```

## The tutor

Ask your agent to coach you — *"what should I do next?"*, *"give me a hint"*, *"review my
solution"* — or run `/neetcode-tutor` if you are in Claude Code.

The design constraint is that it must not solve the problem for you, so the protocol makes
it:

- have you restate the statement in your own words, and check the boundaries — empty
  input, a single element, all-equal, negatives;
- have you state the brute force **and its complexity** before anything else, because the
  pattern is an optimisation *of* the brute force, and the gap between that complexity and
  the constraints is what tells you which pattern you need;
- escalate hints one rung at a time, never jumping to the end:

  | Rung | What it will tell you |
  |---|---|
  | 1 | The pattern's name, or a nudge to guess it |
  | 2 | What to store, and what question that makes cheap |
  | 3 | The invariant — what must hold on every iteration |
  | 4 | Pseudocode |
  | 5 | The Go solution, explained |

- when a test fails, give you the failing case and **one question** instead of a patch;
- record the highest rung you needed, which is what the review schedule keys off.

Ask for the answer outright and it will offer the next rung instead — once. Ask again and
you get it. It is your practice.

How well an agent holds that line varies, and the usual failure is being too helpful: it
patches your bug or writes the loop for you. If yours does that, say *"follow the hint
ladder in tutor/TUTOR.md — one rung only"* and it will generally get back on track.

## The CLI

| Command | What it does |
|---|---|
| `nc fetch [--force]` | Download the problem list, statements and Go signatures |
| `nc serve [--host addr] [--port 8150]` | Serve the study site |
| `nc serve --tutor-provider claude\|codex\|deepseek` | Which tutor answers; only `deepseek` is offered to guests |
| `nc serve --tutor-mode full\|tutor [--tutor-budget usd]` | What the agent may touch, and a per-session cap on Claude |
| `nc serve --sandbox auto\|docker\|local` | Whether test runs are isolated in a container |
| `nc user list\|add\|link\|revoke\|rm` | Who can reach the site, and their join links |
| `nc user add\|link <name> --budget <usd>` | Set, or later change, someone's monthly tutor ceiling |
| `nc next` | What to work on now |
| `nc start <problem>` | Scaffold the Go solution, test and notes |
| `nc status <problem> <state>` | `new`, `attempted`, `solved` or `shaky` |
| `nc rate <problem> <1-5>` | Record how it went and reschedule the review |
| `nc rate <problem> <1-5> --rung <0-5>` | The same, noting the highest hint you needed |
| `nc time <problem> <minutes>` | Log time spent, if the clock did not catch it |
| `nc resolve <problem>` | Archive the solution and reset it for a cold re-solve |
| `nc diff <problem>` | The last archived attempt against the current one |
| `nc drill [--pattern p] [--solved]` | A problem at random, pattern withheld |
| `nc weak [-n 10]` | What is least secure, and why |
| `nc review` | What is due for review |
| `nc stats` | Progress by pattern, with how securely each is held |
| `nc history <problem>` | Every rating one problem has had |
| `nc info <problem>` | Paths and state for one problem |
| `nc notes\|hints <problem>`, `nc log` | Your notes, the hint ladder, the study log; `--file` replaces, `--append-file` adds |
| `nc backup <file.sqlite3>` | A consistent copy of the database; `nc migrate` imports legacy files once |
| `nc export <name> <file.json>` | One person's progress, notes, attempts and transcripts as JSON |
| `nc <command> --help` | That command's line and flags |

`<problem>` takes a slug, a LeetCode number, a directory name, or part of a title —
`nc start two-sum`, `nc start 1` and `nc start "valid anag"` all work.

`nc start` and `nc resolve` start a clock, and `nc rate` logs the elapsed time by itself,
so the schedule runs on measured minutes rather than remembered ones. A sitting left open
longer than six hours is ignored rather than believed.

## Layout

```
AGENTS.md                        instructions for any coding agent
CLAUDE.md, GEMINI.md             short pointers to AGENTS.md
tutor/
  TUTOR.md                       the tutoring protocol
  references/                    complexity targets, Go idioms, loop invariants

content/
  problems.json                  the 150, in roadmap order
  study-order.md, .csv           the same list, readable
  patterns/<slug>.md             18 pattern guides
  problems/<NNNN>-<slug>/
    problem.md                   the statement          (fetched)
    signature.txt                the official Go signature (fetched)
solutions/<NNNN>-<slug>/         your Go solution and its tests
_work/<userID>/solutions/...     a guest's, on a shared instance
study.sqlite3                    your progress, attempts, chat, notes and reviews

cmd/nc/                          the CLI
internal/
  catalog/                       the problem list and lookup by slug/number/title
  fetcher/                       LeetCode + neetcode-gh download, HTML → Markdown
  content/                       Markdown rendering and the content store
  progress/                      status, ratings, the review schedule
  scaffold/                      solution and test generation from a Go signature
  web/                           the study site
.claude/skills/neetcode-tutor/   wraps tutor/TUTOR.md as a Claude Code command
```

## Review schedule

Solving something once is not knowing it. A solve enters a ladder of widening intervals —
1, 3, 7, 16, 35, then 90 days — and `nc rate` moves you along it. A confident review widens
the gap; a slip pulls it back. The site's **Review** page shows what is due, and `nc next`
puts reviews ahead of new material on purpose.

The ladder is the backbone, not the whole of it. Each problem also carries an *ease*: a
multiplier that shortens its intervals while it keeps needing help and lengthens them once
it stops, so two problems on the same rung are not assumed to be equally secure. Reviews
are also nudged a day or two off their exact date, because a pattern worked through in one
sitting would otherwise come back all on one day. `nc info` reports both the ease and the
interval actually in force.

Rate honestly. The scale is about the help you needed, not how you feel now that you have
seen the answer:

| | | |
|---|---|---|
| **1** | Needed the full solution. | Back to the start of the ladder, marked shaky. |
| **2** | Needed a strong hint. | Falls two rungs, marked shaky. |
| **3** | Got there, with effort. | Holds its place. |
| **4** | Comfortable. | Interval widens. |
| **5** | Near-instant. | Interval widens. |

The hint you needed is counted separately, and it has the final say on the schedule: a
sitting that needed the invariant spelled out (rung 3) holds its rung rather than earning
the next one, and one handed pseudocode or working Go (rung 4 or 5) falls back down the
ladder. Being shown the answer is not recall, however comfortable it felt afterwards.

A 1 and a 2 are deliberately not the same. Forgetting something outright and getting it
with a nudge are different events, and sending a problem you held for 35 days back to
tomorrow is time spent on what you nearly knew.

An inflated rating pushes a problem out to 90 days, by which point you will have forgotten
it. `nc rate --rung <0-5>` records the highest hint you actually needed alongside the
rating; a 4 next to rung 3 is the signature of a generous self-assessment, and `nc history`
and `nc weak` both surface it.

Because the rating is what drives all of this, a wrong one is worth taking back:
`nc rate <problem> --amend <1-5>` corrects the last one and `nc rate <problem> --undo`
drops it, both rebuilding the schedule from the ratings that remain. Logged minutes stay —
the sitting happened whichever rating was written down.

A review is a re-solve from scratch, not a re-read. `nc resolve <problem>` sets that up: it
archives your current solution in SQLite and resets the file to a blank scaffold,
so you cannot read the answer out of the file you are meant to be writing. Once the test
passes, `nc diff <problem>` shows the two attempts against each other — did you find
something better, or forget something you knew?

Reviews are not the only thing worth mixing up. `nc next` walks the roadmap in order, which
means you know the pattern before you have read the statement, and that is most of the
work. `nc drill` draws a problem at random and does not tell you which pattern it belongs
to. Interleaved practice feels worse during a session and holds up better weeks later.

## Coach

A count of solved problems flatters a problem that was only ever solved with help, which is
exactly the one worth going back to. The site's **Coach** page answers the harder question
off the data the ratings already carry.

The headline is the share of rated problems whose last sitting needed no hint at all —
not how much you have covered, but how much you can do unaided. Below it, patterns are
ranked for attention: hints needed, slips since last time and low ratings all push a
pattern up, while a pattern you have not started is not listed, because it is not a
weakness. A pattern that is holding is not listed either; a list that flags everything
stops being read. `nc stats` prints the same per-pattern figures in the terminal, and
`nc weak` names the individual problems.

The page also keeps the one thing the test output knew and nothing used to record: which
fixtures each run broke. Over time that separates two different failures. Breaking a worked
example means the approach is wrong; breaking only corner cases means it is nearly right and
under-tested. Runs that never happened — a compile error, a cut-off loop — are not counted,
or the pass rate would measure typing rather than understanding.

## What is not here

Two categories of file are deliberately gitignored. Both are listed in
[`.gitignore`](.gitignore) with the reasoning inline.

Personal study data lives in `study.sqlite3`: progress, archived attempts, ratings,
learning summaries, notes, hint ladders, tutor conversations, pending assessments, test
runs and the fixtures they broke.
CLI and browser share it. Finishing a submission saves its snapshot, rating and schedule
in one transaction; transcripts survive server restarts. Go solutions and tests remain
regular files for editing and `go test`.

The first open imports `progress.json`, personal Markdown and archived attempts once.
Legacy files remain untouched backups and are no longer live sources. Stop an older
server before first migration. `nc migrate` checks the import and database integrity.
A corrupt legacy file stops migration rather than silently dropping that record.

```sh
nc notes two-sum                       # print personal notes
nc notes two-sum --file /tmp/notes.md  # replace notes with your own Markdown
nc hints two-sum --file /tmp/hints.md  # save the recorded hint ladder
nc log --append-file /tmp/session.md  # append a tutor observation
nc backup study-backup-2026-10-31.sqlite3
```

Use `nc backup` for a consistent live backup. SQLite uses WAL, so copying only the main
file while the server is running can omit committed data. Keep backups private and outside
version control. Never edit legacy files expecting the site to pick up the changes.

**Nobody's solutions.** `solutions/`, `study.sqlite3` and its WAL/SHM files, and legacy
personal data are gitignored. A clone contains no worked solutions or personal history.

**LeetCode's problem statements.** `content/problems/*/problem.md` and `signature.txt` are
fetched from LeetCode at runtime, not redistributed here. `nc fetch` puts them on your disk
in a few minutes. The seven Premium problems are the exception — LeetCode will not serve
them, so those statements are original work and are tracked.

No solutions are downloaded either. The site links out to LeetCode and to the NeetCode
walkthrough rather than embedding anything, so the answer is never one click away from the
statement.

## Credits

The 150-problem list, the pattern grouping and the walkthrough links come from
[neetcode-gh/leetcode](https://github.com/neetcode-gh/leetcode) (MIT). Problem statements
are fetched from [LeetCode](https://leetcode.com) and remain their authors' work. Markdown
rendering is [goldmark](https://github.com/yuin/goldmark), syntax highlighting is
[chroma](https://github.com/alecthomas/chroma). The locally served [IBM Plex](https://github.com/IBM/plex) fonts are licensed under
the SIL Open Font License 1.1; their license is included with the font assets. Local study storage uses
[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite), without CGO.

## License

[MIT](LICENSE) — this covers the CLI, the study site, the pattern guides and the tutoring
protocol. It does not cover the problem statements fetched from LeetCode, which are not
mine to license.
