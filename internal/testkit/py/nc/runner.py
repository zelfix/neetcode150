"""One fixture case against one solution (internal/testkit/runner.go)."""

import inspect
import os
import sys
import threading
import time
import traceback

from . import compare, fixtures
from .compare import CompareError, FixtureError
from .events import PRACTICE, case_name


class CaseError(Exception):
    """A case that did not pass, with the message the user sees."""


def entry_point(module, entry):
    cls = getattr(module, entry.get("cls") or "Solution", None)
    if cls is None:
        raise CaseError("no solution entry point: class %s is missing" % (entry.get("cls") or "Solution"))
    return cls


def bound(cls, name):
    """A method of a fresh instance. Each case starts from a new object so
    state left behind by one case cannot help or hurt the next."""
    fn = getattr(cls(), name, None)
    if fn is None or not callable(fn):
        raise CaseError("missing method %s" % name)
    return fn


def arity(fn):
    """How many positional arguments fn takes, or None when it varies."""
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return None
    params = list(sig.parameters.values())
    if params and params[0].name == "self" and not inspect.ismethod(fn):
        params = params[1:]
    n = 0
    for p in params:
        if p.kind == p.VAR_POSITIONAL:
            return None
        if p.kind in (p.POSITIONAL_ONLY, p.POSITIONAL_OR_KEYWORD):
            n += 1
    return n


def check_arity(fn, name, want):
    got = arity(fn)
    if got is not None and got != want:
        raise CaseError("fixture has %d arguments; %s takes %d" % (want, name, got))


def check_case(suite, case, entry, module, validate=False):
    """Raise CaseError if the case does not pass. Mirrors Go's checkCase,
    including the order of its checks, so the two report the same thing."""
    try:
        _check_case(suite, case, entry, module, validate)
    except (CompareError, FixtureError) as e:
        raise CaseError(str(e))


def _check_case(suite, case, entry, module, validate):
    mode = suite.get("mode", "function")
    policy = suite.get("policy", "") or ""
    cls = entry_point(module, entry)
    if mode == "operations":
        return _check_operations(case, cls, validate)

    kinds = list(entry.get("args") or [])
    methods = list(entry.get("methods") or [])
    if not methods:
        raise CaseError("no solution entry point: no method named")
    raw = list(case.get("args") or [])
    if len(raw) != len(kinds):
        raise CaseError("fixture has %d arguments; signature requires %d" % (len(raw), len(kinds)))

    if policy == "ancestor" and len(raw) == 3:
        args = [fixtures.build(raw[0], kinds[0]), None, None]
        for i in (1, 2):
            args[i] = fixtures.find_node(args[0], raw[i])
            if args[i] is None:
                raise CaseError("ancestor fixture refers to missing node %s" % raw[i])
    else:
        args = [fixtures.build(v, k) for v, k in zip(raw, kinds)]

    if policy == "cycle" and case.get("cyclePos") is not None and case["cyclePos"] >= 0:
        chain = fixtures.list_nodes(args[0])
        if case["cyclePos"] >= len(chain):
            raise CaseError("cycle index out of bounds")
        chain[-1].next = chain[case["cyclePos"]]

    want = case.get("want")

    if mode in ("strings-codec", "tree-codec"):
        if len(methods) != 2:
            raise CaseError("codec needs encoder and decoder")
        if validate:
            for name in methods:
                check_arity(bound_or_unbound(cls, name), name, 1)
            return
        # Two instances, as the Go test file calls Constructor() twice: a codec
        # that remembers the tree on self instead of in the string would pass
        # with one.
        encode, decode = bound(cls, methods[0]), bound(cls, methods[1])
        before = fixtures.normalize(args[0])
        got = fixtures.normalize(decode(encode(args[0])))
        if not compare.equivalent(got, before, ""):
            raise compare.result_error(got, before)
        return

    if validate:
        check_arity(bound_or_unbound(cls, methods[0]), methods[0], len(raw))
        return

    fn = bound(cls, methods[0])
    original = before = None
    if policy in ("random-clone", "graph-clone"):
        original = fixtures.node_set(args[0])
        before = fixtures.normalize(args[0])
    reorder = reorder_values = None
    if policy == "reorder":
        reorder = fixtures.list_nodes(args[0])
        reorder_values = [n.val for n in reorder]

    out = fn(*args)
    result = args[0] if suite.get("mutates") else out

    if original is not None:
        for ptr in fixtures.node_set(result):
            if ptr in original:
                raise CaseError("result reuses an original node; a deep copy is required")
        try:
            after = fixtures.normalize(args[0])
        except FixtureError as e:
            raise CaseError("input corrupted: %s" % e)
        if not compare.equivalent(after, before, ""):
            raise CaseError("copy changed the original structure")
    if policy == "reorder":
        after = fixtures.list_nodes(args[0])
        if len(after) != len(reorder):
            raise CaseError("reorder changed the number of nodes")
        for i, n in enumerate(after):
            idx = i // 2 if i % 2 == 0 else len(reorder) - 1 - i // 2
            if n is not reorder[idx]:
                raise CaseError("node %d is not the original node from position %d" % (i, idx))
            if n.val != reorder_values[idx]:
                raise CaseError("reorder must move nodes without changing their values")
    _compare(policy, case, kinds, args, result, want)


def bound_or_unbound(cls, name):
    fn = getattr(cls, name, None)
    if fn is None or not callable(fn):
        raise CaseError("missing method %s" % name)
    return fn


def _compare(policy, case, kinds, args, result, want):
    if policy in ("topological", "alphabet"):
        # A solution may reorder its lists; validate against the original input.
        args = [fixtures.build(v, k) for v, k in zip(case.get("args") or [], kinds)]
    if policy == "ancestor":
        if result is None:
            raise CaseError("got nil ancestor; want node %s" % compare.dumps(want))
        found = fixtures.find_node(args[0], want)
        if found is None or result is not found:
            raise CaseError("want the existing ancestor node %s" % compare.dumps(want))
        return
    got = fixtures.normalize(result)
    if policy == "topological":
        return compare.check_topological(args, got, want)
    if policy == "alphabet":
        return compare.check_alphabet(args, got, want)
    if policy == "palindrome":
        return compare.check_palindrome(args, got, want)
    if policy == "graph-clone":
        if result is not None and args[0] is not None and result.val != args[0].val:
            raise CaseError("cloned graph must return the copy of the original starting node")
        # Adjacency order is not part of the graph, but every edge is.
        got = compare.sort_neighbors(compare.json_value(got))
        want = compare.sort_neighbors(compare.deep(want))
    if not compare.equivalent(got, want, policy):
        raise compare.result_error(got, want)


def _check_operations(case, cls, validate):
    ops, calls = case.get("ops") or [], case.get("calls") or []
    if not ops or len(ops) != len(calls):
        raise CaseError("operations and argument lists differ")
    wants = case.get("want")
    if not isinstance(wants, list) or len(wants) != len(ops):
        raise CaseError("operation outputs differ in length")
    if wants[0] is not None:
        raise CaseError("constructor output must be null")
    if validate:
        check_arity(cls.__init__, ops[0], len(calls[0]))
        for i in range(1, len(ops)):
            check_arity(bound_or_unbound(cls, ops[i]), ops[i], len(calls[i]))
        return
    obj = cls(*calls[0])
    for i in range(1, len(ops)):
        # LeetCode's Python methods are already spelled as the fixtures spell
        # the operations, so there is no Go-style capitalisation step.
        name = ops[i]
        method = getattr(obj, name, None)
        if method is None or not callable(method):
            raise CaseError("missing method %s" % name)
        out = method(*calls[i])
        if out is None:
            if wants[i] is not None:
                raise CaseError("operation %d %s: got null; want %s" % (i, name, compare.dumps(wants[i])))
            continue
        got = fixtures.normalize(out)
        policy = "float" if isinstance(out, float) else ""
        if not compare.equivalent(got, wants[i], policy):
            raise CaseError("operation %d %s: %s" % (i, name, compare.result_error(got, wants[i])))


# ---------- running a whole suite ----------

RECURSION_LIMIT = 20_000
STACK_BYTES = 64 << 20


def describe_exception(e, solution_path):
    """type: message, plus the frames that are the user's own."""
    lines = ["%s: %s" % (type(e).__name__, e)]
    if isinstance(e, RecursionError):
        lines.append("(the recursion limit here is %d frames; is the base case reached?)" % RECURSION_LIMIT)
    base = os.path.basename(solution_path) if solution_path else None
    for frame in traceback.extract_tb(e.__traceback__):
        if base and os.path.basename(frame.filename) == base:
            lines.append("  %s:%d in %s" % (base, frame.lineno, frame.name))
    return "\n".join(lines)


def _in_thread(fn, timeout):
    """Run fn on its own thread and return (finished, error_or_None)."""
    box = {}

    def target():
        try:
            fn()
        except BaseException as e:  # a case failure is data, not a crash
            box["error"] = e

    t = threading.Thread(target=target, daemon=True)
    t.start()
    t.join(timeout)
    if t.is_alive():
        return False, None
    return True, box.get("error")


def prepare_threads():
    sys.setrecursionlimit(RECURSION_LIMIT)
    try:
        threading.stack_size(STACK_BYTES)
    except (ValueError, RuntimeError):
        threading.stack_size(16 << 20)


def run_suite(suite, entry, module, emitter, case_timeout, deadline_at, solution_path=None):
    """Run every case as its own test, reporting through emitter."""
    cases = suite.get("cases") or []
    if not cases:
        emitter.failed_(PRACTICE, "practice suite has no cases")
        return
    for case in cases:
        test = case_name(case.get("kind", "other"), case.get("name", ""))
        remaining = deadline_at - time.monotonic()
        if remaining <= 0:
            emitter.failed_(test, "not run: the suite's deadline passed")
            continue
        started = time.monotonic()
        finished, err = _in_thread(lambda: check_case(suite, case, entry, module), min(case_timeout, remaining))
        elapsed = time.monotonic() - started
        if not finished:
            emitter.failed_(test, "timed out after %.0fs; an endless loop would do that" % min(case_timeout, remaining), elapsed)
        elif err is None:
            emitter.passed_(test, elapsed)
        elif isinstance(err, CaseError):
            emitter.failed_(test, "%s\n%s" % (err, describe_input(suite, case)), elapsed)
        else:
            emitter.failed_(test, describe_exception(err, solution_path), elapsed)


# Bounds the input shown under a failure, as inputLimit does in Go.
INPUT_LIMIT = 320


def describe_input(suite, case):
    """The line under a failed case that shows what it was given, mirroring
    Go's describeInput: arguments for a function case, calls for operations."""
    if suite.get("mode") == "operations":
        ops = case.get("ops") or []
        calls = case.get("calls") or []
        parts = []
        for i, op in enumerate(ops):
            args = calls[i] if i < len(calls) else []
            parts.append("%s(%s)" % (op, ", ".join(compare.dumps(a) for a in args)))
        text = "ops: " + ", ".join(parts)
    else:
        text = "input: " + ", ".join(compare.dumps(a) for a in (case.get("args") or []))
        pos = case.get("cyclePos")
        if pos is not None and pos >= 0:
            text += "; tail links back to index %d" % pos
    if len(text) > INPUT_LIMIT:
        text = text[:INPUT_LIMIT] + "…"
    return text


def validate_suite(suite, entry, module):
    """Every case against the signature only, without calling the solution."""
    errors = []
    for case in suite.get("cases") or []:
        try:
            check_case(suite, case, entry, module, validate=True)
        except CaseError as e:
            errors.append("%s/%s: %s" % (suite.get("slug", "?"), case.get("name", "?"), e))
    return errors
