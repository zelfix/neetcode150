"""The user's own test_*.py files, reported in the same event stream."""

import os
import sys
import unittest


class _Reporter(unittest.TestResult):
    """Names come out as Class/method, which the server files as kind "other",
    exactly where a personal Go test lands."""

    def __init__(self, emitter):
        super().__init__()
        self.emitter = emitter

    @staticmethod
    def _name(test):
        return "%s/%s" % (type(test).__name__, getattr(test, "_testMethodName", str(test)))

    def addSuccess(self, test):
        super().addSuccess(test)
        self.emitter.passed_(self._name(test))

    def addFailure(self, test, err):
        super().addFailure(test, err)
        self.emitter.failed_(self._name(test), self._format_err(err))

    def addError(self, test, err):
        super().addError(test, err)
        self.emitter.failed_(self._name(test), self._format_err(err))

    def addSkip(self, test, reason):
        super().addSkip(test, reason)
        self.emitter.skipped_(self._name(test), reason)

    def addExpectedFailure(self, test, err):
        super().addExpectedFailure(test, err)
        self.emitter.passed_(self._name(test))

    def addUnexpectedSuccess(self, test):
        super().addUnexpectedSuccess(test)
        self.emitter.failed_(self._name(test), "unexpected success")

    @staticmethod
    def _format_err(err):
        exc_type, value, _ = err
        return "%s: %s" % (exc_type.__name__, value)


def run_user_tests(pkg_dir, module, emitter):
    """Discover test_*.py beside the solution and run them.

    The solution module is pre-registered so `from solution import Solution`
    resolves to the file already loaded, and the package directory goes at
    the end of sys.path, so a file there cannot shadow the standard library.
    """
    if not any(n.startswith("test_") and n.endswith(".py") for n in os.listdir(pkg_dir)):
        return
    sys.modules.setdefault("solution", module)
    sys.path.append(pkg_dir)
    try:
        suite = unittest.defaultTestLoader.discover(pkg_dir, pattern="test_*.py", top_level_dir=pkg_dir)
        suite.run(_Reporter(emitter))
    finally:
        try:
            sys.path.remove(pkg_dir)
        except ValueError:
            pass
