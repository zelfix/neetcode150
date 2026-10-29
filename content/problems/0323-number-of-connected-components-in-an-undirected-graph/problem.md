---
title: Number of Connected Components In An Undirected Graph
id: 323
slug: number-of-connected-components-in-an-undirected-graph
pattern: Graphs
difficulty: Medium
blind75: true
topics: Depth-First Search, Breadth-First Search, Union Find, Graph
leetcode: https://leetcode.com/problems/number-of-connected-components-in-an-undirected-graph/
video: https://www.youtube.com/watch?v=8f1XPm4WOUc
---

# 323. Number of Connected Components In An Undirected Graph

You have a graph of `n` nodes labelled from `0` to `n - 1`. You are given `n` and a list `edges`, where each `edges[i] = [a, b]` is an undirected edge between node `a` and node `b`.

Return the number of connected components in the graph.

**Example 1:**

```
Input:  n = 5, edges = [[0,1],[1,2],[3,4]]
Output: 2
```

Nodes `0, 1, 2` form one component and nodes `3, 4` form another.

**Example 2:**

```
Input:  n = 5, edges = [[0,1],[1,2],[2,3],[3,4]]
Output: 1
```

**Example 3:**

```
Input:  n = 4, edges = []
Output: 4
```

An isolated node is a component of its own.

**Constraints:**

- `1 <= n <= 2000`
- `0 <= edges.length <= 5000`
- `edges[i].length == 2`
- `0 <= a, b < n`
- `a != b`
- There are no repeated edges.
