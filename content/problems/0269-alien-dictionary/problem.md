---
title: Alien Dictionary
id: 269
slug: alien-dictionary
pattern: Advanced Graphs
difficulty: Hard
blind75: true
topics: Array, String, Depth-First Search, Breadth-First Search, Graph, Topological Sort
leetcode: https://leetcode.com/problems/alien-dictionary/
video: https://www.youtube.com/watch?v=6kTZYvNNyps
---

# 269. Alien Dictionary

There is a language that uses the English lowercase letters, but in an unknown order. The order of its alphabet is some permutation of `a` through `z`.

You are given `words`, a list of words from this language's dictionary, already sorted according to the rules of that unknown alphabet.

Return a string of the unique letters in `words`, sorted in the language's order. If there is more than one valid order, return any one of them. If the given ordering is impossible, return `""`.

A word `a` sorts before a word `b` when, at the first position where they differ, `a`'s letter comes earlier in the alien alphabet. If no position differs, the shorter word sorts first — so a dictionary where a word precedes its own prefix is impossible.

**Example 1:**

```
Input:  words = ["wrt","wrf","er","ett","rftt"]
Output: "wertf"
```

**Example 2:**

```
Input:  words = ["z","x"]
Output: "zx"
```

**Example 3:**

```
Input:  words = ["z","x","z"]
Output: ""
```

The ordering is contradictory, so no alphabet produces it.

**Example 4:**

```
Input:  words = ["abc","ab"]
Output: ""
```

`"ab"` is a prefix of `"abc"`, so it cannot sort after it.

**Constraints:**

- `1 <= words.length <= 100`
- `1 <= words[i].length <= 100`
- `words[i]` consists of only lowercase English letters.
