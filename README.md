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

## Working a problem

```sh
nc next                   # what to do now — reviews first, then the roadmap
nc start two-sum          # scaffold solutions/0001-two-sum/ and mark it in progress
# ... write your solution ...
go test ./solutions/0001-two-sum/
nc rate two-sum 4         # how it went; sets the next review date
```

`nc start` writes a `solution.go` carrying the official Go signature and whatever node
types the problem needs, plus a `solution_test.go` — table-driven when the problem is one
function returning one value, a plain skeleton when it is not. Filling in the test table
from the worked examples is the first half of solving the problem.

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
| `nc serve [--port 8150]` | Serve the study site |
| `nc next` | What to work on now |
| `nc start <problem>` | Scaffold the Go solution, test and notes |
| `nc status <problem> <state>` | `new`, `attempted`, `solved` or `shaky` |
| `nc rate <problem> <1-5>` | Record how it went and reschedule the review |
| `nc time <problem> <minutes>` | Log time spent |
| `nc review` | What is due for review |
| `nc stats` | Progress by pattern |
| `nc info <problem>` | Paths and state for one problem |

`<problem>` takes a slug, a LeetCode number, a directory name, or part of a title —
`nc start two-sum`, `nc start 1` and `nc start "valid anag"` all work.

## Layout

```
AGENTS.md                        instructions for any coding agent
CLAUDE.md, GEMINI.md             two-line pointers to AGENTS.md
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
    hints.md                     your hint ladder, written as you need it
    notes.md                     your approach and retro
solutions/<NNNN>-<slug>/         your Go solution and its tests
progress.json                    what you have solved, and when it is next due

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
the gap; a shaky one pulls it back to the start. The site's **Review** page shows what is
due, and `nc next` puts reviews ahead of new material on purpose.

Rate honestly. The scale is about the help you needed, not how you feel now that you have
seen the answer:

| | |
|---|---|
| **1** | Needed the full solution. Back tomorrow, marked shaky. |
| **2** | Needed a strong hint. Same. |
| **3** | Got there, with effort. Stays on the ladder. |
| **4** | Comfortable. Interval widens. |
| **5** | Near-instant. Interval widens. |

An inflated rating pushes a problem out to 90 days, by which point you will have forgotten
it.

A review is a re-solve from scratch, not a re-read: the protocol says not to show you your
old solution until you have written a new one. Comparing the two afterwards is the
interesting part — did you find something better, or forget something you knew?

## What is not here

Two categories of file are deliberately gitignored. Both are listed in
[`.gitignore`](.gitignore) with the reasoning inline.

**Nobody's solutions.** `solutions/`, `progress.json`, and every `hints.md` and `notes.md`.
A clone that arrives with 150 worked solutions is an answer sheet, and a problem you did
not think through is worse than an unsolved one — it removes the signal that you still need
to practise it. You get an empty slate and write your own.

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
[chroma](https://github.com/alecthomas/chroma).

## License

[MIT](LICENSE) — this covers the CLI, the study site, the pattern guides and the tutoring
protocol. It does not cover the problem statements fetched from LeetCode, which are not
mine to license.
