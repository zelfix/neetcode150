"""run.py end to end, the way the server invokes it."""

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(HERE, "..", "run.py")

SPEC = json.dumps({
    "slug": "two-sum", "entry": "twoSum", "mode": "function", "policy": "unordered",
    "cases": [
        {"name": "example 1", "kind": "example", "args": [[2, 7, 11, 15], 9], "want": [0, 1]},
        {"name": "no answer", "kind": "edge", "args": [[1], 5], "want": []},
    ],
})


def package(solution, spec=SPEC, entry=None, tests=None):
    d = tempfile.mkdtemp()
    with open(os.path.join(d, "solution.py"), "w") as f:
        f.write(textwrap.dedent(solution))
    with open(os.path.join(d, "practice_cases.py"), "w") as f:
        f.write("SPEC = r'''%s'''\nENTRY = %r\n" % (spec, entry or {"cls": "Solution", "methods": ["twoSum"], "args": ["", ""]}))
    if tests:
        with open(os.path.join(d, "test_solution.py"), "w") as f:
            f.write(textwrap.dedent(tests))
    return d


def run(*args):
    p = subprocess.run([sys.executable, "-I", "-B", RUN, *args], capture_output=True, text=True, timeout=60)
    results = {}
    plain = []
    for line in p.stdout.splitlines():
        try:
            ev = json.loads(line)
        except ValueError:
            plain.append(line)
            continue
        if ev["Action"] in ("pass", "fail", "skip"):
            results[ev["Test"]] = ev["Action"]
    return p.returncode, results, plain, p.stdout + p.stderr


class RunPy(unittest.TestCase):
    def test_correct_solution_passes_and_exit_is_zero(self):
        d = package("""
            class Solution:
                def twoSum(self, nums, target):
                    seen = {}
                    for i, n in enumerate(nums):
                        if target - n in seen:
                            return [seen[target - n], i]
                        seen[n] = i
                    return []
        """)
        code, results, _, out = run("--package", d)
        self.assertEqual(code, 0, out)
        self.assertEqual(results, {"TestPracticeCases/example/example_1": "pass",
                                   "TestPracticeCases/edge/no_answer": "pass"})

    def test_wrong_solution_fails_the_case_and_exit_is_one(self):
        d = package("""
            class Solution:
                def twoSum(self, nums, target):
                    return [0, 0]
        """)
        code, results, _, out = run("--package", d)
        self.assertEqual(code, 1)
        self.assertEqual(results["TestPracticeCases/example/example_1"], "fail")
        self.assertIn("got [0,0]; want [0,1]", out)
        # The failing data goes with the verdict, so nobody opens the fixture
        # file to learn what broke.
        self.assertIn("input: [2,7,11,15], 9", out)

    def test_exception_names_the_users_line(self):
        d = package("""
            class Solution:
                def twoSum(self, nums, target):
                    raise NotImplementedError("TODO: implement")
        """)
        code, results, _, out = run("--package", d)
        self.assertEqual(code, 1)
        self.assertIn("NotImplementedError: TODO: implement", out)
        self.assertIn("solution.py:4 in twoSum", out)

    def test_syntax_error_is_plain_output_with_nothing_run(self):
        d = package("class Solution:\n    def twoSum(self, nums, target)\n        return []\n")
        code, results, plain, out = run("--package", d)
        self.assertEqual(code, 1)
        self.assertEqual(results, {})
        self.assertTrue(any("SyntaxError" in line for line in plain), out)

    def test_endless_loop_times_out_and_the_run_continues(self):
        d = package("""
            class Solution:
                def twoSum(self, nums, target):
                    if target == 9:
                        while True:
                            pass
                    return []
        """)
        code, results, _, out = run("--package", d, "--case-timeout", "300ms", "--deadline", "5s")
        self.assertEqual(code, 1)
        self.assertEqual(results["TestPracticeCases/example/example_1"], "fail")
        self.assertEqual(results["TestPracticeCases/edge/no_answer"], "pass")
        self.assertIn("timed out", out)

    def test_user_tests_are_reported_as_other(self):
        d = package("""
            class Solution:
                def twoSum(self, nums, target):
                    return [0, 1] if target == 9 else []
        """, tests="""
            import unittest
            from solution import Solution

            class MyTests(unittest.TestCase):
                def test_mine(self):
                    self.assertEqual(Solution().twoSum([2, 7], 9), [0, 1])

                def test_broken(self):
                    self.assertEqual(Solution().twoSum([2, 7], 9), [1, 0])
        """)
        code, results, _, out = run("--package", d)
        self.assertEqual(code, 1)
        self.assertEqual(results["MyTests/test_mine"], "pass")
        self.assertEqual(results["MyTests/test_broken"], "fail")

    def test_validate_accepts_a_stub_and_rejects_wrong_arity(self):
        good = package("""
            class Solution:
                def twoSum(self, nums, target):
                    raise NotImplementedError
        """)
        bad = package("""
            class Solution:
                def twoSum(self, nums):
                    raise NotImplementedError
        """)
        code, _, plain, out = run("--mode", "validate", "--package", good)
        self.assertEqual(code, 0, out)
        self.assertIn("validated 1 packages, 0 problems", out)
        code, _, _, out = run("--mode", "validate", "--package", good, "--package", bad)
        self.assertEqual(code, 1)
        self.assertIn("two-sum/example 1: fixture has 2 arguments; twoSum takes 1", out)

    def test_nothing_from_the_package_shadows_the_stdlib(self):
        d = package("""
            import json
            class Solution:
                def twoSum(self, nums, target):
                    return [0, 1] if hasattr(json, "dumps") else []
        """)
        with open(os.path.join(d, "json.py"), "w") as f:
            f.write("raise SystemExit('planted json.py was imported')\n")
        code, results, _, out = run("--package", d)
        self.assertEqual(results.get("TestPracticeCases/example/example_1"), "pass", out)
