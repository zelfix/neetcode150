# AGENTS.md

Instructions for any coding agent working in this repository. Agent-neutral by design —
see [Agent setup](README.md#agent-setup) for how each tool picks this up.

## What this repository is

A study kit for the [NeetCode 150](https://neetcode.io/practice), solved in Go or in
Python. It holds the 150 problem statements, 18 pattern guides, a `nc` CLI that tracks
progress on a spaced-repetition schedule, and a local study site.

It is a **practice** repository. The user is learning, and the solutions are theirs to
write.

## The rule that overrides everything else

**Do not solve the practice problems for the user.**

Anything under `solutions/` is the user's own work. Do not write the body of a solution,
do not fix their bug by patching it, and do not paste a working implementation —
even if asked directly, until you have offered a smaller hint first and they have asked
again.

A solved problem the user did not think through is worse than an unsolved one: it removes
the signal that they still need to practise it. Being maximally helpful here means
*withholding* the answer, which is the opposite of the usual default. This is the whole
point of the repository, so treat it as a hard constraint rather than a preference.

What you may always do:

- fill in a test table from the worked examples in `problem.md`
- answer a Go language question (`tutor/references/go-idioms.md`)
- explain a compile error or name a failing case
- work on the tooling: `cmd/`, `internal/`, `content/patterns/`, docs, CI

## Tutoring

If the user wants to be coached through a problem — "what's next", "give me a hint",
"review my solution", "let's do a review" — **read [`tutor/TUTOR.md`](tutor/TUTOR.md) in
full before replying.** It is the complete protocol: how to pick the problem, how to check
they have read the statement, the five-rung hint ladder and the rule that you climb it one
rung per request, how to answer a failing test, and what to record afterwards.

Do not improvise a tutoring style from this file alone. The hint ladder is the part people
get wrong, and it is specified there precisely.

Supporting references, cited from the protocol:

| File | For |
|---|---|
| `tutor/references/complexity.md` | Reading the target complexity off the constraints |
| `tutor/references/go-idioms.md` | Go mechanics these problems need, and Go-specific traps |
| `tutor/references/invariants.md` | Loop invariants |

## Commands

Everything runs through the `nc` CLI. From the project root:

```sh
go run ./cmd/nc next                  # what the user should work on now
go run ./cmd/nc info <ref>            # paths and state for one problem
go run ./cmd/nc start <ref>           # scaffold solution + example/corner-case tests
                                      #   --lang go|python chooses for this problem
go run ./cmd/nc tests --all           # add suites to existing workspaces; preserve personal tests
                                      #   --lang go|python picks which workspaces (default go)
go run ./cmd/nc status <ref> <state>  # new | attempted | solved | shaky
go run ./cmd/nc rate <ref> <1-5>      # record how it went, reschedule the review
                                      #   --rung <0-5> notes the highest hint needed;
                                      #   rung 3 holds the stage, rung 4-5 drops it
                                      #   --amend <1-5> corrects the last rating
                                      #   --undo drops it and rebuilds the schedule
go run ./cmd/nc time <ref> <minutes>  # log time spent, if the clock missed it
go run ./cmd/nc resolve <ref>         # archive the solution, reset it for a cold re-solve
go run ./cmd/nc diff <ref>            # last archived attempt vs the current one
go run ./cmd/nc drill [--pattern p]   # a problem at random, pattern withheld
                                      #   --solved draws from solved ones, for retrieval practice
go run ./cmd/nc weak [-n 10]          # what is least secure, and why
go run ./cmd/nc review [--limit 8]    # what is due, most overdue first
go run ./cmd/nc stats                 # progress by pattern, and how securely each is held
go run ./cmd/nc history <ref>         # every rating one problem has had
go run ./cmd/nc notes <ref>           # the user's notes; --file <path> replaces them
go run ./cmd/nc hints <ref>           # the recorded hint ladder; --file <path> replaces it
go run ./cmd/nc log                   # the study log; --append-file <path> adds an entry
go run ./cmd/nc fetch [--force]       # download statements and signatures
go run ./cmd/nc serve [--port 8150]   # the study site, incl. the browser workspace
                                      #   --host <addr> to listen beyond loopback
                                      #   --tutor-provider claude|codex|deepseek
                                      #   --tutor-mode full|tutor (what the agent may touch)
                                      #   --tutor-model <name>, DEEPSEEK_API_KEY from the env
                                      #   --tutor-budget <usd>, a per-session cap (claude only)
                                      #   --tutor-rates <file.json>, a DeepSeek price list (DEPLOY.md)
                                      #   --sandbox auto|docker|local, --max-runs <n>
                                      #   --sandbox-image <go image>, --sandbox-image-python <py image>
go run ./cmd/nc user list             # who can reach the site
go run ./cmd/nc user add <name>       # invite someone; prints their join link once
                                      #   --budget <usd> sets their monthly tutor ceiling
                                      #   --lang go|python sets the language they start in
go run ./cmd/nc user lang <name> <go|python>  # change that default later
go run ./cmd/nc user link <name>      # issue a new link, invalidating the old one
                                      #   --budget <usd> changes the ceiling at the same time
go run ./cmd/nc user revoke <name>    # stop a link working, keeping their work
go run ./cmd/nc user rm <name> --yes  # delete someone and everything of theirs
go run ./cmd/nc migrate               # import legacy JSON/Markdown into SQLite once
go run ./cmd/nc backup <file.sqlite3> # a consistent copy of the database
go run ./cmd/nc export <name> <file.json>  # one person's data, nobody else's, no token hashes
go run ./cmd/nc <command> --help      # that command's line and flags
```

## Languages

Every problem is worked in Go or in Python. The language is a per-person default
(`nc user add --lang`, `nc user lang`, or the selector in the site header) with a
per-problem override chosen at start (`nc start --lang`, or the selector beside Start).
`internal/lang` is the one place that knows what differs: file names, the formatter, the
fence label, the indent. `lang.Resolve` is the one rule for which language a problem is
in — the problem's own choice, else the person's default, else Go — and the CLI and the
site both call it rather than deciding on their own.

Both languages share one directory per problem: `solution.go`, `solution_test.go` and
`practice_cases_test.go` beside `solution.py`, `test_solution.py` and `practice_cases.py`.
Starting a problem in Python next to a Go solution adds files and archives nothing. The
schedule, ratings and hint ladders are per problem, not per language; an archived attempt
records which language it was in, and `nc diff` compares same-language attempts only.
Fixture-failure counts on the Coach page merge both languages, which is deliberate: the
fixture is what was hard.

The Python test harness is `internal/testkit/py`, a standard-library-only port of
`internal/testkit` that reads the same fixtures and reports in the same `go test -json`
line format, so `readTestEvents` and everything downstream of it have no second code
path. The eleven comparison policies exist twice, so both suites run the shared table in
`internal/testkit/testdata/policy-cases.json`, and `TestAllPracticeSuitesMatchPythonSignatures`
validates all 150 fixtures against the Python stubs under `python3`. Keep the harness on
the standard library; its tests are `make python-test`.

The site's Coach page at `/coach/` reports what is not secure yet: the share of problems
last solved unaided, which patterns to go back to, and which test fixtures keep breaking.
It is read-only, and reuses `progress.Patterns`, `progress.Weakest` and the run records
that `handleRunTests` files — do not re-derive weakness anywhere else.

The site serves nobody without a cookie. `/join/<token>` trades a link for one, and
`nc user` mints them — there is no admin page, so the web surface cannot create an
identity. Only a token's hash is stored, which is why a link can be reissued but never
shown again. There is no exception for loopback or for a missing cookie: behind a reverse
proxy every request looks local, so an exception would make a guest who clears their
cookie into the owner. Each person's progress, notes, attempts and test runs are their
own, and so is the tutor: sessions are keyed by person as well as problem.

The panel is offered to guests only on a provider that cannot read the server. The CLI
providers (`claude`, `codex`) run an agent with the repository as its working directory,
so a guest's tutor there could read everybody's solutions and the study database — wider
than anything the test sandbox closes — and `tutorGate` refuses them. `deepseek` talks to
an HTTP API with no tools and no file access, so guests get the panel there.

Because it fetches nothing, that provider is *handed* its context: `internal/tutor/prompt.go`
assembles the protocol, the references, the statement and the recorded ladder into the
system prompt, and the person's current source into the turn. That split is about money,
not tidiness — a cached prefix costs about a thirtieth of a fresh one, and source changes
every turn, so putting the code in the system prompt would miss the cache on every single
turn. Keep it out of there. Cost comes from the `usage` the API reports, priced per model
with the published peak and off-peak rates (peak is 01:00–04:00 and 06:00–10:00 UTC on
weekdays), and an unknown model is priced as the dearest so an allowance is never
under-charged. Each person has a monthly tutor
allowance, checked before a turn starts and recorded as each turn finishes. Cost arrives
as an *increment*: the Claude CLI reports `total_cost_usd` for the session so far, so
summing what it reports across turns overstates a five-turn session threefold.

The site's workspace at `/problems/<slug>/solve` writes `solution.go` and runs `go test`.
Those endpoints require a same-origin JSON request (`internal/web/guard.go`) — a plain
`curl` is refused by design, so reach for the CLI rather than trying to work around it.

Deploying a shared instance is [DEPLOY.md](DEPLOY.md); two of its steps are load-bearing
and fail in misleading ways, so do not simplify them away — a reverse proxy must pass the
original `Host` (the same-origin check compares `Origin` against it, and a rewritten Host
makes every save fail while pages keep working) and `X-Forwarded-Proto` (without it the
sign-in cookie is not marked `Secure`).

`go test` runs in a one-shot container (`sandbox/Dockerfile`, built as
`nc-sandbox:go1.26`). The mount set is the security model, and what it leaves out is the
point: the repository root is never mounted, so a guest cannot
`import "neetcode150/internal/studydb"` and read everybody's data — only `internal/testkit`,
`internal/testbank` and the one package under test are present, all read-only, with no
network and a per-user build cache. The image carries a `go.mod` declaring only the module
path, so the sandbox needs no proxy and a third-party import simply fails to resolve.
Inside the container the package always appears at `/src/solutions/<problem>`, whoever owns
it on the host.

Python runs in a second image (`sandbox/Dockerfile.python`, `nc-sandbox:py3.12`) with
`site-packages` emptied and pip removed — the Python analogue of that `go.mod` — and
mounts strictly less: the harness at `/opt/nc/harness` and the package, both read-only,
nothing writable, half the memory, a `noexec` tmpfs, and `python3 -I -B` so nothing in the
package can shadow a standard module. The Go image decides whether a container is used at
all; a missing Python image switches Python off with the build command in the message and
never drops to running it unisolated. `--sandbox=local` needs `python3` 3.12 or newer.

`--sandbox=local` runs `go test` unisolated, which is right for one person on their own
machine and refused outright once any guest exists — the server will not start. Do not
soften that into a warning. Two details that cost an afternoon to find: the tmpfs must be
mounted `exec`, because `go test` builds its binary into `GOTMPDIR` and runs it from
there; and killing the `docker run` client does not stop the container, so cancellation
goes through `docker kill` on a known name.

Working Go files have to stay inside this module: a generated practice test imports
`neetcode150/internal/testkit`, and Go's internal-package rule is a plain import-path
prefix test, so a separate module with a `replace` directive or a `go.work` file cannot
host them. Hence `_work/<userID>/` rather than a directory outside the repo. The leading
underscore is load-bearing — the go tool skips `_`-prefixed directories when expanding
`./...`, so guest code never reaches `go vet ./...` or the CI test run while an explicit
package path still builds. Ask `internal/workspace` for a path; do not join one by hand.

`<ref>` accepts a slug, a LeetCode number, a directory name, or part of a title:
`two-sum`, `1` and `"valid anag"` all resolve. Prefer the CLI and browser over editing `study.sqlite3` directly.

Mutable study data lives in the gitignored SQLite database: progress, snapshots, ratings,
notes, hint ladders, session logs, tutor events, pending submissions, test runs and the
fixtures they broke. The first open
imports legacy JSON/Markdown/archives once; those files remain untouched backups.
Use `nc notes <ref>`, `nc hints <ref>`, and `nc log` to read personal Markdown.
`--file <path>` replaces it; `nc log --append-file <path>` appends a session entry.
`nc backup <new-file.sqlite3>` makes a consistent backup; do not copy a live WAL database
as a single file. Never hand-edit legacy files expecting the site to read them.

Verification, and what CI enforces:

```sh
gofmt -l .                                  # must print nothing
go vet ./...
go build ./...
go test -race ./...                         # the tooling
go test ./solutions/<NNNN>-<slug>/          # one problem, Go
python3 -I -B internal/testkit/py/run.py --package solutions/<NNNN>-<slug>   # one problem, Python
make python-test                            # the Python harness's own tests
```

## Layout

```
content/
  problems.json                  the 150, in roadmap order
  patterns/<slug>.md             18 pattern guides
  problems/<NNNN>-<slug>/
    problem.md                   the statement          (fetched; gitignored)
    signature.txt                official Go signature  (fetched; gitignored)
    signature.py                 official Python stub   (fetched; gitignored)
solutions/<NNNN>-<slug>/         the owner's solution and tests  (gitignored)
_work/<userID>/solutions/...     a guest's, same shape               (gitignored)
study.sqlite3                    all personal study data        (gitignored)

cmd/nc/                          the CLI
internal/catalog/                problem list, lookup by slug/number/title
internal/fetcher/                LeetCode + neetcode-gh download, HTML → Markdown
internal/content/                Markdown rendering, content store
internal/progress/               status, ratings, review schedule
internal/studydb/                SQLite schema, import and storage
internal/lang/                   what differs between Go and Python, in one place
internal/scaffold/               solution/test generation from a Go or Python signature
internal/workspace/              where one person's working files live
sandbox/                         the containers that run a guest's tests, one per language
internal/testbank/               examples and corner-case fixtures for all 150
internal/testkit/                standard-library practice test helpers (Go)
internal/testkit/py/             the same harness in Python, stdlib only
internal/web/                    the study site
tutor/                           the tutoring protocol and its references
```

Note what is gitignored: on a fresh clone there are no statements until `nc fetch` runs,
and `solutions/`, `study.sqlite3` (including WAL/SHM), and legacy personal data never
leave the machine.
Do not add them to version control, and do not propose committing them.

## Conventions

- **Go 1.26.** `gofmt` is not negotiable and CI fails on an unformatted file.
- **Python 3.12** for the harness and for Python solutions; standard library only, no
  pip, no pytest. There is no Python formatter in the loop, by choice.
- **Dependencies.** Tooling uses goldmark, chroma, `golang.org/x/net`, and the pure-Go
  `modernc.org/sqlite` driver. Solutions depend on nothing — if a solution needs a third-party
  package, the solution is wrong, in either language.
- **Table-driven tests**, matching what `nc start` generates.
- Match the surrounding comment density. This codebase explains *why*, not *what*; do not
  add comments that restate the code.
- `content/problems/*/problem.md` is fetched output. Do not edit it, except for the seven
  premium problems whose statements are hand-written and tracked.
