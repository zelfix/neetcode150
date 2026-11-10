"""Node classes the harness builds fixtures with.

These are deliberately separate from the classes in the user's solution.py.
The harness never isinstance-checks what a solution returns; it reads `val`,
`left`, `next` and the rest with getattr, so a solution's own TreeNode and this
one interoperate the way two Go structs with the same fields would not.
Importing these into the solution would also need the harness on the user's
sys.path, which is exactly what running under -I prevents.
"""


class TreeNode:
    def __init__(self, val=0, left=None, right=None):
        self.val = val
        self.left = left
        self.right = right


class ListNode:
    def __init__(self, val=0, next=None):
        self.val = val
        self.next = next


class GraphNode:
    def __init__(self, val=0, neighbors=None):
        self.val = val
        self.neighbors = neighbors if neighbors is not None else []


class RandomNode:
    def __init__(self, val=0, next=None, random=None):
        self.val = val
        self.next = next
        self.random = random


def shape(obj):
    """Which node family an object belongs to, by its attributes.

    The order matters the same way it does in the Go decoder: a random-pointer
    node also has `next`, so `random` is tested first.
    """
    if obj is None:
        return None
    if hasattr(obj, "left"):
        return "tree"
    if hasattr(obj, "neighbors"):
        return "graph"
    if hasattr(obj, "random"):
        return "random"
    if hasattr(obj, "next"):
        return "list"
    return None
