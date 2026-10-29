---
title: Encode and Decode Strings
id: 271
slug: encode-and-decode-strings
pattern: Arrays & Hashing
difficulty: Medium
blind75: true
topics: Array, String, Design
leetcode: https://leetcode.com/problems/encode-and-decode-strings/
video: https://www.youtube.com/watch?v=B1k_sxOSgv8
---

# 271. Encode and Decode Strings

Design an algorithm that encodes a list of strings into a single string, and a matching algorithm that decodes that single string back into the original list.

The two halves run on different machines, so `decode` receives nothing but the string `encode` produced. Any string content is allowed, including the empty string and whatever delimiter you were hoping to use.

**Example 1:**

```
Input:  ["neet", "code", "love", "you"]
Output: ["neet", "code", "love", "you"]
```

**Example 2:**

```
Input:  ["we", "say", ":", "yes"]
Output: ["we", "say", ":", "yes"]
```

Note that `":"` appears as an element in its own right, so a scheme that splits on `:` loses information.

**Example 3:**

```
Input:  ["", ""]
Output: ["", ""]
```

**Constraints:**

- `0 <= strs.length <= 200`
- `0 <= strs[i].length <= 200`
- `strs[i]` contains any possible characters out of 256 valid ASCII characters.

**Follow-up:** Could you write a solution that does not depend on any character being absent from the input?
