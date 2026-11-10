"""JSON fixtures to values and back (internal/testkit/fixtures.go)."""

import copy
from collections import deque

from . import nodes

# Output traversals are bounded even when an incorrect solution grows cycles.
MAX_NODES = 100_000


class FixtureError(Exception):
    pass


def build(raw, kind):
    """Turn one JSON argument into the value a solution expects.

    `kind` is what the scaffold derived from the signature's annotation: "",
    "tree", "list", "graph", "random", or any of those followed by "[]" for a
    list of them. Go gets this from reflection on the parameter type; Python
    has no static type to read, and the user may edit the annotations.
    """
    if kind.endswith("[]"):
        if not isinstance(raw, list):
            raise FixtureError("expected a list of %s fixtures" % kind[:-2])
        return [build(x, kind[:-2]) for x in raw]
    if kind == "":
        # A fresh copy every time, as Go decodes afresh: a solution that
        # mutates its input must not change the fixture for the next run, and
        # the policies that re-read the original input rely on it.
        return copy.deepcopy(raw)
    if not isinstance(raw, list):
        raise FixtureError("%s fixture must be an array" % kind)
    if not raw:
        return None
    if kind == "tree":
        return build_tree(raw)
    if kind == "list":
        return build_list(raw)
    if kind == "graph":
        return build_graph(raw)
    if kind == "random":
        return build_random(raw)
    raise FixtureError("unknown argument kind %r" % kind)


def build_tree(data):
    if data[0] is None:
        return None
    root = nodes.TreeNode(data[0])
    queue = deque([root])
    pos = 1
    while queue and pos < len(data):
        n = queue.popleft()
        for side in ("left", "right"):
            if pos >= len(data):
                break
            value = data[pos]
            pos += 1
            if value is None:
                continue
            child = nodes.TreeNode(value)
            setattr(n, side, child)
            queue.append(child)
    if pos < len(data):
        raise FixtureError("unreachable tree fixture values")
    return root


def build_list(data):
    head = None
    for value in reversed(data):
        head = nodes.ListNode(value, head)
    return head


def build_graph(data):
    out = [nodes.GraphNode(i + 1) for i in range(len(data))]
    for node, edges in zip(out, data):
        for v in edges:
            if not isinstance(v, int) or v < 1 or v > len(out):
                raise FixtureError("invalid graph neighbor %s" % (v,))
            node.neighbors.append(out[v - 1])
    return out[0]


def build_random(data):
    out = []
    for pair in data:
        if not isinstance(pair, list) or len(pair) != 2:
            raise FixtureError("random node needs value and pointer")
        out.append(nodes.RandomNode(pair[0]))
    for i, (node, pair) in enumerate(zip(out, data)):
        if i + 1 < len(out):
            node.next = out[i + 1]
        idx = pair[1]
        if idx is None:
            continue
        if not isinstance(idx, int) or idx < 0 or idx >= len(out):
            raise FixtureError("invalid random pointer %s" % (idx,))
        node.random = out[idx]
    return out[0]


def list_nodes(head):
    out = []
    seen = set()
    n = head
    while n is not None:
        if id(n) in seen:
            raise FixtureError("returned list contains a cycle")
        if len(out) >= MAX_NODES:
            raise FixtureError("returned list exceeds fixture limit")
        seen.add(id(n))
        out.append(n)
        n = getattr(n, "next", None)
    return out


def node_set(root):
    """Every node reachable from root, keyed by identity.

    The dict holds the nodes themselves, not just their ids: id() is only
    unique among live objects, so a set of bare ids could match a node that
    has since been freed and its address reused.
    """
    seen = {}
    queue = deque([root])
    while queue:
        n = queue.popleft()
        if n is None or id(n) in seen or nodes.shape(n) is None:
            continue
        seen[id(n)] = n
        for attr in ("next", "random", "left", "right"):
            if hasattr(n, attr):
                queue.append(getattr(n, attr))
        for m in getattr(n, "neighbors", None) or []:
            queue.append(m)
        if len(seen) > MAX_NODES:
            break
    return seen


def find_node(root, value):
    for n in node_set(root).values():
        if n.val == value:
            return n
    return None


def normalize(v):
    """Reduce a value to plain data the way the fixtures spell it.

    None stands in for Go's nil pointer and becomes an empty list, which is
    how an empty tree or list is written in the JSON.
    """
    if v is None:
        return []
    kind = nodes.shape(v)
    if kind == "tree":
        return normalize_tree(v)
    if kind == "graph":
        return normalize_graph(v)
    if kind == "random":
        return normalize_random(v)
    if kind == "list":
        return [n.val for n in list_nodes(v)]
    if isinstance(v, (list, tuple)):
        return [normalize(x) for x in v]
    # Anything else is handed to the JSON round trip, which names what it
    # cannot carry.
    return v


def normalize_tree(root):
    out = []
    queue = deque([root])
    seen = set()
    while queue:
        n = queue.popleft()
        if n is None:
            out.append(None)
            continue
        if id(n) in seen or len(seen) >= MAX_NODES:
            raise FixtureError("returned tree contains a cycle or shared node")
        seen.add(id(n))
        out.append(n.val)
        queue.append(getattr(n, "left", None))
        queue.append(getattr(n, "right", None))
    while out and out[-1] is None:
        out.pop()
    return out


def normalize_graph(root):
    found = node_set(root)
    out = [None] * len(found)
    used = set()
    for n in found.values():
        val = n.val
        if not isinstance(val, int) or val < 1 or val > len(found) or val in used:
            raise FixtureError("invalid or duplicate cloned node value %s" % (val,))
        used.add(val)
        edges = []
        for m in n.neighbors or []:
            if m is None:
                raise FixtureError("nil graph neighbor")
            edges.append(m.val)
        out[val - 1] = sorted(edges)
    return out


def normalize_random(root):
    chain = list_nodes(root)
    indices = {id(n): i for i, n in enumerate(chain)}
    out = []
    for n in chain:
        idx = None
        r = getattr(n, "random", None)
        if r is not None:
            if id(r) not in indices:
                raise FixtureError("random pointer points outside cloned list")
            idx = indices[id(r)]
        out.append([n.val, idx])
    return out
