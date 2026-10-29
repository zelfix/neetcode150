---
title: Stack
---

# Stack

A stack is the right structure whenever the thing you are matching is **nested**, or whenever an element's answer depends on the nearest earlier element that beats it. "Most recent unresolved thing" is exactly what a stack gives you in `O(1)`.

Two distinct uses hide under one name. *Matching* pushes openers and pops on closers. *Monotonic stacks* keep the stack sorted by popping everything an arriving element dominates — and each pop is the moment you learn an answer.

## When to reach for it

- Brackets, nesting, expression evaluation, path simplification, encoded strings with nested groups.
- "Next greater / next smaller element", "days until warmer", "largest rectangle".
- Undo, or any "revert the last thing" behaviour.
- A recursive solution you are asked to write iteratively — the stack *is* the call stack.

## The shape

```go
// Matching: push what must be closed, and check the top on arrival.
pairs := map[byte]byte{')': '(', ']': '[', '}': '{'}
stack := []byte{}
for i := 0; i < len(s); i++ {
    open, isCloser := pairs[s[i]]
    if !isCloser {
        stack = append(stack, s[i])
        continue
    }
    if len(stack) == 0 || stack[len(stack)-1] != open {
        return false
    }
    stack = stack[:len(stack)-1]
}
return len(stack) == 0 // anything left is unclosed
```

```go
// Monotonic: the pop is where the answer appears.
// Stack holds indices with strictly decreasing values.
stack := []int{}
for i, v := range temps {
    for len(stack) > 0 && temps[stack[len(stack)-1]] < v {
        j := stack[len(stack)-1]
        stack = stack[:len(stack)-1]
        result[j] = i - j // i is the first index that beats j
    }
    stack = append(stack, i)
}
```

## Complexity

`O(n)` time for both shapes. In a monotonic stack every index is pushed once and popped at most once, so the inner loop is amortised constant. Space is `O(n)` worst case — a fully increasing input never pops.

## Go mechanics

- A slice is the idiomatic stack. Push with `append`, peek with `s[len(s)-1]`, pop with `s = s[:len(s)-1]`.
- **Always check `len(stack) > 0` before peeking.** Indexing an empty slice panics, and this is the single commonest bug here.
- Store *indices*, not values, whenever the answer involves a distance or a width.
- Popping with `s = s[:len(s)-1]` keeps the popped element alive in the backing array. That is harmless for ints; for pointers in a long-lived stack, nil the slot first.
- `strings.Builder` for assembling output, not `+=` in a loop.

## Where it goes wrong

- Returning `true` at the end without checking the stack is empty.
- `<` versus `<=` in a monotonic stack: it decides how equal elements are treated, which is usually exactly what the problem turns on.
- Pushing values when you need widths, then having no way to recover the position.
- Using a stack for a problem that needs the *minimum so far* rather than the most recent — that is a heap, or a second stack carrying running minima.
