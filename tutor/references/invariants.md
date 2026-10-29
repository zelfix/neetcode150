# Loop invariants

A loop invariant is a property that is true **at the loop boundary** — before the first
iteration, and again after every iteration. Not at every instant inside the body: the body
is allowed to break the property temporarily, as long as it has restored it by the time
control reaches the top of the loop again.

That one word, *boundary*, is what makes an invariant useful. It turns a loop — a thing
that runs an unknown number of times on unknown data — into something you can reason about
with a finite argument.

## Why it is the only way to know a loop is correct

A loop over `n` elements has `n+1` states you could inspect, and `n` is unbounded. You
cannot check them all. Tests sample a few inputs; an invariant covers every input, because
it is an inductive argument rather than an enumeration.

The practical difference:

- **Testing** tells you the code worked on the inputs you thought of.
- **An invariant** tells you the code works, and *why* — which means you can also say where
  it stops working. Every "what if the input were…" question has an answer you can derive
  rather than guess.

And you get the bug before you run anything. A mismatch between what you wrote down and
what the code does is visible by reading them side by side.

## The three-part proof

This is the standard framing (CLRS §2.1), and it is just mathematical induction over the
iteration count.

**1. Initialisation.** The invariant holds before the first iteration.
   Usually trivial: the processed prefix is empty, so the claim about it is vacuous.

**2. Maintenance.** *If* the invariant holds at the top of an iteration, *then* it holds
   again at the top of the next one. This is the inductive step, and it is the only part
   that takes thought. You reason about **one** iteration — never about the whole loop.

**3. Termination.** When the loop exits, the invariant is still true, and now the exit
   condition is true as well. The two together must give you exactly what you set out to
   compute. If they do not, your invariant was too weak, even if it was true.

Step 3 is where people are surprised. A true invariant that does not imply your goal is
useless. "The map contains some counts" is true and proves nothing.

Formally this is Hoare logic's rule for `while`:

```
            { I ∧ B } C { I }
    ─────────────────────────────────
    { I } while B do C { I ∧ ¬B }
```

Read it as: if running the body `C` from a state satisfying the invariant `I` and the
condition `B` leaves `I` true, then running the whole loop from `I` ends in a state
satisfying `I` and the negation of `B`. Everything above is this rule in prose.

## Partial correctness is not all of correctness

An invariant proves **partial correctness**: *if* the loop terminates, the result is right.
It says nothing about whether it terminates. `for { }` preserves every invariant you like.

Termination needs a separate argument — a **variant** (or measure): a quantity that is
bounded below and strictly decreases every iteration. For `for i := 0; i < n; i++` the
measure is `n - i`, which is why nobody mentions it. It matters when the loop is not a
simple count:

- Two pointers: `r - l` strictly decreases, because every branch moves one pointer inward.
- Binary search: `hi - lo` at least halves, so it reaches the exit in `O(log n)` steps.
- Sliding window: `l` only ever increases and is bounded by `n`, which is *also* the
  argument that the total work is `O(n)` rather than `O(n²)` — the amortised bound and the
  termination proof are the same observation.

When a loop hangs, the bug is in the measure, not the invariant.

## How to write one

Three parts, in this order:

1. **A moment in time.** "At the top of iteration `i`", "after processing the first `i`
   elements", "before handling bucket `f`".
2. **A claim about the state that mentions that variable.** This is the part people skip.
   If `i` appears only in the preamble and not in the claim, you have written a description
   of the final result, and there is nothing to induct on.
3. **Enough completeness.** "All and only those." Without it you cannot conclude anything at
   termination: maybe something is missing, maybe something extra crept in.

Two checks on what you wrote:

- **The `i` test.** Does the loop variable appear in the claim itself? If not, rewrite.
- **The induction test.** Knowing only what one iteration does, can you derive the claim
  for `i+1` from the claim for `i`? If not, the invariant is too weak — strengthen it,
  usually by saying more about what is *not* there.

Then read the invariant and the code side by side. Every line of the body should be
explicable as "this is what keeps the invariant true".

## Worked examples from this course

### Two Sum (1)

> At the top of iteration `i`, the map holds the key `target - num` for every `num` before
> index `i`, and each value is that element's index.

- *Initialisation*: `i = 0`, prefix empty, map empty. Vacuously true.
- *Maintenance*: the body looks up `nums[i]`, and if absent stores `target - nums[i] → i`.
  The claim for `i+1` follows.
- *Termination*: on a hit, `map[nums[i]]` is by the invariant the index of an earlier
  element that needed exactly `nums[i]`, so those two sum to `target`. That is the answer.

This is the invariant that found a real bug by reading: the code looked up `target - num`
while the invariant said the key to look up is `num`. The two did not match, and Example 2
would have failed. No test had been run yet.

### Valid Anagram (242)

Here the argument is about a sum rather than a prefix, and it shows what "enough
completeness" buys you:

- After both loops, the 26 counters sum to `len(s) - len(t)`, which the early length check
  forces to `0`.
- Only a letter appearing in `t` can have been decremented, and the body checks every one
  of those for negativity.
- So all 26 counters are non-negative, and non-negative numbers summing to zero are all
  zero. Equal multisets.

Notice that the length check, which looks like a cheap early exit, is load-bearing: remove
it and the sum is not zero and the whole argument collapses. An invariant tells you which
lines you are allowed to delete.

### Top K Frequent (347)

Three loops, three invariants:

1. After the first `i` elements: `counts` has as keys exactly the elements among the first
   `i`, each mapped to its occurrence count among the first `i`.
2. After distribution: `buckets[f]` holds all elements with frequency exactly `f`, and only
   those; each distinct element sits in exactly one bucket.
3. Before handling bucket `f`: `out` holds only elements with frequency strictly greater
   than `f`, and `len(out) < k`.

The third is the one that proves the answer is the top `k`. Walking downward, you cannot
reach a lower frequency before exhausting every higher one — so when `len(out)` hits `k`,
those are the `k` most frequent. Without that invariant, "walk the buckets backwards" is an
intention, not a proof.

### Sliding window (the pattern)

> At the top of each iteration, the window `[l, r]` is valid.

The body breaks it by extending `r`, then the inner `for` restores it by advancing `l`.
That is why the shrink step is a `for` and not an `if`: one step may not be enough to
restore the invariant, and the invariant is the specification of that inner loop.

## This is also what dynamic programming is

The reason to build this habit now, on easy problems, is that DP is nothing but invariants
with the table written down:

| Invariant vocabulary | DP vocabulary |
|---|---|
| The claim about state | the meaning of `dp[i]` — "the best achievable using the first `i`" |
| Initialisation | the base case |
| Maintenance | the recurrence |
| Termination | reading `dp[n]` |

Every DP bug is one of three things: a `dp[i]` whose meaning was never stated precisely, a
recurrence that does not preserve that meaning, or base cases that do not establish it.
"I cannot see the recurrence" almost always means "I have not defined the state".

So the sentence you write before a DP loop is not a study exercise — it is the solution.
The code is a transcription.

## Where to read more

**CLRS, *Introduction to Algorithms*, §2.1.** Two pages, introduces invariants on insertion
sort with exactly the initialisation/maintenance/termination structure. The standard
reference, and the shortest path to the idea. Start here.

**Jon Bentley, *Programming Pearls*, column 4 — "Writing Correct Programs".** Derives
binary search from its invariant, and is candid about how many published versions were
wrong. The most convincing argument for the technique, because the example is one everybody
thinks they know. Read it before you reach problem 28 (Binary Search).

**David Gries, *The Science of Programming* (1981).** The teaching book on this. Covers
predicate logic, Hoare triples, then deriving loops from invariants rather than writing a
loop and checking it afterwards.

**Edsger Dijkstra, *A Discipline of Programming* (1976).** Where the "derive the program
from the invariant" idea comes from. Dense, and worth it once the idea is familiar.

**C.A.R. Hoare, "An Axiomatic Basis for Computer Programming" (1969).** The original paper,
eleven pages, readable. Robert Floyd's "Assigning Meanings to Programs" (1967) precedes it.

**Rustan Leino, *Program Proofs* (MIT Press, 2023)**, with Dafny. You write the invariant
and a machine checks whether it is strong enough. Having a verifier reject a too-weak
invariant teaches step 3 faster than any amount of reading.
