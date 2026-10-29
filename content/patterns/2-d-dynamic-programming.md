---
title: 2-D Dynamic Programming
---

# 2-D Dynamic Programming

Same method as the one-dimensional case, but the state needs two coordinates: a position in each of two sequences, or a position plus a remaining budget. `dp[i][j]` is a sentence about *both* indices, and getting that sentence right is still the whole job.

A useful habit: draw the grid, fill in the first row and column by hand, then work out one interior cell. The recurrence that produces your hand-filled cell is the one to write.

## When to reach for it

- Two strings compared: edit distance, longest common subsequence, interleaving, regular-expression matching.
- One sequence plus a budget: 0/1 knapsack, target sum, partition into equal subsets.
- Paths through a grid with obstacles or costs.
- Intervals on a sequence: burst balloons, matrix chain — `dp[i][j]` over a *range* rather than a prefix.
- Counting distinct subsequences or decodings of a pair of strings.

## The shape

```go
// Two sequences. dp[i][j] = answer for the first i of a and first j of b.
dp := make([][]int, len(a)+1)
for i := range dp {
    dp[i] = make([]int, len(b)+1)
}
for i := 1; i <= len(a); i++ {
    for j := 1; j <= len(b); j++ {
        if a[i-1] == b[j-1] {
            dp[i][j] = dp[i-1][j-1] + 1 // consume one of each
        } else {
            dp[i][j] = max(dp[i-1][j], dp[i][j-1]) // drop one or the other
        }
    }
}
return dp[len(a)][len(b)]
```

```go
// Edit distance: three moves, one cell each.
dp[i][j] = 1 + min(
    dp[i-1][j],   // delete from a
    dp[i][j-1],   // insert into a
    dp[i-1][j-1], // replace
)
```

```go
// 0/1 knapsack, rolled to one row. Iterate the budget DOWNWARD so each
// item is used at most once — upward would let it be reused.
dp := make([]bool, target+1)
dp[0] = true
for _, v := range nums {
    for t := target; t >= v; t-- {
        dp[t] = dp[t] || dp[t-v]
    }
}
return dp[target]
```

```go
// Interval DP: by increasing length, so both halves are already known.
for length := 2; length <= n; length++ {
    for i := 0; i+length-1 < n; i++ {
        j := i + length - 1
        for k := i; k <= j; k++ {
            dp[i][j] = max(dp[i][j], split(i, k, j))
        }
    }
}
```

## Complexity

`O(m · n)` time and space for the two-sequence shape. Space drops to `O(min(m, n))` when each row only reads the row above — keep two rows, or one row updated in the right direction. Interval DP is `O(n^3)`: `n^2` states, `O(n)` split points each.

## Go mechanics

- A 2-D slice needs a loop to allocate each row; `make([][]int, m)` alone gives you `m` nil slices.
  ```go
  dp := make([][]int, m+1)
  for i := range dp {
      dp[i] = make([]int, n+1)
  }
  ```
- For one big allocation, back it with a flat slice and index `flat[i*n+j]`. Worth it only when it matters.
- The `+1` dimension lets the empty prefix be row 0 and column 0, which removes every boundary branch. Take it.
- **Loop direction decides reuse.** Downward over the budget means each item is used once (0/1 knapsack); upward means unlimited use (coin change). This single line is the difference between two different problems.
- `min` and `max` are builtins and take any number of arguments, so the three-way edit-distance `min` needs no helper.
- `a[i-1]` with a 1-offset table: the table index is `i`, the string index is `i-1`. Keep that straight and most off-by-ones vanish.

## Where it goes wrong

- Allocating rows but not columns, then panicking on the first write.
- Rolling to one row while still reading a value that this row has already overwritten.
- Iterating the knapsack budget upward and silently solving the unbounded problem instead.
- An interval DP looped by `i` and `j` directly instead of by length, so a subproblem is read before it is computed.
- Base cases left at zero when the empty prefix has a real cost — in edit distance, `dp[i][0]` is `i`, not 0.
- Confusing subsequence (gaps allowed) with substring (contiguous). They have different recurrences.
