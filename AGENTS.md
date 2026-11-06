# AGENTS.md

Instructions for any coding agent working in this repository. Agent-neutral by design —
see [Agent setup](README.md#agent-setup) for how each tool picks this up.

## What this repository is

A study kit for the [NeetCode 150](https://neetcode.io/practice), solved in Go. It holds
the 150 problem statements, 18 pattern guides, a `nc` CLI that tracks progress on a
spaced-repetition schedule, and a local study site.

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
go run ./cmd/nc tests --all           # add suites to existing workspaces; preserve personal tests
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
go run ./cmd/nc weak [-n 10]          # what is least secure, and why
go run ./cmd/nc review [--limit 8]    # what is due, most overdue first
go run ./cmd/nc stats                 # progress by pattern, and how securely each is held
go run ./cmd/nc history <ref>         # every rating one problem has had
go run ./cmd/nc fetch [--force]       # download statements and signatures
go run ./cmd/nc serve [--port 8150]   # the study site, incl. the browser workspace
go run ./cmd/nc user list             # who can reach the site
go run ./cmd/nc user add <name>       # invite someone; prints their join link once
go run ./cmd/nc user link <name>      # issue a new link, invalidating the old one
go run ./cmd/nc user revoke <name>    # stop a link working, keeping their work
go run ./cmd/nc user rm <name> --yes  # delete someone and everything of theirs
```

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
own; the tutor is still one process per problem rather than per person, so it stays the
owner's for now.

The site's workspace at `/problems/<slug>/solve` writes `solution.go` and runs `go test`.
Those endpoints require a same-origin JSON request (`internal/web/guard.go`) — a plain
`curl` is refused by design, so reach for the CLI rather than trying to work around it.

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
go test ./solutions/<NNNN>-<slug>/          # one problem
```

## Layout

```
content/
  problems.json                  the 150, in roadmap order
  patterns/<slug>.md             18 pattern guides
  problems/<NNNN>-<slug>/
    problem.md                   the statement          (fetched; gitignored)
    signature.txt                official Go signature  (fetched; gitignored)
solutions/<NNNN>-<slug>/         the owner's solution and tests  (gitignored)
_work/<userID>/solutions/...     a guest's, same shape               (gitignored)
study.sqlite3                    all personal study data        (gitignored)

cmd/nc/                          the CLI
internal/catalog/                problem list, lookup by slug/number/title
internal/fetcher/                LeetCode + neetcode-gh download, HTML → Markdown
internal/content/                Markdown rendering, content store
internal/progress/               status, ratings, review schedule
internal/studydb/                SQLite schema, import and storage
internal/scaffold/               solution/test generation from a Go signature
internal/workspace/              where one person's working Go files live
internal/testbank/               examples and corner-case fixtures for all 150
internal/testkit/                standard-library practice test helpers
internal/web/                    the study site
tutor/                           the tutoring protocol and its references
```

Note what is gitignored: on a fresh clone there are no statements until `nc fetch` runs,
and `solutions/`, `study.sqlite3` (including WAL/SHM), and legacy personal data never
leave the machine.
Do not add them to version control, and do not propose committing them.

## Conventions

- **Go 1.26.** `gofmt` is not negotiable and CI fails on an unformatted file.
- **Dependencies.** Tooling uses goldmark, chroma, `golang.org/x/net`, and the pure-Go
  `modernc.org/sqlite` driver. Solutions depend on nothing — if a solution needs a third-party
  package, the solution is wrong.
- **Table-driven tests**, matching what `nc start` generates.
- Match the surrounding comment density. This codebase explains *why*, not *what*; do not
  add comments that restate the code.
- `content/problems/*/problem.md` is fetched output. Do not edit it, except for the seven
  premium problems whose statements are hand-written and tracked.
