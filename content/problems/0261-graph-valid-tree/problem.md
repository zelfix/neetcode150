---
title: Graph Valid Tree
id: 261
slug: graph-valid-tree
pattern: Graphs
difficulty: Medium
blind75: true
topics: Depth-First Search, Breadth-First Search, Union Find, Graph
leetcode: https://leetcode.com/problems/graph-valid-tree/
video: https://www.youtube.com/watch?v=bXsUuownnoQ
---

# 261. Graph Valid Tree

You have a graph of `n` nodes labelled from `0` to `n - 1`. You are given `n` and a list `edges`, where each `edges[i] = [a, b]` is an undirected edge between node `a` and node `b`.

Return `true` if these edges make up a valid tree, and `false` otherwise.

A valid tree is connected and has no cycle: every node is reachable from every other node, and by exactly one path.

**Example 1:**

```
Input:  n = 5, edges = [[0,1],[0,2],[0,3],[1,4]]
Output: true
```

**Example 2:**

```
Input:  n = 5, edges = [[0,1],[1,2],[2,3],[1,3],[1,4]]
Output: false
```

The edges `1-2`, `2-3`, `3-1` close a cycle.

**Example 3:**

```
Input:  n = 4, edges = [[0,1],[2,3]]
Output: false
```

There is no cycle, but the graph is in two pieces, so it is a forest rather than a tree.

**Constraints:**

- `1 <= n <= 2000`
- `0 <= edges.length <= 5000`
- `edges[i].length == 2`
- `0 <= a, b < n`
- `a != b`
- There are no self-loops or repeated edges.
