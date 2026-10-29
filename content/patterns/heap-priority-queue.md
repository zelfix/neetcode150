---
title: Heap / Priority Queue
---

# Heap / Priority Queue

A heap keeps the extreme element at hand — minimum or maximum — for `O(log n)` insert and `O(log n)` removal. It deliberately does *not* keep everything sorted, which is why it is cheaper than a sorted structure when you only ever need the next-best item.

Two uses dominate. **Top-k**: keep a heap of exactly `k` elements and discard anything that cannot make the cut, which is `O(n log k)` rather than `O(n log n)` for a full sort. And **merging or scheduling**: repeatedly take the smallest head among many sequences.

## When to reach for it

- "k largest / k smallest / k-th / k closest / k most frequent."
- A stream, where the data arrives over time and must be sorted again after each arrival.
- Merging several sorted sequences.
- A running median — two heaps facing each other, max-heap below and min-heap above.
- Scheduling by always taking the next-soonest or next-biggest task.
- Dijkstra and Prim, where the frontier must yield its cheapest edge.

## The shape

```go
// container/heap is an interface, not a type: you supply the backing store.
type MinHeap []int

func (h MinHeap) Len() int            { return len(h) }
func (h MinHeap) Less(i, j int) bool  { return h[i] < h[j] } // flip for a max-heap
func (h MinHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *MinHeap) Push(x any)         { *h = append(*h, x.(int)) }
func (h *MinHeap) Pop() any {
    old := *h
    n := len(old)
    item := old[n-1]
    *h = old[:n-1]
    return item
}
```

```go
// Use the package functions, never the methods directly.
h := &MinHeap{5, 2, 8}
heap.Init(h)             // O(n), cheaper than n pushes
heap.Push(h, 3)
smallest := heap.Pop(h).(int)
peek := (*h)[0]          // O(1), no removal
```

```go
// Top-k with a bounded min-heap: evict as soon as it overflows.
for _, v := range nums {
    heap.Push(h, v)
    if h.Len() > k {
        heap.Pop(h) // drops the smallest, so the k largest survive
    }
}
// (*h)[0] is now the k-th largest.
```

## Complexity

Push and pop are `O(log n)`; peeking at the root is `O(1)`; `heap.Init` is `O(n)`. Top-k over `n` elements with a bounded heap is `O(n log k)` time and `O(k)` space.

For k-th smallest in a static array, quickselect is `O(n)` on average and beats a heap — but a heap handles a stream, and quickselect does not.

## Go mechanics

- `container/heap` needs five methods. `Push` and `Pop` must be on the **pointer** receiver because they change the slice length; `Len`, `Less` and `Swap` can be on the value.
- `heap.Pop` returns the root. The `Pop` *method* you write returns the **last** element — the package swaps the root to the end before calling it. Writing that method to return `old[0]` is the classic mistake.
- Always call `heap.Push(h, x)` and `heap.Pop(h)`, not `h.Push(x)` / `h.Pop()`. The methods skip the sifting that maintains the invariant.
- A max-heap is a min-heap with `Less` reversed. For a max-heap of ints you can also push `-v` and negate on the way out.
- For a heap of structs, make `Less` compare the field you care about; ties are broken arbitrarily unless you compare a second field.
- `heap.Fix(h, i)` after mutating an element in place, instead of removing and re-adding.

## Where it goes wrong

- `Pop` returning the first element instead of the last.
- Value receivers on `Push`/`Pop`, so the heap silently never grows.
- Calling the methods instead of the package functions, so the heap is just an unordered slice.
- Building a heap of all `n` elements when a bounded heap of `k` would do — correct, but it throws away the whole point.
- Expecting iteration over the slice to be sorted. Only the root is guaranteed.
- Forgetting `heap.Init` after constructing a heap from existing unordered data.
