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
go run ./cmd/nc start <ref>           # scaffold solution.go + solution_test.go
go run ./cmd/nc status <ref> <state>  # new | attempted | solved | shaky
go run ./cmd/nc rate <ref> <1-5>      # record how it went, reschedule the review
go run ./cmd/nc time <ref> <minutes>  # log time spent
go run ./cmd/nc review                # what is due
go run ./cmd/nc stats                 # progress by pattern
go run ./cmd/nc fetch [--force]       # download statements and signatures
go run ./cmd/nc serve [--port 8150]   # the study site
```

`<ref>` accepts a slug, a LeetCode number, a directory name, or part of a title:
`two-sum`, `1` and `"valid anag"` all resolve. Prefer the CLI over hand-editing
`progress.json`.

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
    hints.md                     the hint ladder, written as the user needs it
    notes.md                     the user's approach and retro
solutions/<NNNN>-<slug>/         the user's solution and tests  (gitignored)
progress.json                    status and review schedule     (gitignored)

cmd/nc/                          the CLI
internal/catalog/                problem list, lookup by slug/number/title
internal/fetcher/                LeetCode + neetcode-gh download, HTML → Markdown
internal/content/                Markdown rendering, content store
internal/progress/               status, ratings, review schedule
internal/scaffold/               solution/test generation from a Go signature
internal/web/                    the study site
tutor/                           the tutoring protocol and its references
```

Note what is gitignored: on a fresh clone there are no statements until `nc fetch` runs,
and `solutions/`, `progress.json`, `hints.md` and `notes.md` never leave the machine.
Do not add them to version control, and do not propose committing them.

## Conventions

- **Go 1.26.** `gofmt` is not negotiable and CI fails on an unformatted file.
- **The standard library.** `cmd/` and `internal/` depend only on goldmark, chroma and
  `golang.org/x/net`. Solutions depend on nothing — if a solution needs a third-party
  package, the solution is wrong.
- **Table-driven tests**, matching what `nc start` generates.
- Match the surrounding comment density. This codebase explains *why*, not *what*; do not
  add comments that restate the code.
- `content/problems/*/problem.md` is fetched output. Do not edit it, except for the seven
  premium problems whose statements are hand-written and tracked.
