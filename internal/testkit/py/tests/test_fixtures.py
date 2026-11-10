import unittest

from nc import fixtures, nodes
from nc.compare import equivalent


class NodeRoundTrips(unittest.TestCase):
    def test_round_trip_and_reject_cycles(self):
        for raw, kind in [
            ([1, 2, 3], "list"),
            ([1, None, 2, 3, None, None, 4], "tree"),
            ([[2, 3], [1, 3], [1, 2]], "graph"),
            ([[7, None], [7, 0], [8, 2]], "random"),
            ([[1, 2], [], [3]], "list[]"),
        ]:
            with self.subTest(kind=kind):
                built = fixtures.build(raw, kind)
                self.assertTrue(equivalent(fixtures.normalize(built), raw, ""), fixtures.normalize(built))

        cyclic = nodes.ListNode(1)
        cyclic.next = cyclic
        with self.assertRaises(fixtures.FixtureError):
            fixtures.normalize(cyclic)
        shared = nodes.TreeNode(2)
        root = nodes.TreeNode(1, shared, shared)
        with self.assertRaises(fixtures.FixtureError):
            fixtures.normalize(root)

    def test_empty_and_null_roots(self):
        self.assertIsNone(fixtures.build([], "tree"))
        self.assertIsNone(fixtures.build([None], "tree"))
        self.assertIsNone(fixtures.build([], "list"))
        self.assertEqual(fixtures.normalize(None), [])

    def test_fixture_errors_name_the_problem(self):
        with self.assertRaisesRegex(fixtures.FixtureError, "unreachable tree"):
            fixtures.build([1, None, None, 2], "tree")
        with self.assertRaisesRegex(fixtures.FixtureError, "invalid graph neighbor"):
            fixtures.build([[2], [3]], "graph")
        with self.assertRaisesRegex(fixtures.FixtureError, "invalid random pointer"):
            fixtures.build([[1, 5]], "random")
        with self.assertRaisesRegex(fixtures.FixtureError, "value and pointer"):
            fixtures.build([[1]], "random")

    def test_solution_classes_interoperate_by_attributes(self):
        # A solution's own TreeNode, not ours, is what comes back.
        class TreeNode:
            def __init__(self, val, left=None, right=None):
                self.val, self.left, self.right = val, left, right

        self.assertEqual(fixtures.normalize(TreeNode(1, TreeNode(2))), [1, 2])

    def test_node_set_keeps_nodes_alive(self):
        root = fixtures.build([[2], [1]], "graph")
        found = fixtures.node_set(root)
        self.assertEqual(len(found), 2)
        for n in found.values():
            self.assertIsNotNone(n)
