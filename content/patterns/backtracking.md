---
title: Backtracking
---

# Backtracking

Build a candidate one choice at a time; when a choice cannot lead anywhere, undo it and try the next. It is depth-first search over the tree of partial solutions, and the "backtrack" is literally the line that undoes the move after the recursive call returns.

Every one of these problems is the same four decisions: what is a complete solution, what choices are available at this step, which choices can be pruned, and what has to be undone afterwards. Write those four down and the code follows.

## When to reach for it

- "Find **all** subsets / permutations / combinations / partitions."
- Constraint satisfaction: n-queens, sudoku, word search in a grid.
- Counting or enumerating paths through a space with rules.
- Constraints are small — `n <= 20` for subsets, `n <= 9` for permutations. Exponential is expected and the small bound is the hint.

## The shape

```go
// The skeleton. Every problem here is a variation on these six lines.
func backtrack(path []int, start int) {
    if isComplete(path) {
        out = append(out, slices.Clone(path)) // copy: path keeps changing
        return
    }
    for i := start; i < len(choices); i++ {
        if !allowed(choices[i]) {
            continue // prune
        }
        path = append(path, choices[i]) // choose
        backtrack(path, i+1)            // explore
        path = path[:len(path)-1]       // un-choose
    }
}
```

```go
// Subsets: every node of the recursion tree is itself an answer.
func subsets(nums []int, i int, path []int) {
    if i == len(nums) {
        out = append(out, slices.Clone(path))
        return
    }
    subsets(nums, i+1, append(path, nums[i])) // take it
    subsets(nums, i+1, path)                  // leave it
}
```

```go
// Permutations: any unused element may come next, so track usage.
for i, v := range nums {
    if used[i] {
        continue
    }
    used[i] = true
    path = append(path, v)
    permute()
    path = path[:len(path)-1]
    used[i] = false // undo both pieces of state
}
```

```go
// Skipping duplicates: sort first, then ignore a repeat at the same level.
if i > start && nums[i] == nums[i-1] {
    continue
}
```

## Complexity

Exponential by nature: `O(n · 2^n)` for subsets, `O(n · n!)` for permutations, where the extra `n` is the cost of copying each completed path. Pruning does not change the bound but routinely changes the runtime by orders of magnitude — in n-queens it is the difference between feasible and not.

## Go mechanics

- **`append` may reuse the backing array, so append the result without copying and every stored path can change under you.** Use `slices.Clone(path)`, or `append([]int{}, path...)`. This is the number-one bug in Go backtracking.
- `path = path[:len(path)-1]` is the undo. It keeps capacity, so the next append reuses the same memory — which is exactly why the clone above is required.
- Passing `append(path, v)` directly as an argument works for the take/leave shape but is risky for the loop shape: two siblings can share a backing array. Prefer explicit choose/un-choose.
- Grid search: mark the cell visited before recursing and restore it after. Writing a sentinel like `'#'` into the grid and putting the original character back is idiomatic and avoids a second structure.
- A closure capturing `out`, `path` and the input is cleaner than threading everything through parameters.

## Where it goes wrong

- Storing `path` instead of a copy of it. The output ends up full of identical, wrong slices.
- Undoing only part of the state — the path but not the `used` flag, or the grid cell but not the counter.
- Passing `i+1` where the problem allows reuse (then it is `i`), or `i` where it does not (then it is `i+1`).
- Deduplicating without sorting first, so equal elements are not adjacent and the skip never fires.
- No pruning at all on a problem that needs it, then wondering why it times out.
