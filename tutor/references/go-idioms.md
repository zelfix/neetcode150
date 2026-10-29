# Go for algorithm problems

The mechanics these 150 problems need, and the traps specific to Go. Reach for this when
the obstacle is the language rather than the algorithm.

## Builtins worth knowing

```go
max(a, b)            // builtins since Go 1.21, any number of arguments
min(a, b, c)
clear(m)             // empties a map or zeroes a slice
for i := range 10 {} // range over an integer, Go 1.22+
```

## Slices

```go
s := make([]int, 0, n)      // length 0, capacity n: append without reallocating
s = append(s, v)
s = s[:len(s)-1]            // pop
slices.Clone(s)             // a real copy — see the aliasing trap below
slices.Sort(s)
slices.SortFunc(s, func(a, b T) int { return a.field - b.field })
slices.Reverse(s)
slices.Contains(s, v)
slices.Max(s), slices.Min(s)
slices.BinarySearch(s, v)   // returns (index, found)
```

**The aliasing trap.** `append` may reuse the backing array, so two slices can share
memory. This is the single most common source of wrong answers in Go backtracking:

```go
out = append(out, path)                // WRONG: every entry aliases the same array
out = append(out, slices.Clone(path))   // right
```

A sub-slice shares memory too: `b := a[1:3]` then `b[0] = 9` changes `a[1]`. Convenient
when you want it, a bug when you do not.

## Maps

```go
m := make(map[string]int, n)
m[k]++                 // a missing key reads as the zero value, no init needed
v, ok := m[k]
delete(m, k)
len(m)
```

- Iteration order is randomised on purpose. Sort the keys if you need order.
- `map[T]struct{}` is a set with no value storage; `map[T]bool` reads better and the
  difference rarely matters at these sizes.
- **Arrays are comparable, slices are not.** `[26]int` can be a map key, `[]int` cannot —
  which is the clean way to group by letter frequency.
- When the key range is small and known, a fixed array beats a map by a wide margin:
  `var count [26]int`.

## Strings, bytes and runes

```go
s[i]                  // a BYTE, not a character
len(s)                // bytes, not characters
for i, r := range s   // r is a RUNE, i is the byte offset — the two differ
[]byte(s), []rune(s)  // both copy
string(b)
```

- For ASCII problems, index bytes: it is simpler and faster.
- For anything Unicode, convert to `[]rune` first, or `len` and indexing will mislead you.
- `s[i] - 'a'` gives a 0-based letter index.
- Build output with `strings.Builder`, not `+=` in a loop:
  ```go
  var b strings.Builder
  b.WriteByte(c)
  b.WriteString(s)
  return b.String()
  ```
- `strings.Split`, `strings.Fields`, `strings.TrimSpace`, `strings.Repeat`,
  `strconv.Itoa`, `strconv.Atoi`.

## Nodes

The problem supplies these; the scaffold copies them from the official snippet. Do not
redefine them.

```go
type ListNode struct {
    Val  int
    Next *ListNode
}

type TreeNode struct {
    Val         int
    Left, Right *TreeNode
}
```

`nil` is a valid empty list or tree. Guard every dereference, and rely on `&&`
short-circuiting: `for fast != nil && fast.Next != nil`.

## Heaps

`container/heap` is an interface, not a type. Five methods, and two of them must be on
the pointer receiver:

```go
type MinHeap []int

func (h MinHeap) Len() int           { return len(h) }
func (h MinHeap) Less(i, j int) bool { return h[i] < h[j] } // reverse for a max-heap
func (h MinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *MinHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *MinHeap) Pop() any {
    old := *h
    n := len(old)
    item := old[n-1]   // the LAST element, not the first
    *h = old[:n-1]
    return item
}
```

Then use the **package functions**, never the methods:

```go
h := &MinHeap{3, 1, 2}
heap.Init(h)              // O(n)
heap.Push(h, 5)
v := heap.Pop(h).(int)
top := (*h)[0]            // peek, O(1)
heap.Fix(h, i)            // after mutating element i in place
```

Two mistakes to watch for: writing `Pop` to return `old[0]` (the package has already
swapped the root to the end), and calling `h.Push(x)` instead of `heap.Push(h, x)`, which
skips the sifting and leaves you with an unordered slice.

## Queues

A slice is enough. `queue = queue[1:]` is an `O(1)` dequeue — it leaks the front of the
backing array, which does not matter at these sizes.

```go
for len(queue) > 0 {
    n := len(queue) // freeze the level BEFORE appending children
    for range n {
        node := queue[0]
        queue = queue[1:]
        // ...
    }
}
```

## Numbers

```go
math.MaxInt, math.MinInt, math.MaxInt32, math.MinInt32
math.Abs(float64)          // floats only; for ints write it yourself
```

- `int` is 64-bit on these platforms. An `int32` overflow problem has to be checked
  against `math.MaxInt32` deliberately.
- `%` keeps the sign of the dividend: `-7 % 10` is `-3`, not `7`.
- Integer division truncates toward zero. Ceiling division: `(a + b - 1) / b`.
- Guard before adding to a `math.MaxInt` sentinel, or it wraps negative.

## Bits

```go
a & b    a | b    a ^ b    a &^ b   // and, or, xor, and-not
^x                                   // NOT — Go has no ~
x << n   x >> n
n & (n - 1)                          // clear the lowest set bit
```

**Right shift on a signed int is arithmetic** — it copies the sign bit, so a loop over
the bits of a negative number never ends. Convert to `uint32` for bit-pattern work.

`math/bits` has `OnesCount32`, `Reverse32`, `LeadingZeros32`, `TrailingZeros32` when you
are not writing the loop as an exercise.

## Recursion

Declare before assigning, or the closure cannot call itself:

```go
var dfs func(node *TreeNode) int
dfs = func(node *TreeNode) int {
    if node == nil {
        return 0
    }
    return 1 + max(dfs(node.Left), dfs(node.Right))
}
```

A closure over an outer variable is the idiomatic way to accumulate a global answer
without threading a pointer through every call.

## Tests

```go
func TestSolve(t *testing.T) {
    tests := []struct {
        name string
        in   []int
        want int
    }{
        {"example 1", []int{1, 2}, 3},
        {"empty", nil, 0},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            if got := solve(tc.in); got != tc.want {
                t.Errorf("got %v, want %v", got, tc.want)
            }
        })
    }
}
```

Use `reflect.DeepEqual` for slices and maps — `==` does not compare them. For results
whose order is unspecified, sort both sides before comparing, or the test fails on a
correct answer.
