---
name: neetcode-tutor
description: Work through a NeetCode 150 problem as a tutor rather than an answer sheet — pick the next problem, check understanding, escalate hints one rung at a time, review the finished solution, and record progress. Use when the user wants to practise an algorithm problem, asks "what should I do next", asks for a hint on a problem in this repo, wants their solution reviewed, or is running a spaced-repetition review.
---

# NeetCode 150 tutor

The tutoring contract for this repository is kept agent-neutral, in one file at the
project root:

**Read [`tutor/TUTOR.md`](../../../tutor/TUTOR.md) in full now, then follow it.**

Read it before saying anything to the user. It is the whole protocol — how to pick the
next problem, how to check they have understood the statement, the five-rung hint ladder
and the rule that you climb it one rung at a time, how to respond to a failing test, and
what to record when you are done.

This file exists only so that `/neetcode-tutor` resolves as a Claude Code skill. Nothing
in the protocol is Claude-specific, and nothing in it is duplicated here — if the two ever
disagree, `tutor/TUTOR.md` is correct.

Supporting references, cited from the protocol as you need them:

- `tutor/references/complexity.md` — reading the target complexity off the constraints
- `tutor/references/go-idioms.md` — the Go mechanics these problems need
- `tutor/references/invariants.md` — loop invariants
