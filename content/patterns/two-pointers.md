---
title: Two Pointers
---

# Two Pointers

Two indices walk the array, and the gap between them carries the meaning. Because each pointer only ever moves forward (or they only ever move toward each other), the whole structure is linear even though it is deciding something about pairs.

This works when the data has an **order you can exploit to rule things out**. On a sorted array, if the pair at `(l, r)` sums too high, every pair using `r` is too high — so `r` can move left and never come back. That irreversible pruning is the pattern.

## When to reach for it

- The array is sorted, or sorting it does not destroy what you need.
- You are looking for a pair, a triple, or a palindromic property.
- You need `O(1)` extra space where a hash map would be `O(n)`.
- Two sequences are compared in lockstep (merging, subsequence checks).
- A fast and a slow pointer in the same direction: midpoint, cycle detection, in-place filtering.

## The shape

```go
// Converging: the sorted-pair search.
l, r := 0, len(nums)-1
for l < r {
    sum := nums[l] + nums[r]
    switch {
    case sum == target:
        return []int{l, r}
    case sum < target:
        l++ // only a bigger left value can help
    default:
        r-- // only a smaller right value can help
    }
}
```

```go
// Same direction: write lags behind read, compacting in place.
write := 0
for read := range nums {
    if keep(nums[read]) {
        nums[write] = nums[read]
        write++
    }
}
return nums[:write]
```

```go
// Skipping duplicates, the part that is always fiddly.
for l < r && nums[l] == nums[l-1] {
    l++
}
```

## Complexity

`O(n)` after an `O(n log n)` sort, with `O(1)` extra space. For triples, fix one element and run a two-pointer scan inside: `O(n^2)`, which beats the `O(n^3)` triple loop and is the expected answer whenever `n <= 3000`.

## Go mechanics

- `sort.Ints(nums)` sorts in place; `slices.Sort(nums)` is the modern equivalent and generic.
- `s[i]` on a string is a **byte**, not a rune. For ASCII problems that is what you want and it is fast. For anything Unicode, convert with `[]rune(s)` first.
- `unicode.IsLetter`, `unicode.IsDigit`, `unicode.ToLower` take runes; `byte` versions need a manual range check.
- Compacting in place and returning `nums[:write]` reuses the backing array — the caller's slice header is unchanged, which is why these problems ask you to return the new length.

## Where it goes wrong

- Forgetting `l < r` inside a nested duplicate-skip loop, and running off the end.
- Using `l <= r` when the two pointers must not land on the same element.
- Advancing both pointers when the comparison only justifies moving one.
- Deduplicating the fixed element but not the two moving ones, so triples repeat.
- Sorting when the problem needs original indices. Sort a slice of `struct{val, idx int}`, or use a map instead.
