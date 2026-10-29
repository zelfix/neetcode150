---
title: Graphs
---

# Graphs

Most graph problems are not labelled as graph problems. A grid is a graph where each cell has up to four neighbours. A list of prerequisites is a graph. A set of words differing by one letter is a graph. The real work is **recognising the graph and choosing the traversal**; the traversal itself is the same every time.

Pick by what you need. DFS explores one path to its end — natural for connectivity, cycles and anything recursive. BFS explores by distance — the only correct choice for shortest path on an unweighted graph. Union-find answers "same component?" without traversing at all.

## When to reach for it

- Islands, regions, flood fill, surrounded areas — **DFS or BFS over a grid**.
- Shortest number of steps, minimum moves, fewest transformations — **BFS**, always.
- Prerequisites, build order, "can this be scheduled" — **topological sort** (and a cycle means no).
- "Are these connected", counting components with edges arriving over time — **union-find**.
- Rotting oranges, spreading infection, distance from several sources at once — **multi-source BFS**: seed the queue with every source before you start.

## The shape

```go
// Grid DFS: the visited check is the base case.
var dirs = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

func dfs(grid [][]byte, r, c int) {
    if r < 0 || r >= len(grid) || c < 0 || c >= len(grid[0]) || grid[r][c] != '1' {
        return
    }
    grid[r][c] = '0' // mark on entry, which is also the visited set
    for _, d := range dirs {
        dfs(grid, r+d[0], c+d[1])
    }
}
```

```go
// BFS by levels: the loop count is the distance.
queue := [][2]int{{sr, sc}}
visited[sr][sc] = true
steps := 0
for len(queue) > 0 {
    for range len(queue) { // one full level per step
        cell := queue[0]
        queue = queue[1:]
        for _, d := range dirs {
            nr, nc := cell[0]+d[0], cell[1]+d[1]
            if inBounds(nr, nc) && !visited[nr][nc] && passable(nr, nc) {
                visited[nr][nc] = true // mark on enqueue, not on dequeue
                queue = append(queue, [2]int{nr, nc})
            }
        }
    }
    steps++
}
```

```go
// Topological sort by indegree (Kahn): what is left over is a cycle.
queue := []int{}
for v := range n {
    if indegree[v] == 0 {
        queue = append(queue, v)
    }
}
order := []int{}
for len(queue) > 0 {
    v := queue[0]
    queue = queue[1:]
    order = append(order, v)
    for _, w := range adj[v] {
        indegree[w]--
        if indegree[w] == 0 {
            queue = append(queue, w)
        }
    }
}
if len(order) != n {
    return nil // a cycle kept some nodes from ever reaching indegree 0
}
```

```go
// Union-find with path compression and union by size.
type DSU struct{ parent, size []int }

func (d *DSU) Find(x int) int {
    for d.parent[x] != x {
        d.parent[x] = d.parent[d.parent[x]] // halve the path on the way up
        x = d.parent[x]
    }
    return x
}

func (d *DSU) Union(a, b int) bool {
    ra, rb := d.Find(a), d.Find(b)
    if ra == rb {
        return false // already together: this edge closes a cycle
    }
    if d.size[ra] < d.size[rb] {
        ra, rb = rb, ra
    }
    d.parent[rb] = ra
    d.size[ra] += d.size[rb]
    return true
}
```

## Complexity

DFS and BFS are `O(V + E)` — for a grid, `O(rows × cols)`. Topological sort is also `O(V + E)`. Union-find is effectively `O(1)` amortised per operation with both optimisations, so building components from `E` edges is `O(E)`.

## Go mechanics

- Package-level `dirs` as `[4][2]int` avoids rebuilding the neighbour list on every call.
- Mark visited **when you enqueue**, not when you dequeue. Marking on dequeue lets a node enter the queue many times and can make BFS quadratic.
- Mutating the input grid as the visited set is `O(1)` space and idiomatic — just be sure the caller tolerates it, and restore it if not.
- `adj := make(map[int][]int)` for sparse graphs; `[][]int` indexed by node when nodes are `0..n-1`.
- Deep recursion on a 250×250 grid is fine — Go grows goroutine stacks dynamically, so there is no fixed recursion limit to fear.
- `for range len(queue)` freezes the level size before the inner loop appends to it.

## Where it goes wrong

- Using DFS for a shortest path. It finds *a* path, not the shortest.
- Forgetting a visited set on a cyclic graph, and looping forever.
- Appending to the queue before taking the level size, which merges levels and corrupts the step count.
- Running BFS once per source when a multi-source BFS does it in one pass.
- Treating an undirected edge as one-directional. Add both `a→b` and `b→a`.
- Reading `grid[0]` without checking the grid is non-empty.
