"""The Python practice harness.

A port of internal/testkit (Go) that reads the same fixture JSON and reports
in the same `go test -json` line format, so the server, the Coach page and the
fixture-failure records need no second code path. Standard library only: the
sandbox image has nothing else, on purpose.
"""
