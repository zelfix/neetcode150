import unittest

from nc import nodes
from tests.helpers import check, function_entry, module_with


def fixture(args, want, **extra):
    c = {"name": "t", "kind": "edge", "args": args, "want": want}
    c.update(extra)
    return c


class Clones(unittest.TestCase):
    def test_clone_must_be_independent_and_preserve_original(self):
        suite = {"mode": "function", "policy": "graph-clone"}
        case = fixture([[[2], [1]]], [[2], [1]])
        entry = function_entry(1, ["graph"])

        def alias(self, n):
            return n

        self.assertIsNotNone(check(suite, case, entry, module_with(f=alias)))

        def make():
            a, b = nodes.GraphNode(1), nodes.GraphNode(2)
            a.neighbors, b.neighbors = [b], [a]
            return a

        self.assertIsNone(check(suite, case, entry, module_with(f=lambda self, n: make())))
        self.assertIsNotNone(check(suite, case, entry, module_with(f=lambda self, n: make().neighbors[0])))

        def mutate(self, n):
            n.neighbors = []
            return make()

        self.assertIsNotNone(check(suite, case, entry, module_with(f=mutate)))

        suite = {"mode": "function", "policy": "random-clone"}
        case = fixture([[[3, 1], [4, 0]]], [[3, 1], [4, 0]])

        def shares(self, n):
            head = nodes.RandomNode(3, nodes.RandomNode(4))
            head.random = n.next
            return head

        self.assertIn("reuses an original node", check(suite, case, function_entry(1, ["random"]), module_with(f=shares)))


class MutationCycleAncestor(unittest.TestCase):
    def test_mutates_reads_the_first_argument(self):
        suite = {"mode": "function", "mutates": True}
        case = fixture([[["O"]]], [["X"]])

        def flip(self, b):
            b[0][0] = "X"

        self.assertIsNone(check(suite, case, function_entry(1), module_with(f=flip)))
        self.assertIsNotNone(check(suite, case, function_entry(1), module_with(f=lambda self, b: None)))

        def rebind(self, b):
            b = [["X"]]  # noqa: F841 - rebinding is not mutating

        self.assertIsNotNone(check(suite, case, function_entry(1), module_with(f=rebind)))

    def test_ancestor_gets_and_wants_original_nodes(self):
        suite = {"mode": "function", "policy": "ancestor"}
        case = fixture([[2, 1, 3], 1, 3], 2)
        entry = function_entry(3, ["tree", "tree", "tree"])

        def lca(self, root, p, q):
            if p is not root.left or q is not root.right:
                raise AssertionError("arguments are not original nodes")
            return root

        self.assertIsNone(check(suite, case, entry, module_with(f=lca)))
        self.assertIn("existing ancestor", check(suite, case, entry, module_with(f=lambda self, r, p, q: nodes.TreeNode(2))))
        self.assertIn("nil ancestor", check(suite, case, entry, module_with(f=lambda self, r, p, q: None)))

    def test_cycle_is_wired_before_the_call(self):
        suite = {"mode": "function", "policy": "cycle"}
        case = fixture([[1, 2]], True, cyclePos=1)
        self.assertIsNone(check(suite, case, function_entry(1, ["list"]),
                                module_with(f=lambda self, n: n.next is n.next.next)))

    def test_reorder_checks_identity_and_values(self):
        suite = {"mode": "function", "policy": "reorder", "mutates": True}
        case = fixture([[1, 2, 3, 4]], [1, 4, 2, 3])
        entry = function_entry(1, ["list"])

        def reorder(self, head):
            a, b, c, d = head, head.next, head.next.next, head.next.next.next
            a.next, d.next, b.next, c.next = d, b, c, None

        self.assertIsNone(check(suite, case, entry, module_with(f=reorder)))

        def rebuild(self, head):
            vals = [1, 4, 2, 3]
            head.val = vals[0]
            n = head
            for v in vals[1:]:
                n.next = nodes.ListNode(v)
                n = n.next

        self.assertIn("not the original node", check(suite, case, entry, module_with(f=rebuild)))


class Operations(unittest.TestCase):
    CASE = {"name": "t", "kind": "edge", "ops": ["Counter", "add", "value", "half"],
            "calls": [[2], [3], [], []], "want": [None, None, 5, 2.5]}

    def counter(self):
        class Counter:
            def __init__(self, n):
                self.n = n

            def add(self, k):
                self.n += k

            def value(self):
                return self.n

            def half(self):
                return self.n / 2

        import types
        mod = types.ModuleType("solution")
        mod.Counter = Counter
        return mod

    def test_sequence_passes_and_arity_is_validated(self):
        suite = {"mode": "operations"}
        entry = {"cls": "Counter", "methods": [], "args": []}
        self.assertIsNone(check(suite, self.CASE, entry, self.counter()))
        broken = dict(self.CASE, calls=[[2], [], [], []])
        self.assertIsNotNone(check(suite, broken, entry, self.counter(), validate=True))
        wrong = dict(self.CASE, want=[None, None, 6, 2.5])
        self.assertIn("operation 2 value", check(suite, wrong, entry, self.counter()))

    def test_method_names_are_taken_verbatim(self):
        import types

        class T:
            def __init__(self):
                pass

            def getMin(self):
                return 1

        mod = types.ModuleType("solution")
        mod.T = mod.MinStack = T
        case = {"name": "t", "kind": "edge", "ops": ["MinStack", "getMin"], "calls": [[], []], "want": [None, 1]}
        self.assertIsNone(check({"mode": "operations"}, case, {"cls": "MinStack"}, mod))


class Exceptions(unittest.TestCase):
    def test_an_exception_is_the_cases_failure(self):
        from nc import runner

        def boom(self):
            raise NotImplementedError("TODO")

        suite = {"mode": "function"}
        case = fixture([], 0)
        with self.assertRaises(NotImplementedError):
            runner.check_case(suite, case, function_entry(0), module_with(f=boom))
        self.assertIn("TODO", runner.describe_exception(self._raise(boom), None))

    @staticmethod
    def _raise(fn):
        try:
            fn(None)
        except Exception as e:  # noqa: BLE001
            return e


class Codecs(unittest.TestCase):
    def test_round_trip_without_prescribing_a_format(self):
        suite = {"mode": "strings-codec"}
        case = fixture([["", "a#"]], ["", "a#"])
        entry = {"cls": "Solution", "methods": ["encode", "decode"], "args": [""]}

        def encode(self, strs):
            return "arbitrary format"

        def decode(self, data):
            if data != "arbitrary format":
                raise AssertionError("lost encoded output")
            return ["", "a#"]

        self.assertIsNone(check(suite, case, entry, module_with(encode=encode, decode=decode)))
        self.assertIsNotNone(check(suite, case, entry, module_with(encode=encode, decode=lambda self, d: ["a#"])))

        suite = {"mode": "tree-codec"}
        case = fixture([[0, None, -1]], [0, None, -1])
        entry = {"cls": "Codec", "methods": ["serialize", "deserialize"], "args": ["tree"]}
        self.assertIsNone(check(suite, case, entry, module_with(
            "Codec", serialize=lambda self, root: "tree",
            deserialize=lambda self, data: nodes.TreeNode(0, None, nodes.TreeNode(-1)))))

    def test_codec_state_on_self_does_not_survive(self):
        suite = {"mode": "tree-codec"}
        case = fixture([[1]], [1])
        entry = {"cls": "Codec", "methods": ["serialize", "deserialize"], "args": ["tree"]}

        def serialize(self, root):
            self.kept = root
            return ""

        def deserialize(self, data):
            return getattr(self, "kept", None)

        self.assertIsNotNone(check(suite, case, entry, module_with("Codec", serialize=serialize, deserialize=deserialize)))


class Topological(unittest.TestCase):
    def test_validation_uses_original_prerequisites(self):
        suite = {"mode": "function", "policy": "topological"}
        case = fixture([2, [[1, 0]]], [0, 1])

        def cheat(self, n, edges):
            edges[0][0], edges[0][1] = 0, 1
            return [1, 0]

        self.assertIsNotNone(check(suite, case, function_entry(2), module_with(f=cheat)))


class Validate(unittest.TestCase):
    def test_validate_checks_arity_without_calling(self):
        suite = {"mode": "function"}
        case = fixture([1, 2], 3)

        def never(self, a, b):
            raise AssertionError("called during validate")

        self.assertIsNone(check(suite, case, function_entry(2), module_with(f=never), validate=True))
        self.assertIn("takes 1", check(suite, case, function_entry(2), module_with(f=lambda self, a: 0), validate=True))
        self.assertIn("missing method", check(suite, case, function_entry(2, method="g"), module_with(f=never), validate=True))
