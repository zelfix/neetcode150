"""Value comparison and its policies (internal/testkit/compare.go)."""

import copy
import json

from .fixtures import FixtureError


class CompareError(Exception):
    pass


def dumps(v):
    # Go's json.Marshal: no whitespace, so messages read the same.
    return json.dumps(v, separators=(",", ":"), sort_keys=True, allow_nan=False)


def json_value(v):
    """Round a value through JSON so tuples and lists compare by content.

    A value JSON cannot carry (a set, an object) is reported as the case's
    error rather than silently compared unequal.
    """
    try:
        return json.loads(json.dumps(v, allow_nan=False))
    except (TypeError, ValueError) as e:
        raise CompareError("cannot compare a %s: %s" % (type(v).__name__, e))


def equivalent(got, want, policy):
    got, want = json_value(got), json_value(want)
    if policy in ("unordered", "unordered-groups"):
        got = sort_array(got, policy == "unordered-groups")
        want = sort_array(want, policy == "unordered-groups")
    return equal_value(got, want, policy == "float")


def sort_array(v, inner):
    if not isinstance(v, list):
        return v
    if inner:
        v = [sort_array(x, False) for x in v]
    return sorted(v, key=dumps)


def sort_neighbors(v):
    if not isinstance(v, list):
        return v
    return [sort_array(x, False) for x in v]


def equal_value(a, b, tolerant):
    if isinstance(a, list):
        if not isinstance(b, list) or len(a) != len(b):
            return False
        return all(equal_value(x, y, tolerant) for x, y in zip(a, b))
    if isinstance(a, dict):
        if not isinstance(b, dict) or a.keys() != b.keys():
            return False
        return all(equal_value(a[k], b[k], tolerant) for k in a)
    # bool is an int in Python; after Go's JSON round trip it is not a number,
    # so True must not pass where 1 is wanted.
    if isinstance(a, bool) or isinstance(b, bool):
        return isinstance(a, bool) and isinstance(b, bool) and a == b
    if isinstance(a, (int, float)):
        if not isinstance(b, (int, float)):
            return False
        if not tolerant or b == 0:
            return a == b
        return abs(a - b) <= abs(b) * 1e-6
    return a == b


def result_error(got, want):
    return CompareError("got %s; want %s" % (dumps(got), dumps(want)))


def check_palindrome(args, got, want):
    text = args[0]
    if not isinstance(got, str) or not isinstance(want, str) or len(got) != len(want) or got not in text:
        raise result_error(got, want)
    if got != got[::-1]:
        raise CompareError('"%s" is not a palindrome' % got)


def check_topological(args, got, want):
    if not isinstance(got, list):
        raise CompareError("order must be an array")
    if not want:
        if not got:
            return
        raise CompareError("cyclic prerequisites require an empty order")
    n = args[0]
    if len(got) != n:
        raise CompareError("order has %d courses; want %d" % (len(got), n))
    pos = {}
    for i, x in enumerate(got):
        if isinstance(x, bool) or not isinstance(x, (int, float)) or int(x) != x:
            raise CompareError("unknown course %s" % dumps(x))
        v = int(x)
        if v < 0 or v >= n:
            raise CompareError("unknown course %d" % v)
        if v in pos:
            raise CompareError("course %d appears twice" % v)
        pos[v] = i
    for after, before in args[1]:
        if pos[before] >= pos[after]:
            raise CompareError("course %d must precede %d" % (before, after))


def check_alphabet(args, got, want):
    if not isinstance(got, str):
        raise CompareError("alphabet must be a string")
    if want == "":
        if got == "":
            return
        raise CompareError("invalid dictionary requires empty alphabet")
    words = args[0]
    chars = set()
    for w in words:
        chars.update(w)
    if len(got) != len(chars):
        raise CompareError("alphabet must include each of the %d letters once" % len(chars))
    rank = {}
    for i, ch in enumerate(got):
        if ch not in chars:
            raise CompareError("alphabet includes unknown letter %r" % ch)
        if ch in rank:
            raise CompareError("duplicate letter %r" % ch)
        rank[ch] = i
    for a, b in zip(words, words[1:]):
        j = 0
        while j < len(a) and j < len(b) and a[j] == b[j]:
            j += 1
        if j == len(b) and j < len(a):
            raise CompareError("invalid prefix ordering")
        if j < len(a) and j < len(b) and rank[a[j]] >= rank[b[j]]:
            raise CompareError("alphabet does not order %r before %r" % (a, b))


def deep(v):
    # Policies that sort want in place must not reorder the suite's own copy.
    return copy.deepcopy(v)


__all__ = [
    "CompareError", "FixtureError", "dumps", "json_value", "equivalent", "sort_array",
    "sort_neighbors", "equal_value", "result_error", "check_palindrome",
    "check_topological", "check_alphabet", "deep",
]
