"""Places where Python's own rules would let a wrong answer through."""

import unittest

from nc.compare import equivalent
from tests.helpers import check, constant, function_entry, module_with


class Traps(unittest.TestCase):
    def test_bool_is_not_a_number(self):
        self.assertFalse(equivalent(True, 1, ""))
        self.assertFalse(equivalent([1], [True], ""))
        self.assertTrue(equivalent(True, True, ""))

    def test_int_and_float_compare_numerically(self):
        self.assertTrue(equivalent(3, 3.0, ""))
        self.assertTrue(equivalent([[1, 2.0]], [[1.0, 2]], ""))
        self.assertFalse(equivalent(3, 3.5, ""))

    def test_tuples_are_lists(self):
        self.assertTrue(equivalent((1, (2, 3)), [1, [2, 3]], ""))

    def test_none_result_is_an_empty_structure(self):
        suite = {"mode": "function"}
        case = {"name": "t", "kind": "edge", "args": [], "want": []}
        self.assertIsNone(check(suite, case, function_entry(0), module_with(f=constant(None))))
        case["want"] = [1]
        self.assertIsNotNone(check(suite, case, function_entry(0), module_with(f=constant(None))))

    def test_uncomparable_values_are_reported(self):
        suite = {"mode": "function"}
        case = {"name": "t", "kind": "edge", "args": [], "want": [1]}
        self.assertIn("cannot compare", check(suite, case, function_entry(0), module_with(f=constant({1, 2}))))
