---
title: Trees
---

# Trees

A tree is a recursive structure, so the solution is almost always a recursive function whose body answers one question: **what do I need from my children, and what do I return to my parent?** Get that contract right and the code is four lines.

Choose the traversal by what the answer depends on. If a node's answer needs its children's answers, go bottom-up (post-order). If children need information from above — a valid range, a running path — pass it down (pre-order). If the answer is about distance from the root, go level by level (BFS).

## When to reach for it

- Depth, diameter, balance, subtree sums — **post-order**: recurse first, combine after.
- Validate a BST, path sums, "is this a subtree" — **pre-order**: carry context down.
- Level order, minimum depth, right-side view, widest level — **BFS with a queue**.
- Anything on a BST where order matters — **in-order** traversal visits values ascending.
- Lowest common ancestor: recurse both sides and reason about what comes back.

## The shape

```go
// Post-order: the child results are the whole input to your decision.
func depth(root *TreeNode) int {
    if root == nil {
        return 0 // the base case does the work of every leaf check
    }
    return 1 + max(depth(root.Left), depth(root.Right))
}
```

```go
// Returning two things at once: the local answer, and the global best so far.
// This is how diameter, max path sum and "is balanced" are done in one pass.
func solve(node *TreeNode) (height int) {
    if node == nil {
        return 0
    }
    l, r := solve(node.Left), solve(node.Right)
    best = max(best, l+r) // the path through this node, for the outer answer
    return 1 + max(l, r)  // what the parent actually needs
}
```

```go
// Pre-order with a carried range: a BST is not just locally ordered.
func valid(node *TreeNode, low, high *int) bool {
    if node == nil {
        return true
    }
    if (low != nil && node.Val <= *low) || (high != nil && node.Val >= *high) {
        return false
    }
    return valid(node.Left, low, &node.Val) && valid(node.Right, &node.Val, high)
}
```

```go
// BFS: take the level's size first, then drain exactly that many.
queue := []*TreeNode{root}
for len(queue) > 0 {
    n := len(queue) // freeze the boundary before appending children
    for range n {
        node := queue[0]
        queue = queue[1:]
        if node.Left != nil {
            queue = append(queue, node.Left)
        }
        if node.Right != nil {
            queue = append(queue, node.Right)
        }
    }
}
```

## Complexity

`O(n)` time for any full traversal. Space is `O(h)` for recursion, where `h` is the height — `O(log n)` balanced, `O(n)` for a degenerate list-shaped tree. BFS is `O(w)` for the widest level.

On a BST, search and insert are `O(h)`, which is the reason to use one at all.

## Go mechanics

- The node type is given by the problem; do not redefine it:
  ```go
  type TreeNode struct {
      Val         int
      Left, Right *TreeNode
  }
  ```
- `nil` is a valid empty tree. Handle it first in every recursive function and most edge cases disappear.
- Named return values make the two-things-at-once shape readable.
- A closure over an outer variable is the clean way to accumulate a global answer without threading a pointer through every call.
- For unbounded range checks, `*int` with `nil` meaning "no bound" beats `math.MinInt`, which breaks when a node legitimately holds that value.
- `queue = queue[1:]` is an `O(1)` dequeue. It leaks the front of the backing array, which is fine at these sizes.

## Where it goes wrong

- Validating a BST against the parent only. The constraint is a *range* inherited from every ancestor.
- Confusing the value you return to the parent with the value you record globally — they are usually different, and that is the point.
- Appending children to the BFS queue before freezing the level size, which merges two levels.
- Forgetting that an empty tree has depth 0 while a single node has depth 1.
- Recursing without a `nil` guard and panicking on the first leaf.
