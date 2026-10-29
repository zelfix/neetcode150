---
title: Advanced Graphs
---

# Advanced Graphs

Once edges carry weights, plain BFS stops working — the fewest edges is no longer the cheapest route. This group is the small set of named algorithms that handle weights, plus two problems about *traversing edges* rather than visiting nodes.

There are only four things to know, and each has one tell. **Dijkstra**: cheapest path, non-negative weights. **Bellman-Ford**: cheapest path with at most `k` steps, or negative weights. **Prim or Kruskal**: connect everything for the least total cost. **Hierholzer**: use every edge exactly once.

## When to reach for it

- "Cheapest / fastest / minimum cost path" with weights — **Dijkstra**.
- "…with at most k stops", or weights can be negative — **Bellman-Ford**, relaxing all edges `k` times.
- "Connect all the points for the minimum total cost" — **minimum spanning tree**.
- "Use every ticket / traverse every edge exactly once" — **Eulerian path**, Hierholzer's algorithm.
- Networks of delay, swimming through rising water, climbing with minimum effort — Dijkstra where the "distance" is a max along the path rather than a sum.

## The shape

```go
// Dijkstra: a min-heap on (cost, node). The first time a node is popped,
// its cost is final — that is the whole invariant.
dist := make([]int, n)
for i := range dist {
    dist[i] = math.MaxInt
}
dist[src] = 0
pq := &MinHeap{{0, src}}
for pq.Len() > 0 {
    cur := heap.Pop(pq).(Item)
    if cur.cost > dist[cur.node] {
        continue // a stale entry, already improved on
    }
    for _, e := range adj[cur.node] {
        if next := cur.cost + e.weight; next < dist[e.to] {
            dist[e.to] = next
            heap.Push(pq, Item{next, e.to})
        }
    }
}
```

```go
// Bellman-Ford: k rounds of relaxing every edge. Each round extends
// the best known path by at most one more edge — which is exactly how
// "at most k stops" is enforced.
for range k {
    next := slices.Clone(dist) // read from the previous round only
    for _, e := range edges {
        if dist[e.from] != math.MaxInt && dist[e.from]+e.weight < next[e.to] {
            next[e.to] = dist[e.from] + e.weight
        }
    }
    dist = next
}
```

```go
// Prim: grow one tree, always taking the cheapest edge leaving it.
visited := make([]bool, n)
pq := &MinHeap{{0, 0}}
total, count := 0, 0
for pq.Len() > 0 && count < n {
    cur := heap.Pop(pq).(Item)
    if visited[cur.node] {
        continue
    }
    visited[cur.node] = true
    total += cur.cost
    count++
    for _, e := range adj[cur.node] {
        if !visited[e.to] {
            heap.Push(pq, Item{e.weight, e.to})
        }
    }
}
```

```go
// Kruskal: sort every edge, take it if it joins two different components.
slices.SortFunc(edges, func(a, b Edge) int { return a.weight - b.weight })
dsu := NewDSU(n)
for _, e := range edges {
    if dsu.Union(e.from, e.to) { // false means it would close a cycle
        total += e.weight
    }
}
```

```go
// Hierholzer: append the node after its edges are exhausted, then reverse.
func visit(airport string) {
    for len(dests[airport]) > 0 {
        next := dests[airport][0]            // smallest first, for lexical order
        dests[airport] = dests[airport][1:]  // consume the ticket
        visit(next)
    }
    route = append(route, airport) // post-order is what makes this correct
}
```

## Complexity

Dijkstra with a binary heap is `O((V + E) log V)`. Bellman-Ford is `O(V · E)`, or `O(k · E)` when the problem caps the hops. Kruskal is `O(E log E)` from the sort; Prim is `O(E log V)`. Hierholzer is `O(E)`.

## Go mechanics

- `math.MaxInt` as infinity. Guard before adding to it or you wrap around to a negative — the commonest Bellman-Ford bug.
- Go's `container/heap` has no decrease-key, so Dijkstra pushes duplicates and skips stale pops with the `cur.cost > dist[node]` check. That is the standard Go idiom, not a workaround.
- Bellman-Ford must relax against the **previous** round's distances when hops are capped. Writing into the same slice lets one round use two edges.
- `slices.SortFunc` with a comparison returning an int is the current way to sort by a field.
- Dijkstra where cost is "the maximum edge on the path" (swim in rising water) works unchanged — replace `cost + weight` with `max(cost, weight)`.
- For Hierholzer, sort each adjacency list once up front, then consume from the front.

## Where it goes wrong

- Dijkstra with negative weights. The finality invariant breaks; use Bellman-Ford.
- Marking nodes visited when you *push* in Dijkstra rather than when you pop, which can lock in a worse cost.
- Forgetting the stale-entry check, so already-finalised nodes are reprocessed.
- Relaxing in place in Bellman-Ford when hops are limited.
- Appending the node before recursing in Hierholzer — the route comes out wrong, and it must be reversed at the end.
- Using an MST algorithm for a shortest-path question. The cheapest tree is not the cheapest route between two nodes.
