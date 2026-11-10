# Python for algorithm problems

The mechanics these 150 problems need, and the traps specific to Python. Reach for this
when the obstacle is the language rather than the algorithm. The practice harness runs
your `solution.py` with the standard library only; there is no `numpy` and no `pytest`.

## Builtins worth knowing

```python
max(a, b), min(a, b, c)      # any number of arguments, or one iterable
sum(xs), abs(x), divmod(a, b)
sorted(xs), reversed(xs)     # new sequence; xs.sort() and xs.reverse() are in place
enumerate(xs, start=0), zip(xs, ys), range(n), range(lo, hi, step)
any(it), all(it)
```

## Lists and slicing

```python
xs = [0] * n                 # n zeros
xs.append(v); xs.pop()       # O(1) at the end; xs.pop(0) is O(n)
xs[i:j], xs[::-1]            # slices COPY
xs[-1]                       # the last element
grid = [[0] * cols for _ in range(rows)]
```

**The aliasing trap.** `[[0] * n] * m` makes `m` references to one row; writing to
`grid[0][0]` changes every row. Use the comprehension above. The same trap applies to
backtracking: `out.append(path)` stores a reference that keeps changing, so store a copy,
`out.append(path[:])` or `list(path)`.

`del xs[i]` and `xs.insert(i, v)` are O(n). A sorted list with `bisect.insort` is fine
at these sizes but it is still O(n) per insert; a heap is O(log n).

## Dicts, defaultdict, Counter

```python
from collections import defaultdict, Counter
d = {}
d[k] = d.get(k, 0) + 1
count = defaultdict(int); count[k] += 1      # a missing key reads as the zero value
groups = defaultdict(list); groups[k].append(v)
c = Counter(s); c.most_common(k)
k in d, d.pop(k, None), len(d)
```

- Dicts keep insertion order (3.7+), which is what an LRU cache or a stable grouping
  relies on. `next(iter(d))` is the oldest key; `d.popitem(last=False)` needs an
  `OrderedDict` for the oldest, but `move_to_end` does too, so LRU usually uses one.
- Keys must be hashable: a `tuple` of counts works as a key, a `list` does not. So group
  anagrams by `tuple(sorted(s))` or by a `tuple` of 26 counts.
- A `set` is a dict without values: `seen = set(); seen.add(v); v in seen`.
- When the key range is small and known, a list beats a dict: `count = [0] * 26`.

## Strings and characters

```python
s[i]                   # a one-character string; there is no char type
ord(c) - ord('a')      # 0-based letter index
chr(ord('a') + i)
s.lower(), s.isalnum(), s.split(), s.strip()
''.join(parts)         # build output from a list, not with += in a loop
```

Strings are immutable: `s[i] = 'x'` is an error. Build a `list` of characters and join
at the end. Comparing two strings with `==` is O(n); hashing one is O(n) too, once.

## Nodes

The problem supplies these; the scaffold copies them from the official snippet. Do not
redefine them differently, and do not import them from anywhere: the harness recognises
a node by its attributes (`val`, `next`, `left`, `right`, `neighbors`, `random`).

```python
class ListNode:
    def __init__(self, val=0, next=None):
        self.val = val
        self.next = next

class TreeNode:
    def __init__(self, val=0, left=None, right=None):
        self.val = val
        self.left = left
        self.right = right
```

`None` is a valid empty list or tree. Guard every attribute access, and rely on `and`
short-circuiting: `while fast and fast.next:`.

## Heaps

`heapq` works on a plain list and is a **min**-heap only:

```python
import heapq
h = []
heapq.heappush(h, (key, item))   # tuples compare element by element
key, item = heapq.heappop(h)
h[0]                              # peek
heapq.heapify(xs)                 # O(n), in place
heapq.nlargest(k, xs), heapq.nsmallest(k, xs)
```

For a max-heap, push the negated key: `heappush(h, (-key, item))` and negate on the
way out. When two items can share a key and the items themselves are not comparable
(two `ListNode`s, say), add a tiebreaker: `(key, counter, item)`.

## Queues

`collections.deque` gives O(1) at both ends; `list.pop(0)` does not.

```python
from collections import deque
q = deque([root])
while q:
    for _ in range(len(q)):       # freeze the level BEFORE appending children
        node = q.popleft()
        ...
```

A `deque` is also the right structure for a sliding-window maximum (`q[0]` is the
front, `q.pop()` the back).

## Numbers

```python
a // b, a % b           # floor division and a non-negative remainder for b > 0
-7 // 2 == -4           # rounds toward negative infinity, unlike Go and C
int(a / b)              # truncation toward zero, when the problem wants that
float('inf'), -float('inf'), math.inf
x.bit_length()
```

- Integers do not overflow. A problem that talks about 32-bit overflow has to be
  checked against `2**31 - 1` deliberately; the language will not do it for you.
- Because `%` is non-negative, a negative hash key needs no fix-up, and the clock
  arithmetic of a circular buffer just works.
- `0.1 + 0.2 != 0.3`. Compare floats with a tolerance, or stay in integers.

## Bits

```python
a & b, a | b, a ^ b, ~x, x << n, x >> n
n & (n - 1)             # clear the lowest set bit
bin(n).count('1'), n.bit_count()   # 3.10+
```

`~x` on an unbounded int is `-x - 1`, so to work with a 32-bit pattern mask it:
`x & 0xFFFFFFFF`, and read a negative result back with `x - (1 << 32)` when the top bit
is set. Right shift of a negative int never reaches zero; mask first.

## Recursion

The default recursion limit is about a thousand frames. The practice harness raises it,
but a linked list of ten thousand nodes recursed over is still a sign to iterate instead.

```python
def dfs(node):
    if not node:
        return 0
    return 1 + max(dfs(node.left), dfs(node.right))
```

A nested function can read an outer variable, but to assign one it needs `nonlocal`:

```python
best = 0
def dfs(node):
    nonlocal best
    ...
```

`functools.cache` (3.9+) memoises a function on its arguments, which turns a
top-down recurrence into dynamic programming in one line. The arguments must be
hashable, so pass indices rather than lists.

## Traps that are Python's alone

- **Mutable default arguments.** `def f(seen=[])` shares one list across every call.
  Use `None` and create inside.
- **`is` vs `==`.** `is` compares identity. Use it for `None` and for the ancestor
  problem's node identity; use `==` for values.
- **`bool` is an `int`.** `True == 1` and `sum([True, False])` is `1`. The harness
  does not accept `True` where `1` is expected, the same as the Go one would not.
- **Shadowing a module.** A file named `heapq.py` or a variable named `list` beside your
  solution breaks the import or the builtin. The harness isolates the interpreter so a
  file cannot, but a variable still can.
- **Late binding in loops.** Lambdas created in a loop all see the final loop variable;
  bind it as a default argument if you must.

## Tests

The scaffold's `test_solution.py` is a `unittest` table:

```python
import unittest
from solution import Solution

class TestTwoSum(unittest.TestCase):
    cases = [
        ("example 1", ([2, 7, 11, 15], 9), [0, 1]),
        ("no answer", ([1], 5), []),
    ]

    def test_cases(self):
        for name, args, want in self.cases:
            with self.subTest(name):
                self.assertEqual(Solution().twoSum(*args), want)
```

`practice_cases.py` beside it holds the examples and corner cases for all 150 problems;
the Run-tests button runs both. For results whose order is unspecified, sort both sides
before comparing, or the test fails on a correct answer.
