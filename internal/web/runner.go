package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"neetcode150/internal/studydb"
)

// goTestTimeout is passed to `go test` itself, and runTimeout bounds the whole
// subprocess from the outside.
//
// Both are needed. The inner one makes the test binary report which test hung,
// which is the useful diagnostic; the outer one is the backstop for a run that
// never gets as far as a test binary — a compile that spins, or a `go` that
// blocks fetching the toolchain.
const (
	goTestTimeout = 30 * time.Second
	runTimeout    = 60 * time.Second
)

// testRun is the outcome of one `go test`.
//
// OK and the error separate the two failures that look alike from a browser:
// OK=false with a nil error means the tests ran and something failed, which is
// the normal case worth showing in full. A non-nil error means the run never
// happened, and the user needs to know that rather than read an empty pane as
// a pass.
type testRun struct {
	Output  string
	OK      bool
	Passed  int
	Failed  int
	Skipped int
	// Ran reports whether any non-skipped test completed. Go calls an empty or
	// wholly skipped package a success, which is not evidence of anything.
	Ran bool
	// Cases are the practice fixtures this run broke.
	Cases []studydb.FailedCase
}

// runGoTest runs the tests for one solution package.
func runGoTest(ctx context.Context, root, pkgDir string) (testRun, error) {
	cacheRoot := filepath.Join(root, ".nc-cache")
	goCache := filepath.Join(cacheRoot, "go-build")
	goTmp := filepath.Join(cacheRoot, "tmp")
	for _, dir := range []string{goCache, goTmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return testRun{}, fmt.Errorf("could not prepare Go's workspace-local cache: %w", err)
		}
	}

	// -count=1 defeats the test cache. The cache keys on file content so it
	// would be correct, but it prints "(cached)" and reads as "nothing ran".
	cmd := exec.CommandContext(ctx, "go", "test",
		"-json", "-count=1", "-run=.", "-skip=^$", "-timeout", goTestTimeout.String(),
		"./"+filepath.ToSlash(pkgDir)+"/")
	cmd.Dir = root
	cmd.Env = os.Environ()
	for name, value := range map[string]string{
		"GOCACHE":  goCache,
		"GOTMPDIR": goTmp,
		"TMPDIR":   goTmp,
		"TMP":      goTmp,
		"TEMP":     goTmp,
	} {
		cmd.Env = setEnv(cmd.Env, name, value)
	}

	raw, runErr := cmd.CombinedOutput()
	run := readTestEvents(raw)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return run, fmt.Errorf("the run was cut off after %s — an endless loop in the solution would do that: %w",
			runTimeout, ctxErr)
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return run, nil // compiled or not, `go test` had its say
	}
	if runErr != nil {
		return run, fmt.Errorf("could not run go test: %w", runErr)
	}
	if !run.Ran {
		return run, fmt.Errorf("no tests ran: tests are missing or all were skipped; add test cases before submitting")
	}
	run.OK = run.Failed == 0
	return run, nil
}

// Go's package-level success includes empty and entirely skipped test suites.
// Only a completed, non-skipped test is evidence that a solution was exercised.
func readTestEvents(raw []byte) testRun {
	var output strings.Builder
	results := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		var e struct{ Action, Test, Package, Output string }
		if json.Unmarshal([]byte(line), &e) != nil {
			output.WriteString(line + "\n")
			continue
		}
		output.WriteString(e.Output)
		if e.Test != "" && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip") {
			results[e.Test] = e.Action
		}
	}
	var run testRun
	for name, result := range results {
		parent := false
		failedChild := false
		for other := range results {
			if strings.HasPrefix(other, name+"/") {
				parent = true
				failedChild = failedChild || results[other] == "fail"
			}
		}
		if parent {
			// A parent that failed only because a child did is not a case of
			// its own; one that failed on its own account is.
			if result == "fail" && !failedChild {
				run.Failed++
				run.Cases = append(run.Cases, namedCase(name))
			}
			continue
		}
		switch result {
		case "pass":
			run.Passed++
		case "fail":
			run.Failed++
			run.Cases = append(run.Cases, namedCase(name))
		case "skip":
			run.Skipped++
		}
	}
	// Map iteration is unordered, and these are stored and asserted on.
	sort.Slice(run.Cases, func(i, j int) bool {
		if run.Cases[i].Kind != run.Cases[j].Kind {
			return run.Cases[i].Kind < run.Cases[j].Kind
		}
		return run.Cases[i].Name < run.Cases[j].Name
	})
	run.Ran = run.Passed+run.Failed > 0
	run.Output = fmt.Sprintf("Tests: %d passed, %d failed, %d skipped.\n\n",
		run.Passed, run.Failed, run.Skipped) + output.String()
	return run
}

// namedCase reads a Go test name back into the fixture it came from.
//
// testkit runs each case as t.Run(kind + "/" + name), and Go substitutes
// underscores for the spaces in a subtest name. The fixtures use spaces and
// nothing else, so putting them back recovers the name as written.
func namedCase(test string) studydb.FailedCase {
	spaces := func(s string) string { return strings.ReplaceAll(s, "_", " ") }
	parts := strings.Split(test, "/")
	switch {
	case len(parts) >= 3 && (parts[1] == "example" || parts[1] == "edge"):
		return studydb.FailedCase{Kind: parts[1], Name: spaces(strings.Join(parts[2:], "/"))}
	case len(parts) >= 2:
		return studydb.FailedCase{Kind: "other", Name: spaces(strings.Join(parts[1:], "/"))}
	default:
		return studydb.FailedCase{Kind: "other", Name: spaces(test)}
	}
}

func setEnv(env []string, name, value string) []string {
	prefix := name + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// formatGo runs source through gofmt.
//
// Source that does not parse is returned unchanged alongside the error, never
// discarded: a save in the middle of typing is the normal case, and the file is
// the user's work in progress. Refusing it would lose keystrokes to protect a
// file that is allowed to be broken, and `go test` reports the compile error
// anyway.
func formatGo(src string) (string, error) {
	out, err := format.Source([]byte(src))
	if err != nil {
		return src, err
	}
	return string(out), nil
}
