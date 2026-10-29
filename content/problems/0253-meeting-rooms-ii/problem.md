---
title: Meeting Rooms II
id: 253
slug: meeting-rooms-ii
pattern: Intervals
difficulty: Medium
blind75: true
topics: Array, Two Pointers, Greedy, Sorting, Heap (Priority Queue), Prefix Sum
leetcode: https://leetcode.com/problems/meeting-rooms-ii/
video: https://www.youtube.com/watch?v=FdzJmTCVyJU
---

# 253. Meeting Rooms II

You are given an array of meeting time intervals, where `intervals[i] = [start_i, end_i]`.

Return the minimum number of rooms needed to hold all of the meetings.

A meeting that ends exactly when another begins can reuse the same room.

**Example 1:**

```
Input:  intervals = [[0,30],[5,10],[15,20]]
Output: 2
```

`[0,30]` holds one room for its whole duration; `[5,10]` and `[15,20]` share the second.

**Example 2:**

```
Input:  intervals = [[7,10],[2,4]]
Output: 1
```

**Example 3:**

```
Input:  intervals = [[0,10],[10,20],[20,30]]
Output: 1
```

**Constraints:**

- `0 <= intervals.length <= 10^4`
- `intervals[i].length == 2`
- `0 <= start_i < end_i <= 10^6`
