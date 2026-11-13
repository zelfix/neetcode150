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

`tutor/INTERVIEW.md` is the other protocol: what the site's Interview page runs instead of
this one. Do not follow it when somebody asks to be coached, and do not fold the two
together — the point of the second is that it withholds everything the first exists to
give.

## Commands

The study itself happens in the browser. What the CLI is actually needed for is
narrower than the list below suggests, and splits by deployment:

- **A local instance, for yourself:** `nc fetch` once, `nc user link owner` to get a
  sign-in link (there is no loopback exception — see below), then
  `nc serve --tutor-provider claude`, which spends a Claude subscription rather than
  tokens.
- **A shared instance, for guests:** the above plus `nc user add/list/link/revoke/rm`,
  `nc backup` and `nc export`, with `--tutor-provider deepseek` and `--sandbox docker`.
  See [DEPLOY.md](DEPLOY.md).
- **Everything else** is a second way to do what the site already does, kept as the
  escape hatch and as the path an agent has in `--tutor-mode full`. Reach for the browser
  first; `nc diff`, `nc history`, `nc time` and `nc rate --rung` all have
  equivalents there now.

The full reference. From the project root:

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
go run ./cmd/nc user link <name>      # issue a new link, invalidating the old one
                                      #   --budget <usd> changes the ceiling at the same time
go run ./cmd/nc user revoke <name>    # stop a link working, keeping their work
go run ./cmd/nc user rm <name> --yes  # delete someone and everything of theirs
go run ./cmd/nc invite new            # one link several people may join through
                                      #   --count <n>, --expires 48h (required, max 720h)
                                      #   --budget <usd> each joiner's ceiling, must be > 0
                                      #   --note <text>, --base-url <addr>
go run ./cmd/nc invite list           # what each invite still allows; never the tokens
go run ./cmd/nc invite revoke <id>    # spend the rest of its uses
go run ./cmd/nc migrate               # import legacy JSON/Markdown into SQLite once
go run ./cmd/nc backup <file.sqlite3> # a consistent copy of the database
go run ./cmd/nc export <name> <file.json>  # one person's data, nobody else's, no token hashes
go run ./cmd/nc <command> --help      # that command's line and flags
```

## Languages

Every problem is worked in Go or in Python, and the choice belongs to the problem alone —
made when it is started (`nc start --lang`, or the Start-in-Go and Start-in-Python buttons
in the workspace) and recorded on its own row. There is deliberately no per-person default
and no global switch: a language that applies to every problem at once has to be set before
starting one and read back everywhere afterwards, which is a setting to maintain rather
than a choice to make — and the header control that did it never re-rendered the pages
already on screen, so the change appeared not to take.

`internal/lang` is the one place that knows what differs: file names, the formatter, the
fence label, the indent. `lang.Resolve` is the one rule for which language a problem is
in — the problem's own choice, else Go — and the CLI and the site both call it rather than
deciding on their own. Go answers only for a problem nobody has started, so that paths and
labels have something to show.

Both languages share one directory per problem: `solution.go`, `solution_test.go` and
`practice_cases_test.go` beside `solution.py`, `test_solution.py` and `practice_cases.py`.

The two languages reach the same place by different roads, and the difference is
deliberate. Go embeds the curated suite in `solution_test.go` and writes no second test
file, so there is never an empty table. Python keeps the suite in its own
`practice_cases.py`, which would leave `test_solution.py` empty — and an empty table has
only bad options: skipping reports missing coverage that is not missing, and looping over
nothing reports a pass it has not earned. So `test_solution.py` is written out
**commented**: nothing runs, nothing is claimed, and the shape is there to uncomment when a
case of your own is worth keeping. Only a problem with no curated suite gets a runnable
skipping skeleton, because there the file is the only thing that tests anything.

For the same reason `solution.py` imports `typing` names only when the generated body
subscripts one. Modern stubs annotate with the builtins (`list[int]`, `TreeNode | None`),
so that is 12 problems out of 150; emitting the import everywhere left a dead line at the
top of almost every solution, which is the first thing a reader — or a tutor — questions.
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

The dashboard reports what is not secure yet: the share of problems last solved unaided,
which patterns to go back to, which problems look weakest, and which test fixtures keep
breaking. That half was a Coach page at `/coach/` until the two were merged — both read the
same ratings, so the split put the count of solved problems on one page and the figures
that qualify it on another. `/coach/` is gone, not redirected. It is all read-only, and
reuses `progress.Patterns`, `progress.Weakest` and the run records that `handleRunTests`
files — do not re-derive weakness anywhere else. The per-pattern table replaced the
dashboard's own lane strip, which `/problems/` now carries.

`/problems/` is the only index over the eighteen groups. `/patterns/` was a second one and
is gone outright rather than redirected — the site has never been published, so a redirect
would be a second name for a page kept for nobody. `/patterns/<slug>` still serves the
guide. The lanes at the top of the
list are that former page. Two details there are easy to undo by accident: the lanes are
in-page jumps (`#pattern-<slug>`) rather than links to the guide, because the group's own
heading is already that link; and `app.js` hides a lane whose group the filters emptied, so
a jump cannot land on a hidden heading. Both halves come from one `s.groups(now)` call.

`/interview/` runs a practice interview: two or three problems drawn at random, a time
limit each, and the tutor in a second role. It replaced `/drill/`, which drew one random
problem and stopped there, and `nc drill` went with it. The draw is still `Server.pick`,
injected like `Server.now` so a test gets a determined interview, over
`progress.DrillPool` and `catalog.MatchPattern`.

Four things about it are load-bearing, and each is easy to undo by accident:

- **The pattern is withheld everywhere the interview reaches**, not just on its own page.
  `solve.html` prints `.P.Pattern` in the statement header, and the record pane prints it
  again; both are behind `{{if not .Interview}}` or behind `Active`. Do not render it, and
  do not reuse `problemRow` or `cells` on the interview page, because both print it.
- **An interview writes no rating and moves no review date.** `handleSubmit` branches to
  `submitToInterview` before it reaches `CompleteIn` or `InsertAttempt`; what it records is
  the time, through `progress.ClockMinutes` into `AddMinutes`. The review ladder is built on
  cold unaided solves and an interview is a different measurement.
- **Its files are not the study files.** `workspace.Interview` puts them under
  `_work/<user>/interview-<id>/`, and `s.work(p)` is the only place that chooses. A problem
  drawn into an interview is scaffolded blank, so writing it into `solutions/` would destroy
  a solution the person already has. The workspace also withholds its own previous
  solutions, archived attempts, diff and ratings for that problem, because each is the
  answer or a route to it.
- **The interviewer is a `tutor.Role`, not a prompt tacked onto the tutor.** The role is
  part of `sessionKey` and of `tutor_sessions`, so a restored session comes back in the
  right persona; `tutor/INTERVIEW.md` is its protocol, canonical the way `TUTOR.md` is; and
  neither the recorded hint ladder nor `turnContext` reaches it.
- **Anything the interviewer is meant to say goes out as `SendAs(prompt, "")`, not
  `SendInternal`.** An internal turn is published as `internal-text`, which `solve.js`
  skips, so the opening and the time-up line both looked like the interviewer saying
  nothing. `SendInternal` is right only where the reply is parsed rather than read — the
  submission decision and the final report.
- **The language is the interview's, chosen once on the way in.** `s.language(p)` returns
  it while the interview is live, so a problem studied in Go is interviewed in Python if
  that is the button that was pressed, and the study row is not rewritten. The draw filters
  out problems that cannot be scaffolded in it, which today means Python without a stub:
  the Go scaffold writes a "write the entry point yourself" file instead of failing.

Two things move the interview without a click, so the page polls `/interview/state` and
follows: a submission the interviewer answers with `"satisfied"` closes that problem
through `advanceInterview`, and the feedback at the end is written in the background.
Finishing stamps `Finishing` on the payload and answers the request at once — writing the
report inside the request meant a browser sitting on a dead page, because it is a model
turn and takes as long as it takes. The row stays live while that runs, which is what stops
a second interview starting in the gap, and `interviewHere` returns false for it, which is
what takes the workspace out of interview mode.

The state machine lives in the `interviews` row's JSON payload (`interviewState`), with one
live interview per person enforced by a partial unique index rather than by a handler
check. The time-up turn is bought once, and the flag that makes it once is on the row: the
browser keeps the clock and reports expiry, so a reloaded tab would otherwise buy another.
Submissions and the final report both go through the decision-JSON shape the submission
flow uses, and the report is written from the notes stored per problem rather than from a
transcript, which may be gone by then.

A statement's trailing **Follow up:** question is held back from every page.
`content.SplitFollowUp` takes it off, `Doc.FollowUp` keeps it, and the prompts carry it
privately with `followUpBriefing` attached — 22 of the 150 have one, and reading "could you
do this in one pass?" before starting gives away both that a better solution exists and
roughly where it is. That is the question to ask once the obvious solution works, which
makes it the tutor's to time and the interviewer's natural complication. The CLI providers
read `problem.md` themselves, so they are told the rule rather than kept from the text.

Two rules the site and the CLI share rather than each having their own:

- **`progress.ClockMinutes`** is what a sitting is worth — under a minute counts for
  nothing, and anything past `MaxSitting` (six hours) is a forgotten tab and counts for
  nothing either. Every path that turns a clock into minutes goes through it: `nc rate`,
  cancelling an attempt, submitting one, and rating from the reading page.
- **`progress.LastArchivedIn`** is which earlier attempt a comparison should use, and it
  is where the same-language rule lives — a Go attempt against a Python solution is noise,
  not a diff. `nc diff` and the workspace both call it. `LastArchived` is the convenience
  wrapper for callers that do not already hold the rows; `handleSolve` does hold them, from
  `submissions`, and passes them in rather than querying twice.

The workspace's History tab holds the ratings table (so it stays readable once an attempt
starts and the record pane is swapped out for the editor) and the comparison against the
last archived attempt. That comparison is recomputed from the run response on a passing
run — one rendered at page load is stale as soon as the editor is touched — and the
rendering is `createElement` and `textContent`, never `innerHTML`. The reading page carries
the same ratings table, undo, and a box for logging time the clock missed.

What the rate form asks is deliberately coarser than what the schedule stores. It offers
four buttons in words — `rateOutcome` in `internal/web/server.go`, which the template
ranges over so a label cannot describe a different rating from the one it submits — and
each sets the confidence *and* the rung. Confidence and rung are separate axes only where
something can judge them separately: the tutor has the transcript, and the gap between the
two is the generosity flag. Asked of a person they came apart the wrong way, a rating being
how it felt once the answer was in view. The four are the four moves `applyRating` has —
reset, lapse, hold, advance — so the 1-5 scale and the rung select cost nothing on the way
out, and no button can now submit a generous pair. Amending by hand went with them: it
means naming both numbers again. `nc rate` keeps the full range, and is the way to rate a
problem on an instance with no tutor. Manual time logging is deliberately *not* in the workspace: that page
runs the clock itself, and a box for adding minutes beside a live timer invites double
counting.

The site serves nobody without a cookie. `/join/<token>` trades a link for one, and
`nc user` mints them — there is no admin page. Only a token's hash is stored, which is why
a link can be reissued but never shown again.

The web surface cannot *invent* an identity, which is a narrower rule than it used to be
and the narrowing is deliberate. It can create one only by spending a use of an invite
(`nc invite new`, `migrateV6`): before that row's deadline, within its count, with the
ceiling written on it. Everything that bounds the damage is a column rather than a
convention, so there is no path where a forgotten default means unlimited — `CreateInvite`
refuses a ceiling of zero outright, because zero means *no* ceiling in `tutorBudgetLeft`
and an invite is the last place that should hand one out. Whoever comes in that way gets
their own 128-bit token immediately, so they are revocable one at a time like anybody
else. The alternative that was weighed and rejected was a standing shared password: it
cannot be revoked per person, it mints rows without limit (each with its own monthly
allowance), and it would need rate limiting that has to key on a forwarded header this
file already says not to trust. There is no exception for loopback or for a missing cookie: behind a reverse
proxy every request looks local, so an exception would make a guest who clears their
cookie into the owner. Each person's notes, attempts, ratings and test runs are their
own, and so is the tutor: sessions are keyed by person as well as problem.

**Nothing on the site crosses that boundary.** There was a `/people/` page ranking effort —
problems finished, days worked, minutes spent — and it is gone, with the generated
pseudonyms and the rename form that only existed to serve it.

It never showed a rating or a hint rung, which was the right constraint: those drive the
review schedule, and a table that rewarded a high rating gives you a reason to under-report
a hint. What was left after that constraint was a scoreboard of the two columns that were
safe to show, and it was the only reason a name had to be visible to anyone. A name is now
a label in the owner's `nc user list`, and `progress.Summarize` is called for one person at
a time. Before adding any comparison across people, work out what it does to the ratings.

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

The editor is a textarea whose affordances are written out in `static/solve.js` —
indent and outdent over a selection, self-closing pairs, indentation carried across a
newline — because no third-party editor code is served to the browser. Two consequences
worth keeping: Tab is trapped for indentation, so **Esc must keep releasing focus** or the
textarea becomes a keyboard trap; and the format key (`⌘⇧F`, `POST …/format`) neither saves
nor runs, so it is safe on code that does not parse. What formats is `lang.Formatter()`,
which names the tool or returns "" — `lang.Format` returns Python unchanged with no error,
and a caller offering formatting as an action cannot otherwise tell that from source that
needed no change.

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
tutor/                           the tutoring and interview protocols, and the references
```

Note what is gitignored: on a fresh clone there are no statements until `nc fetch` runs,
and `solutions/`, `study.sqlite3` (including WAL/SHM), and legacy personal data never
leave the machine.
Do not add them to version control, and do not propose committing them.

## Conventions

- **Go 1.26.** `gofmt` is not negotiable and CI fails on an unformatted file.
- **Python 3.12** for the harness and for Python solutions; standard library only, no
  pip, no pytest. Nothing in the loop *requires* a Python formatter: test runs and
  submissions never reformat Python, and `lang.Format` returns it unchanged. The
  workspace's format key will use `ruff` if the machine happens to have it, and says what
  is missing when it does not — an optional convenience, never a dependency, and never on
  the path that decides whether a solution passes.
- **Dependencies.** Tooling uses goldmark, chroma, `golang.org/x/net`, and the pure-Go
  `modernc.org/sqlite` driver. Solutions depend on nothing — if a solution needs a third-party
  package, the solution is wrong, in either language.
- **Table-driven tests**, matching what `nc start` generates.
- Match the surrounding comment density. This codebase explains *why*, not *what*; do not
  add comments that restate the code.
- `content/problems/*/problem.md` is fetched output. Do not edit it, except for the seven
  premium problems whose statements are hand-written and tracked.
