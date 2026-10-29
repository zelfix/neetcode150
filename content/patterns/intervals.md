---
title: Intervals
---

# Intervals

Almost every interval problem begins the same way: **sort, then sweep**. Unsorted intervals have no local structure, so comparing them is quadratic. Sorted, each one only ever needs to be compared against the one you are currently holding.

The second decision is the one that matters: sort by **start** when you are merging or inserting, and by **end** when you are choosing a maximum non-overlapping set. Choosing the wrong key is the usual reason a correct-looking solution fails.

## When to reach for it

- Merge overlapping intervals, insert one into a sorted list.
- "Minimum removals so none overlap", "maximum meetings attended" — sort by end.
- "How many rooms / resources at once" — a sweep over start and end events, or a min-heap of end times.
- "Can one person attend all of these" — sort and check adjacent pairs.
- Any scheduling or booking question.

## The shape

```go
// Merge: sort by start, then extend or emit.
slices.SortFunc(intervals, func(a, b []int) int { return a[0] - b[0] })
out := [][]int{intervals[0]}
for _, in := range intervals[1:] {
    last := out[len(out)-1]
    if in[0] <= last[1] { // they touch or overlap
        last[1] = max(last[1], in[1]) // extend in place
    } else {
        out = append(out, in)
    }
}
```

```go
// Non-overlapping maximum: sort by END, keep what fits.
slices.SortFunc(intervals, func(a, b []int) int { return a[1] - b[1] })
count, lastEnd := 0, math.MinInt
for _, in := range intervals {
    if in[0] >= lastEnd { // starts after the one we kept
        count++
        lastEnd = in[1]
    }
}
// removals = len(intervals) - count
```

```go
// Concurrency, as a sweep over separated endpoints.
starts := make([]int, len(intervals))
ends := make([]int, len(intervals))
for i, in := range intervals {
    starts[i], ends[i] = in[0], in[1]
}
slices.Sort(starts)
slices.Sort(ends)
rooms, most, j := 0, 0, 0
for i := range starts {
    for starts[i] >= ends[j] { // a room freed before this meeting begins
        rooms--
        j++
    }
    rooms++
    most = max(most, rooms)
}
```

```go
// The same thing with a min-heap of end times: reuse the earliest-freed room.
for _, in := range intervals { // sorted by start
    if h.Len() > 0 && (*h)[0] <= in[0] {
        heap.Pop(h) // that room is free
    }
    heap.Push(h, in[1])
}
return h.Len()
```

## Complexity

`O(n log n)` from the sort, then `O(n)` for the sweep. The heap version is `O(n log n)` too. Space is `O(n)` for the output, `O(1)` beyond that if you merge in place.

## Go mechanics

- Intervals are usually `[][]int`. `slices.SortFunc` with `a[0] - b[0]` is the concise comparator; use `cmp.Compare(a[0], b[0])` if the values could be large enough to overflow a subtraction.
- `out[len(out)-1]` returns the inner slice, which shares its backing array — so `last[1] = ...` really does mutate the element already in `out`. Convenient here, and worth knowing before it surprises you elsewhere.
- Decide what touching means. `[1,2]` and `[2,3]` overlap for merging (`in[0] <= last[1]`) but do not conflict for scheduling (`in[0] >= lastEnd`). The problem statement always says which; read it twice.
- `math.MinInt` as the initial `lastEnd` avoids a special case on the first interval.

## Where it goes wrong

- Sorting by start when choosing a maximum non-overlapping set. Earliest-start greedily picks a long interval that blocks several short ones.
- Mixing up `<` and `<=` at a shared endpoint, which is a one-character difference between two valid answers.
- Forgetting `max(last[1], in[1])` when merging — a fully contained interval would truncate the one holding it.
- Indexing `intervals[0]` without checking the input is non-empty.
- Keeping start and end paired when the question is about concurrency. Separating and sorting them independently is what makes the sweep work.
