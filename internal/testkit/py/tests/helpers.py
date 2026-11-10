"""Shared pieces for the harness's own tests."""

import types

from nc import runner


def module_with(cls_name="Solution", **methods):
    """A stand-in for a loaded solution.py: one class with the given methods."""
    cls = type(cls_name, (), {name: fn for name, fn in methods.items()})
    mod = types.ModuleType("solution")
    setattr(mod, cls_name, cls)
    return mod


def constant(value):
    """A method that ignores its arguments and returns a fresh copy of value."""
    import copy

    def fn(self, *args):
        return copy.deepcopy(value)

    return fn


def check(suite, case, entry, module, validate=False):
    """Return the CaseError message, or None when the case passes."""
    try:
        runner.check_case(suite, case, entry, module, validate)
    except runner.CaseError as e:
        return str(e)
    return None


def function_entry(nargs, kinds=None, method="f", cls="Solution"):
    return {"cls": cls, "methods": [method], "args": kinds or [""] * nargs}
