"""The shared policy table, run through the Python harness.

internal/testkit/testdata/policy-cases.json is also what the Go test suite
pins its own table to, so a policy that drifts on either side shows up here.
"""

import json
import os
import unittest

from tests.helpers import check, constant, function_entry, module_with

TABLE = os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "policy-cases.json")


class PolicyParity(unittest.TestCase):
    def test_every_shared_case(self):
        with open(TABLE, encoding="utf-8") as f:
            rows = json.load(f)
        self.assertGreaterEqual(len(rows), 18)
        for row in rows:
            with self.subTest(row["name"]):
                module = module_with(f=constant(row["returns"]))
                suite = {"mode": "function", "policy": row["policy"]}
                case = {"name": row["name"], "kind": "edge", "args": row["args"], "want": row["want"]}
                err = check(suite, case, function_entry(len(row["args"])), module)
                self.assertEqual(err is None, row["valid"], "error=%r" % err)
