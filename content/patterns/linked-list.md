---
title: Linked List
---

# Linked List

Linked-list problems are pointer-discipline exercises. There is no indexing and no length, so everything is done with a handful of local pointers moved in the right order. The algorithms are simple; the bugs come from mutating `next` before you have saved it.

Two devices remove most of the difficulty: a **dummy head**, so inserting at the front needs no special case, and **two pointers at different speeds**, which find midpoints and cycles in one pass without knowing the length.

## When to reach for it

- Reverse, merge, reorder, or partition a list.
- Find the middle, the k-th from the end, or the start of a cycle — fast and slow pointers.
- Build a result list from one or more inputs — dummy head.
- Remove a node when you may be removing the first one — dummy head again.
- A problem says "in `O(1)` extra space", which rules out copying into a slice.

## The shape

```go
// Reversal: save, rewire, advance. Always in that order.
var prev *ListNode
curr := head
for curr != nil {
    next := curr.Next // save before you destroy it
    curr.Next = prev
    prev = curr
    curr = next
}
return prev // the old tail is the new head
```

```go
// Dummy head: build a list without special-casing the first node.
dummy := &ListNode{}
tail := dummy
for a != nil && b != nil {
    if a.Val <= b.Val {
        tail.Next, a = a, a.Next
    } else {
        tail.Next, b = b, b.Next
    }
    tail = tail.Next
}
if a != nil {
    tail.Next = a // at most one remainder, and it is already linked
} else {
    tail.Next = b
}
return dummy.Next
```

```go
// Fast and slow: fast moves two, slow moves one.
slow, fast := head, head
for fast != nil && fast.Next != nil {
    slow = slow.Next
    fast = fast.Next.Next
}
// slow is now the second middle of an even-length list.
// For a cycle: they meet iff there is one; restart one at head and
// advance both by one to find the entry point.
```

## Complexity

`O(n)` time, `O(1)` space for all of the above. Any list problem solved by copying into a slice is correct but misses the point — and the follow-up always asks for constant space.

## Go mechanics

- `*ListNode` is a pointer; the zero value is `nil`. Dereferencing nil panics, so guard every `.Next`.
- `fast != nil && fast.Next != nil` — the order matters. Go short-circuits `&&`, which is what keeps the second check safe.
- Go has no `delete`; dropping a node means pointing the previous node past it and letting the garbage collector do the rest.
- Multiple assignment is genuinely useful here: `tail.Next, a = a, a.Next` evaluates the right side first, so it does the right thing.
- `&ListNode{}` heap-allocates. A dummy head costs one allocation and saves a branch everywhere — take the trade.

## Where it goes wrong

- Overwriting `curr.Next` before saving it, which loses the rest of the list.
- Returning `head` after a reversal. `head` is the tail now; return `prev`.
- Returning `dummy` instead of `dummy.Next`.
- Not terminating the new tail, leaving a `next` pointing into the old structure and creating a cycle.
- Checking `fast.Next.Next != nil` directly, which panics when `fast.Next` is nil.
- Losing the node *before* the one you need to remove. Either keep a `prev`, or use a dummy head and look one ahead.
