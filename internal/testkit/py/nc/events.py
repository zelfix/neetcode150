"""Emits the `go test -json` line format.

That is a Go tool's schema, used on purpose: the server already parses it in
internal/web/runner.go (readTestEvents, namedCase) and turns failed cases into
the fixture-failure records the Coach page reads. A second format would mean a
second parser and two ways for that page to be wrong. Only three actions are
needed: output, pass, fail (and skip for a user's own skipped test).
"""

import json
import sys
import time

PRACTICE = "TestPracticeCases"


def case_name(kind, name):
    # Go substitutes underscores for spaces in subtest names, and namedCase
    # puts them back. The fixtures use spaces and nothing else.
    return "%s/%s/%s" % (PRACTICE, kind, name.replace(" ", "_"))


class Emitter:
    def __init__(self, out=None):
        self.out = out or sys.stdout
        self.failed = 0
        self.passed = 0
        self.skipped = 0

    def _line(self, **fields):
        fields.setdefault("Time", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        self.out.write(json.dumps(fields, separators=(",", ":")) + "\n")
        self.out.flush()

    def output(self, test, text):
        if not text.endswith("\n"):
            text += "\n"
        self._line(Action="output", Test=test, Output=text)

    def passed_(self, test, elapsed=0.0):
        self.passed += 1
        self.output(test, "--- PASS: %s (%.2fs)" % (test, elapsed))
        self._line(Action="pass", Test=test, Elapsed=round(elapsed, 3))

    def failed_(self, test, message, elapsed=0.0):
        self.failed += 1
        self.output(test, "--- FAIL: %s (%.2fs)" % (test, elapsed))
        for line in message.rstrip("\n").split("\n"):
            self.output(test, "    " + line)
        self._line(Action="fail", Test=test, Elapsed=round(elapsed, 3))

    def skipped_(self, test, reason=""):
        self.skipped += 1
        self.output(test, "--- SKIP: %s" % test)
        if reason:
            self.output(test, "    " + reason)
        self._line(Action="skip", Test=test)

    def raw(self, text):
        # Not an event: readTestEvents keeps a line it cannot parse as plain
        # output, which is how a Go build failure reaches the user too.
        self.out.write(text.rstrip("\n") + "\n")
        self.out.flush()
