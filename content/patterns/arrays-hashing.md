---
title: Arrays & Hashing
---

# Arrays & Hashing

Almost every brute-force array solution is quadratic because it re-scans the array to answer a question it already had the information to answer. A hash map is the fix: it buys you *"have I seen this, and where?"* in constant time, which collapses a nested loop into a single pass.

The move is always the same. Ask what question the inner loop is answering, then store exactly enough while walking forward that the question becomes a lookup.

## When to reach for it

- "Has this value appeared before?" — a `map[T]bool` or `map[T]struct{}` set.
- "How many times?" — a `map[T]int` counter.
- "Where was it?" — a `map[T]int` of value to index.
- "Are these two things the same multiset?" — compare counters, not sorted copies.
- "Group these by some property" — `map[key][]T`, where the key is a canonical form.
- Constraints say `n <= 10^5` and the obvious solution is `O(n^2)`.

## The shape

```go
// The complement trick: at index i, the thing you need was already stored.
seen := make(map[int]int, len(nums))
for i, v := range nums {
    if j, ok := seen[target-v]; ok {
        return []int{j, i}
    }
    seen[v] = i // store after looking up, or an element pairs with itself
}
```

```go
// Canonical-form grouping: collapse each item to the key that defines its class.
groups := make(map[string][]string)
for _, s := range strs {
    var count [26]int
    for _, c := range s {
        count[c-'a']++
    }
    key := fmt.Sprint(count) // an array is comparable, so it can be the key directly
    groups[key] = append(groups[key], s)
}
```

## Complexity

One pass with constant-time lookups: `O(n)` time, `O(n)` space. The trade is always the same — you spend memory to stop re-reading. When the value range is small and known (lowercase letters, digits, a bounded integer range), swap the map for a fixed-size array and the constant factor drops a lot.

## Go mechanics

- `map[T]struct{}` is a set with no per-entry value; `map[T]bool` is more readable and the difference rarely matters.
- Reading a missing key returns the zero value, so `count[k]++` works without initialising.
- **Arrays are comparable, slices are not.** `[26]int` can be a map key; `[]int` cannot. This is the clean way to key by character frequency.
- Pre-size with `make(map[K]V, n)` when you know the count — it avoids rehashing.
- Map iteration order is deliberately randomised. Never depend on it; sort the keys if you need order.
- `for i, v := range nums` copies `v`. To mutate, index with `nums[i]`.

## Where it goes wrong

- Storing before looking up, so an element matches itself.
- Sorting to find duplicates (`O(n log n)`) when a set does it in `O(n)` — unless you need sorted output anyway.
- Using a slice as a key, or a struct containing a slice. It will not compile, and that is the compiler saving you.
- Reaching for a map when the index *is* the key: if values are bounded by `n`, you can often mark the array in place for `O(1)` space.
