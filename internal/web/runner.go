package web

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"os/exec"
	"path/filepath"
	"time"
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

// runGoTest runs the tests for one solution package.
//
// The three return values separate the two failures that look alike from a
// browser: ok=false with err=nil means the tests ran and something failed,
// which is the normal case worth showing in full. A non-nil err means the run
// never happened, and the user needs to know that rather than read an empty
// pane as a pass.
func runGoTest(ctx context.Context, root, pkgDir string) (output string, ok bool, err error) {
	// -count=1 defeats the test cache. The cache keys on file content so it
	// would be correct, but it prints "(cached)" and reads as "nothing ran".
	cmd := exec.CommandContext(ctx, "go", "test",
		"-count=1", "-timeout", goTestTimeout.String(),
		"./"+filepath.ToSlash(pkgDir)+"/")
	cmd.Dir = root

	out, runErr := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return string(out), false, fmt.Errorf("the run was cut off after %s — an endless loop in the solution would do that: %w",
			runTimeout, ctxErr)
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return string(out), false, nil // compiled or not, `go test` had its say
	}
	if runErr != nil {
		return string(out), false, fmt.Errorf("could not run go test: %w", runErr)
	}
	return string(out), true, nil
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
