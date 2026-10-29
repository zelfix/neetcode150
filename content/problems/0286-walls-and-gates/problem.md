---
title: Walls And Gates
id: 286
slug: walls-and-gates
pattern: Graphs
difficulty: Medium
blind75: false
topics: Array, Breadth-First Search, Matrix
leetcode: https://leetcode.com/problems/walls-and-gates/
video: https://www.youtube.com/watch?v=e69C6xhiSQE
---

# 286. Walls And Gates

You are given an `m x n` grid `rooms` initialised with these three possible values:

- `-1` is a wall or an obstacle.
- `0` is a gate.
- `2147483647` (`math.MaxInt32`) is an empty room. Treat it as infinity.

Fill each empty room with the distance to its *nearest* gate, counting one step per move between orthogonally adjacent cells. If a room cannot reach any gate, leave it as `2147483647`.

Modify `rooms` in place.

**Example 1:**

```
Input:  rooms = [[2147483647,-1,0,2147483647],
                 [2147483647,2147483647,2147483647,-1],
                 [2147483647,-1,2147483647,-1],
                 [0,-1,2147483647,2147483647]]

Output: [[3,-1,0,1],
         [2,2,1,-1],
         [1,-1,2,-1],
         [0,-1,3,4]]
```

**Example 2:**

```
Input:  rooms = [[-1]]
Output: [[-1]]
```

**Example 3:**

```
Input:  rooms = [[2147483647,-1],[2147483647,2147483647]]
Output: [[2147483647,-1],[2147483647,2147483647]]
```

With no gate anywhere, every empty room stays at infinity.

**Constraints:**

- `m == rooms.length`
- `n == rooms[i].length`
- `1 <= m, n <= 250`
- `rooms[i][j]` is `-1`, `0`, or `2147483647`.
