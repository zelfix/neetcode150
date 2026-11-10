import io
import json
import unittest

from nc.events import Emitter, case_name


class Events(unittest.TestCase):
    def test_names_match_what_the_server_parses_back(self):
        self.assertEqual(case_name("example", "example 1"), "TestPracticeCases/example/example_1")

    def test_lines_are_go_test_json(self):
        out = io.StringIO()
        e = Emitter(out)
        e.passed_("TestPracticeCases/example/a")
        e.failed_("TestPracticeCases/edge/b", "got 1; want 2\nsecond line")
        e.skipped_("T/skip", "why")
        e.raw("plain text")
        lines = out.getvalue().splitlines()
        events = []
        for line in lines:
            try:
                events.append(json.loads(line))
            except ValueError:
                self.assertEqual(line, "plain text")
        actions = [(ev["Action"], ev["Test"]) for ev in events if ev["Action"] != "output"]
        self.assertEqual(actions, [("pass", "TestPracticeCases/example/a"),
                                   ("fail", "TestPracticeCases/edge/b"), ("skip", "T/skip")])
        outputs = "".join(ev["Output"] for ev in events if ev["Action"] == "output")
        self.assertIn("--- FAIL: TestPracticeCases/edge/b", outputs)
        self.assertIn("    second line\n", outputs)
        self.assertEqual((e.passed, e.failed, e.skipped), (1, 1, 1))
