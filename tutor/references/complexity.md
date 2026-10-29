# Reading the complexity target off the constraints

The constraint block tells you which complexity class is expected. Use it at step 2,
before choosing an approach — it narrows the field faster than staring at the examples.

| `n` up to | Budget | What fits |
|---|---|---|
| 10–12 | `O(n!)` | Permutations, brute-force assignment |
| 20–25 | `O(2^n)` | Subsets, bitmask DP |
| 100 | `O(n^4)` | Four nested loops, interval DP on a small range |
| 500 | `O(n^3)` | Interval DP, Floyd-Warshall, matrix chain |
| 3,000–5,000 | `O(n^2)` | Two nested loops, 2-D DP, LIS the quadratic way |
| 10^5 | `O(n log n)` | Sorting, heap, binary search per element, trees |
| 10^6 | `O(n)` | One pass, hash map, sliding window, prefix sums |
| 10^9 and above | `O(log n)` or `O(1)` | Binary search on the answer space, maths |

A value of `10^9` in the constraints usually bounds the *values*, not the count. If the
array length is `10^5` but elements run to `10^9`, you cannot index by value — that is a
nudge toward sorting or hashing rather than counting.

## Common upgrades

| From | To | How |
|---|---|---|
| `O(n^2)` pair search | `O(n)` | Hash map of what you have seen |
| `O(n^2)` pair search on sorted data | `O(n)` | Two pointers, `O(1)` space |
| `O(n^3)` triples | `O(n^2)` | Fix one, two-pointer the rest |
| `O(n^2)` substring scan | `O(n)` | Sliding window |
| `O(n)` lookup | `O(log n)` | Binary search on sorted input |
| `O(n log n)` full sort for top-k | `O(n log k)` | Bounded heap |
| `O(2^n)` recursion | `O(n^2)` or `O(n)` | Memoise, then tabulate |
| Repeated range sums | `O(1)` per query | Prefix sums |

## Space

- Recursion costs `O(depth)`. On a tree that is `O(h)` — `O(log n)` balanced, `O(n)`
  degenerate. On a `250×250` grid DFS it is fine; Go grows stacks dynamically.
- A hash map of `n` entries is `O(n)`. If the problem says `O(1)` extra space, that is a
  ban on the map and usually a hint toward two pointers, in-place marking, or XOR.
- An output array does not normally count toward "extra" space. Read the follow-up
  carefully — it often says explicitly.

## Stating it

Give both time and space, and say what `n` is. "`O(n log n)` time where `n` is the number
of intervals, `O(n)` space for the sorted copy" is an answer. "`O(n log n)`" alone is not,
because on a two-sequence problem it does not say which sequence.
