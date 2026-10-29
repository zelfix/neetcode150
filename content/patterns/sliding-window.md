---
title: Sliding Window
---

# Sliding Window

A window `[l, r]` over the sequence, carrying a running summary of what is inside it. The right edge expands to take in more; when the window violates the condition, the left edge contracts until it is valid again. Each index enters once and leaves once, so the total work is linear despite the nested-looking loops.

The whole craft is in one question: **what do I store so that expanding and contracting are both `O(1)`?**

## When to reach for it

- "Longest / shortest / count of **contiguous** subarray or substring such that…"
- A brute force that re-examines every substring (`O(n^2)` or `O(n^3)`).
- The condition is *monotone*: once a window is too big it stays too big as you keep growing it.
- Fixed-size windows — the condition is just "length `k`".

## The shape

```go
// Variable window: grow right, shrink left until valid, measure.
counts := make(map[byte]int)
best, l := 0, 0
for r := 0; r < len(s); r++ {
    counts[s[r]]++
    for !valid(counts) { // invariant: after this loop the window is valid
        counts[s[l]]--
        if counts[s[l]] == 0 {
            delete(counts, s[l]) // keep len(counts) meaningful
        }
        l++
    }
    best = max(best, r-l+1)
}
```

```go
// Fixed window of size k: add the new, drop the old, no inner loop.
for r := range nums {
    sum += nums[r]
    if r >= k {
        sum -= nums[r-k]
    }
    if r >= k-1 {
        best = max(best, sum)
    }
}
```

## Complexity

`O(n)` time: `l` and `r` each advance at most `n` times in total, so the inner `for` is amortised, not nested. Space is whatever the summary costs — `O(1)` for a sum or a `[26]int`, `O(k)` for a map of distinct elements.

## Go mechanics

- Delete keys that hit zero, or `len(counts)` stops meaning "distinct characters in the window".
- `[26]int` or `[128]int` instead of a map when the alphabet is known: no allocation, much faster, and you compare whole arrays with `==`.
- `max` and `min` are builtins since Go 1.21 — no helper needed.
- For "window contains all of t", track a single `missing` counter rather than comparing two maps every step. Comparing maps per character is what turns a linear solution quadratic.

## Where it goes wrong

- Measuring the answer inside the shrink loop instead of after it.
- Shrinking with `if` instead of `for`, so one step is not enough to restore the invariant.
- Updating the summary for the entering element but forgetting the leaving one.
- Reaching for a window when the subarray need not be contiguous — that is a different problem, usually sorting or dynamic programming.
- A non-monotone condition (for example "sum equals k" with negative numbers). A window cannot decide that; use prefix sums with a hash map.
