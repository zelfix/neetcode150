"""Run or validate the practice fixtures for one or more solution packages.

    python3 -I -B run.py --package DIR [--package DIR ...] [--mode run|validate]
                         [--case-timeout 5s] [--deadline 25s]

A package directory holds the user's solution.py and the generated
practice_cases.py, which declares SPEC (the fixture JSON) and ENTRY (class,
method names and argument kinds). Output is the `go test -json` line format,
see nc/events.py. The exit status is 1 when any test failed.

Run under -I: the script directory is then not on sys.path, so the harness
puts itself there explicitly and loads the user's file by path, never by
import. Nothing in the package directory can shadow a standard module.
"""

import argparse
import importlib.util
import json
import os
import sys
import time
import traceback

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from nc import runner, usertests  # noqa: E402
from nc.events import Emitter  # noqa: E402


def duration(text):
    text = text.strip()
    for suffix, scale in (("ms", 0.001), ("s", 1.0), ("m", 60.0)):
        if text.endswith(suffix):
            return float(text[: -len(suffix)]) * scale
    return float(text)


def load_module(path, name="solution"):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_spec(path):
    """practice_cases.py is declarative: SPEC and ENTRY, nothing executed."""
    namespace = {}
    with open(path, encoding="utf-8") as f:
        code = compile(f.read(), path, "exec")
    exec(code, namespace)  # noqa: S102 - the user's own file, in the user's own run
    suite = json.loads(namespace["SPEC"])
    entry = namespace.get("ENTRY") or {}
    return suite, entry


def main(argv=None):
    ap = argparse.ArgumentParser(prog="run.py", add_help=True)
    ap.add_argument("--package", action="append", required=True, help="solution package directory")
    ap.add_argument("--mode", choices=("run", "validate"), default="run")
    ap.add_argument("--case-timeout", default="5s")
    ap.add_argument("--deadline", default="25s")
    ap.add_argument("--solution", default="solution.py")
    ap.add_argument("--spec", default="practice_cases.py")
    args = ap.parse_args(argv)

    case_timeout = duration(args.case_timeout)
    deadline_at = time.monotonic() + duration(args.deadline)
    runner.prepare_threads()

    if args.mode == "validate":
        return validate(args)

    emitter = Emitter()
    for pkg in args.package:
        solution_path = os.path.join(pkg, args.solution)
        try:
            module = load_module(solution_path)
        except BaseException:  # noqa: BLE001 - a broken file is the user's news
            # Plain lines, not events: the same shape as a Go build failure,
            # which the server shows as output with nothing having run.
            emitter.raw("%s did not load:" % args.solution)
            emitter.raw(traceback.format_exc())
            emitter.failed += 1
            continue
        try:
            suite, entry = load_spec(os.path.join(pkg, args.spec))
        except BaseException:  # noqa: BLE001
            emitter.raw("%s did not load:" % args.spec)
            emitter.raw(traceback.format_exc())
            emitter.failed += 1
            continue
        runner.run_suite(suite, entry, module, emitter, case_timeout, deadline_at, solution_path)
        usertests.run_user_tests(pkg, module, emitter)
    # Daemon threads from timed-out cases must not hold the interpreter open.
    sys.stdout.flush()
    sys.stderr.flush()
    os._exit(1 if emitter.failed else 0)


def validate(args):
    problems = 0
    for pkg in args.package:
        try:
            module = load_module(os.path.join(pkg, args.solution))
            suite, entry = load_spec(os.path.join(pkg, args.spec))
        except BaseException as e:  # noqa: BLE001
            print("%s: %s: %s" % (pkg, type(e).__name__, e), file=sys.stderr)
            problems += 1
            continue
        for line in runner.validate_suite(suite, entry, module):
            print(line, file=sys.stderr)
            problems += 1
    print("validated %d packages, %d problems" % (len(args.package), problems))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
