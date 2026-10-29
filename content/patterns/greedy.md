---
title: Greedy
---

# Greedy

Take the locally best option at every step and never reconsider. When it works, it is far simpler and faster than dynamic programming — `O(n)` and no table. When it does not work, it fails silently on an input you did not think of, which is why greedy is the pattern that most needs an argument rather than an intuition.

Before committing, answer one question: **why can an early choice never need to be undone?** If you can state that in a sentence — "a later start can never beat an earlier one", "a running sum that has gone negative can only hurt" — greedy is safe. If you cannot, use DP.

## When to reach for it

- Maximum subarray, maximum product subarray — the running value only ever restarts.
- Jump game, gas station — track reachability or a running deficit, one pass.
- Scheduling and intervals sorted by end time.
- Partition labels, merge triplets — a decision once made is never revisited.
- Constraints near `10^5` with no obvious `O(n^2)` structure.

## The shape

```go
// Kadane: the running sum restarts whenever carrying it forward would hurt.
best, running := nums[0], nums[0]
for _, v := range nums[1:] {
    running = max(v, running+v) // start fresh, or extend
    best = max(best, running)
}
```

```go
// Reachability: how far can I get, given everything seen so far?
reach := 0
for i, jump := range nums {
    if i > reach {
        return false // this index was never reachable
    }
    reach = max(reach, i+jump)
}
return true
```

```go
// Running deficit: if the total is non-negative, a valid start exists,
// and it is just after the lowest point.
total, tank, start := 0, 0, 0
for i := range gas {
    diff := gas[i] - cost[i]
    total += diff
    tank += diff
    if tank < 0 {
        start, tank = i+1, 0 // nothing before here can work either
    }
}
if total < 0 {
    return -1
}
return start
```

```go
// Two signs: a max-product subarray flips when it meets a negative,
// so carry both extremes.
maxEnd, minEnd := 1, 1
for _, v := range nums {
    if v < 0 {
        maxEnd, minEnd = minEnd, maxEnd // the smallest becomes the largest
    }
    maxEnd = max(v, maxEnd*v)
    minEnd = min(v, minEnd*v)
    best = max(best, maxEnd)
}
```

## Complexity

`O(n)` and `O(1)` space for the one-pass shapes; `O(n log n)` when a sort comes first. That is the appeal — the same problems solved by DP in `O(n^2)` drop to linear once you prove the exchange argument.

## Go mechanics

- `max` and `min` are builtins, which makes these one-liners read well.
- Initialise from `nums[0]` rather than 0 for Kadane. Starting at 0 gives the wrong answer on an all-negative array — the classic failure.
- Guard `len(nums) == 0` before indexing `nums[0]`.
- `slices.SortFunc(xs, func(a, b T) int { ... })` when the greedy choice needs a sort key.
- Watch for overflow in product problems. These inputs are bounded, but the habit matters.

## Where it goes wrong

- Being greedy on a problem that needs DP. Coin change with arbitrary denominations is the standard counterexample: taking the largest coin first fails on `coins = [1,3,4], amount = 6`.
- Starting an accumulator at 0 when all inputs are negative.
- Sorting by the wrong key — for non-overlapping intervals it is the **end** time, not the start, and the difference is the whole problem.
- Resetting a running value but forgetting to also reset the index or counter that goes with it.
- Not checking global feasibility. In gas station, the local scan finds *where* to start, but only the total tells you *whether* a start exists.
