package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Where the module is assembled inside the container. The image warms its build
// cache at this same path, because Go's cache keys are path-sensitive and a
// different working directory would throw the warm entries away.
const containerSrc = "/src"

// sandboxImage is the default image tag. Built from sandbox/Dockerfile.
const sandboxImage = "nc-sandbox:go1.26"

// dockerRunner runs one person's tests in a one-shot container.
//
// The mount set is the whole security model, and what it leaves out matters
// more than what it includes. The repository root is never mounted: a guest
// writes their own Go, so without this they could import
// neetcode150/internal/studydb and read every other person's notes, progress
// and archived code. Only go.mod, go.sum, the two packages a generated practice
// test needs, and their own package directory are visible, so that import
// simply fails to resolve — which also enforces the repository's own rule that
// solutions depend on nothing, and says so in a comprehensible error.
type dockerRunner struct {
	cli   string // docker binary
	image string
}

func newDockerRunner(image string) dockerRunner {
	if image == "" {
		image = sandboxImage
	}
	return dockerRunner{cli: "docker", image: image}
}

// containerPkg is where the package under test appears inside the container:
// always solutions/<problem>, whoever owns it on the host.
//
// Uniform on purpose, and not just for tidiness. Mounting into a path as deep
// as /src/_work/7/solutions/<problem> did not reliably produce the mount point
// on Docker Desktop — the chain was created only part of the way down and the
// build then reported the directory as missing. A two-level target avoids that,
// keeps `go test` output reading as neetcode150/solutions/<problem> rather than
// leaking a user id into it, and means the container never learns whose work it
// is holding.
func containerPkg(pkgDir string) string {
	return path.Join("solutions", filepath.Base(pkgDir))
}

// mounts lists the bind mounts, source first. Separated out so the golden test
// can assert that the repository root is absent and that every source is
// read-only.
func (d dockerRunner) mounts(req runRequest) []string {
	ro := func(host, inside string) string { return host + ":" + inside + ":ro" }
	return []string{
		// go.mod is NOT mounted. The image carries one declaring the module path
		// and nothing else, so the sandbox never needs a module proxy — and a
		// solution that reaches for a third-party package fails to resolve.
		ro(filepath.Join(req.Root, "internal", "testkit"), containerSrc+"/internal/testkit"),
		ro(filepath.Join(req.Root, "internal", "testbank"), containerSrc+"/internal/testbank"),
		ro(filepath.Join(req.Root, req.PkgDir), path.Join(containerSrc, containerPkg(req.PkgDir))),
		// The only writable mount, and per user: build entries are
		// content-addressed, but a cache shared between people is still a way to
		// put a compiled object into somebody else's build.
		filepath.Join(req.CacheDir, "go-build") + ":/gocache",
	}
}

// args builds the whole invocation. The security posture lives in this slice,
// which is why it is a separate function with a test of its own.
func (d dockerRunner) args(req runRequest, name string) []string {
	a := []string{
		"run", "--rm", "--init",
		"--name", name,
		// No network at all, which is what turns GOPROXY=off from advice into a
		// fact, and stops a solution from calling home.
		"--network", "none",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		// Memory below a gigabyte makes a cold compile of the test binary get
		// OOM-killed, which surfaces as "signal: killed" and explains nothing.
		"--memory", "1g", "--memory-swap", "1g",
		"--cpus", "1.0",
		"--pids-limit", "256",
		"--ulimit", "nofile=1024:1024",
		// exec is required, not an oversight: docker mounts a tmpfs noexec by
		// default, and `go test` builds the test binary into GOTMPDIR and runs
		// it from there, so without this every run dies with "permission
		// denied" after a successful compile. nosuid and nodev stay.
		"--tmpfs", "/tmp:rw,nosuid,nodev,exec,size=512m",
		// A missing image must be a clear error, not a surprise pull attempt
		// against a daemon we have just told to have no network.
		"--pull", "never",
		"-w", containerSrc,
	}
	a = append(a, "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()))
	for _, m := range d.mounts(req) {
		a = append(a, "-v", m)
	}
	// Passed explicitly rather than relied on from the image, so the invocation
	// describes its own behaviour and the test can assert it.
	//
	// GOTOOLCHAIN=local is not optional: go.mod asks for go 1.26.0, and with the
	// network off a mismatched toolchain would try to download one and report
	// something about toolchains rather than about the user's code.
	for _, e := range []string{
		"GOFLAGS=-mod=readonly",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
		"GOCACHE=/gocache",
		"GOTMPDIR=/tmp",
		"TMPDIR=/tmp",
		"HOME=/tmp",
	} {
		a = append(a, "-e", e)
	}
	a = append(a, d.image,
		"go", "test", "-json", "-count=1", "-run=.", "-skip=^$",
		"-timeout", goTestTimeout.String(),
		"./"+containerPkg(req.PkgDir)+"/")
	return a
}

func (d dockerRunner) RunTests(ctx context.Context, req runRequest) (testRun, error) {
	if err := os.MkdirAll(filepath.Join(req.CacheDir, "go-build"), 0o700); err != nil {
		return testRun{}, fmt.Errorf("could not prepare the build cache: %w", err)
	}
	if _, err := os.Stat(filepath.Join(req.Root, req.PkgDir)); err != nil {
		return testRun{}, fmt.Errorf("there is nothing to test yet: %w", err)
	}

	// Docker Desktop shares the host filesystem through a VM, and a directory
	// created moments ago is sometimes not yet visible there — pressing Start
	// and then Run straight away is enough to hit it. The directory is known to
	// exist, checked above, so waiting is the honest response rather than
	// reporting a workspace that is plainly there as missing.
	run, raw, runErr := d.once(ctx, req)
	for _, wait := range []time.Duration{300 * time.Millisecond, 700 * time.Millisecond, 1500 * time.Millisecond} {
		if runErr == nil || !mountNotVisible(raw) {
			break
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return run, ctx.Err()
		}
		run, raw, runErr = d.once(ctx, req)
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return run, fmt.Errorf("the run was cut off after %s — an endless loop in the solution would do that: %w",
			runTimeout, ctxErr)
	}

	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		// Docker's own failures share the exit channel with the test binary's,
		// and reporting one as the other would tell somebody their tests failed
		// when the daemon was simply gone. Keyed on what docker says rather than
		// on the exit code: the documented 125 is not what Docker Desktop
		// actually returns for a bad mount.
		if mountNotVisible(raw) {
			return run, fmt.Errorf("the sandbox could not see your workspace — try again in a moment")
		}
		if daemonRefused(raw) {
			return run, fmt.Errorf("the sandbox could not start: %s", tail(raw, 4))
		}
		switch exit.ExitCode() {
		case 137:
			return run, fmt.Errorf("the run was killed for using more than 1 GiB of memory — an unbounded allocation would do that")
		case 143:
			return run, fmt.Errorf("the run was stopped after %s", runTimeout)
		}
		return run, nil // compiled or not, `go test` had its say
	}
	if runErr != nil {
		return run, fmt.Errorf("could not run the sandbox: %w", runErr)
	}
	if !run.Ran {
		return run, fmt.Errorf("no tests ran: tests are missing or all were skipped; add test cases before submitting")
	}
	run.OK = run.Failed == 0
	return run, nil
}

// once is a single container run. Separated out so a retry is one call.
func (d dockerRunner) once(ctx context.Context, req runRequest) (testRun, []byte, error) {
	name := "nc-test-" + rand.Text()[:12]
	cmd := exec.CommandContext(ctx, d.cli, d.args(req, name)...)
	// Killing the client does not stop the container: the daemon owns the
	// process, and an attached client that dies simply detaches. So cancelling
	// has to go through docker, and WaitDelay stops us waiting forever on a
	// pipe the container is still holding open.
	cmd.Cancel = func() error { return exec.Command(d.cli, "kill", name).Run() }
	cmd.WaitDelay = 5 * time.Second
	defer func() { _ = exec.Command(d.cli, "rm", "-f", name).Run() }()

	raw, err := cmd.CombinedOutput()
	return readTestEvents(raw), raw, err
}

// mountNotVisible is Docker Desktop failing to find a host path it is meant to
// bind, which is a sharing delay rather than a missing directory.
func mountNotVisible(raw []byte) bool {
	out := string(raw)
	return strings.Contains(out, "error while creating mount source path") ||
		(strings.Contains(out, "Error response from daemon") && strings.Contains(out, "no such file or directory"))
}

// daemonRefused is docker declining to run at all, as opposed to the tests
// having something to say.
func daemonRefused(raw []byte) bool {
	out := string(raw)
	return strings.Contains(out, "Error response from daemon") ||
		strings.Contains(out, "Cannot connect to the Docker daemon") ||
		strings.HasPrefix(strings.TrimSpace(out), "docker:")
}

// tail is the last few lines of docker's own diagnostics. More than one,
// because docker's final line is often a usage hint and the reason is above it.
func tail(raw []byte, n int) string {
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) == 0 {
		return "no output"
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// lastLine is the tail of docker's own diagnostics, which is where it says why.
func lastLine(raw []byte) string {
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) == 0 {
		return "no output"
	}
	return lines[len(lines)-1]
}

// dockerAvailable reports whether a daemon is reachable and the image is
// present. Resolved once at startup: a mid-session fallback would silently
// drop isolation for whoever ran next.
func dockerAvailable(image string) error {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("no docker daemon: %s", lastLine(out))
	}
	if out, err := exec.CommandContext(ctx, "docker", "image", "inspect", image).CombinedOutput(); err != nil {
		return fmt.Errorf("the sandbox image %s is missing — build it with `docker build -t %s -f sandbox/Dockerfile .`: %s",
			image, image, lastLine(out))
	}
	return nil
}
