---
title: Binary Search
---

# Binary Search

Halve the search space with every comparison. The array does not have to be sorted — what it must have is a **predicate that flips exactly once** across the range. Think of it as finding the boundary between "no" and "yes" rather than finding a value.

That reframing is what unlocks the hard problems in this group. "Smallest capacity that finishes in time", "minimum speed", "the one rotation point": none of them are lookups, all of them are boundary searches.

## When to reach for it

- Sorted input, and `O(log n)` is expected (constraints near `10^5`–`10^9`).
- The answer is a number in a known range, and checking a candidate is cheap and monotone — search the *answer space*, not the input.
- Rotated or partially-sorted arrays: one half is always sorted, which is enough to prune.
- Two sorted arrays and a median or k-th element.

## The shape

```go
// Boundary search: find the first index where pred is true.
// Invariant: pred is false everywhere below lo, true at or above hi.
lo, hi := 0, len(nums) // hi is one past the end: "not found" is len(nums)
for lo < hi {
    mid := lo + (hi-lo)/2
    if pred(mid) {
        hi = mid // mid might be the answer, so keep it
    } else {
        lo = mid + 1 // mid is definitely not
    }
}
return lo
```

```go
// Searching the answer space, where feasible() is monotone in capacity.
lo, hi := maxWeight, totalWeight
for lo < hi {
    mid := lo + (hi-lo)/2
    if feasible(mid) {
        hi = mid
    } else {
        lo = mid + 1
    }
}
return lo
```

```go
// Rotated array: decide which half is sorted, then whether the target is in it.
if nums[lo] <= nums[mid] { // left half is sorted
    if nums[lo] <= target && target < nums[mid] {
        hi = mid - 1
    } else {
        lo = mid + 1
    }
}
```

## Complexity

`O(log n)` for a plain search. When you binary-search the answer space, each probe costs a feasibility check, so the total is `O(n log(range))` — still far better than testing every candidate.

## Go mechanics

- `mid := lo + (hi-lo)/2` rather than `(lo+hi)/2`. Go's `int` is 64-bit on these platforms so overflow is unlikely, but the habit is free and the intent is clearer.
- `sort.SearchInts(nums, x)` and `slices.BinarySearch(nums, x)` exist and return the insertion point. Use them when you just need a lookup; write the loop when you need a custom predicate.
- `sort.Search(n, f)` is the standard library's version of exactly the boundary shape above.
- Integer division truncates toward zero, so `mid` is the lower middle. Ceiling division is `(a + b - 1) / b`.

## Where it goes wrong

- Mixing the two conventions. Pick one — `[lo, hi)` with `lo < hi`, or `[lo, hi]` with `lo <= hi` — and keep the updates consistent with it. Mixing them is how you get an infinite loop or an off-by-one.
- `hi = mid - 1` in a half-open loop, which can skip the answer.
- A predicate that is not actually monotone. Verify the flip happens once before trusting the search.
- Assuming the midpoint comparison alone resolves a rotated array. You must first establish which side is sorted.
- Duplicates when searching a rotated array: they can make both halves look sorted, and the worst case degrades to `O(n)`.
