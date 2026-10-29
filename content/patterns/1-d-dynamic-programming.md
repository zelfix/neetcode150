---
title: 1-D Dynamic Programming
---

# 1-D Dynamic Programming

Dynamic programming is recursion with the repeated work removed. If a brute-force recursion solves the same subproblem many times, give the subproblem a name, store its answer, and the exponential collapses to linear.

The honest way in is three steps. Write the **state**: what does `dp[i]` mean, in one sentence? Write the **recurrence**: how does `dp[i]` follow from smaller states? Write the **base case**. Only then write code. Skipping straight to code is why these feel impossible.

## When to reach for it

- "Maximum / minimum / number of ways" over a sequence of choices.
- A recursion where each step takes or skips an element, and subproblems overlap.
- "Can this be decomposed" — word break, partition into sums.
- Longest increasing subsequence, house robber, coin change, climbing stairs, decode ways.
- Constraints around `10^3`–`10^4` with an obviously exponential brute force.

## The shape

```go
// Define the state in words first:
//   dp[i] = the best achievable considering the first i elements.
dp := make([]int, len(nums)+1)
dp[0] = base
for i := 1; i <= len(nums); i++ {
    dp[i] = combine(dp[i-1], dp[i-2], nums[i-1])
}
return dp[len(nums)]
```

```go
// Take-or-skip. The two branches are the whole recurrence.
// dp[i] = most money robbable from houses i onward.
prev, curr := 0, 0
for _, v := range nums {
    prev, curr = curr, max(curr, prev+v) // skip this house, or take it and skip one
}
return curr
```

```go
// Unbounded choice — coin change. The inner loop is the set of moves.
dp := make([]int, amount+1)
for i := 1; i <= amount; i++ {
    dp[i] = amount + 1 // a sentinel meaning unreachable
    for _, c := range coins {
        if c <= i && dp[i-c]+1 < dp[i] {
            dp[i] = dp[i-c] + 1
        }
    }
}
if dp[amount] > amount {
    return -1
}
return dp[amount]
```

```go
// Quadratic 1-D: dp[i] depends on every earlier j. Longest increasing subsequence.
dp := make([]int, len(nums))
best := 0
for i := range nums {
    dp[i] = 1
    for j := range i {
        if nums[j] < nums[i] {
            dp[i] = max(dp[i], dp[j]+1)
        }
    }
    best = max(best, dp[i])
}
```

```go
// Memoised recursion, when the recurrence is easier to see top-down.
memo := make(map[int]int)
var solve func(i int) int
solve = func(i int) int {
    if i >= len(nums) {
        return 0
    }
    if v, ok := memo[i]; ok {
        return v
    }
    result := max(solve(i+1), nums[i]+solve(i+2))
    memo[i] = result
    return result
}
```

## Complexity

`O(n)` for a recurrence that looks back a constant number of steps, `O(n · k)` when each step tries `k` moves (coin change), `O(n^2)` when each state scans all earlier states (LIS). Space is `O(n)` for the table, and often `O(1)` once you notice only the last one or two entries are ever read — which is what the rolling-variable version above does.

## Go mechanics

- `make([]int, n)` zeroes the slice. That is free when 0 is your base case and a bug when it is not — fill explicitly with a sentinel otherwise.
- Rolling two variables with `prev, curr = curr, next` is the clean Go way to drop a table to `O(1)`.
- `max` and `min` are builtins since Go 1.21.
- For memoised recursion, declare `var solve func(int) int` **before** assigning the closure, or it cannot call itself.
- A `map` memo is fine for sparse states; a slice with a `-1` sentinel is faster when the state space is dense.
- Beware `dp[i-1]` when `i` is 0. Either offset the table by one (as in the first shape) or guard the index.

## Where it goes wrong

- Writing code before stating what `dp[i]` means. Nearly every wrong recurrence comes from a fuzzy state.
- Off-by-one between "first i elements" and "index i". Choose one and be consistent across the base case, the loop bounds and the return.
- A base case of 0 where the answer should be unreachable, so an impossible path looks achievable.
- Mixing up a rolling pair's update order, so `prev` is already overwritten when it is read.
- Looping over coins outside and amounts inside when counting *combinations* versus *permutations* — the loop order changes the answer.
