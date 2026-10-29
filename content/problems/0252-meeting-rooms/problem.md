---
title: Meeting Rooms
id: 252
slug: meeting-rooms
pattern: Intervals
difficulty: Easy
blind75: true
topics: Array, Sorting
leetcode: https://leetcode.com/problems/meeting-rooms/
video: https://www.youtube.com/watch?v=PaJxqZVPhbg
---

# 252. Meeting Rooms

You are given an array of meeting time intervals, where `intervals[i] = [start_i, end_i]`.

Determine whether a person could attend all of the meetings — that is, whether any two of them overlap.

A meeting that ends exactly when another begins does not conflict: `[0,8]` and `[8,10]` are fine.

**Example 1:**

```
Input:  intervals = [[0,30],[5,10],[15,20]]
Output: false
```

`[0,30]` overlaps both of the others.

**Example 2:**

```
Input:  intervals = [[7,10],[2,4]]
Output: true
```

**Example 3:**

```
Input:  intervals = [[0,8],[8,10]]
Output: true
```

**Constraints:**

- `0 <= intervals.length <= 10^4`
- `intervals[i].length == 2`
- `0 <= start_i < end_i <= 10^6`
