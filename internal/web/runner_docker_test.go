package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
)

// ---------- the invocation ----------

// The mount set is the whole security model, so it is asserted without needing
// a daemon: this test runs everywhere, including in CI where there is none.
func TestSandboxNeverMountsTheRepository(t *testing.T) {
	d := newDockerRunner("")
	req := runRequest{
		Root:     "/home/me/neetcode150",
		PkgDir:   filepath.Join("_work", "7", "solutions", "0001-two-sum"),
		UserID:   7,
		CacheDir: "/home/me/neetcode150/.nc-cache/users/7",
	}
	args := d.args(req, "nc-test-abc")

	var volumes []string
	for i, a := range args {
		if a == "-v" && i+1 < len(args) {
			volumes = append(volumes, args[i+1])
		}
	}
	if len(volumes) == 0 {
		t.Fatal("no mounts at all")
	}
	for _, v := range volumes {
		host := strings.SplitN(v, ":", 2)[0]
		// The repository root itself, mounted anywhere, would put
		// internal/studydb inside the sandbox — and with it every other
		// person's notes, progress and archived code.
		if host == req.Root || host == req.Root+string(filepath.Separator) {
			t.Errorf("the repository root is mounted: %q", v)
		}
		if strings.HasSuffix(host, "internal") {
			t.Errorf("the whole internal tree is mounted: %q", v)
		}
		if strings.Contains(v, "studydb") {
			t.Errorf("studydb is reachable from the sandbox: %q", v)
		}
	}
	// Everything except the build cache is read-only.
	for _, v := range volumes {
		writable := !strings.HasSuffix(v, ":ro")
		isCache := strings.Contains(v, filepath.Join(".nc-cache", "users", "7"))
		if writable && !isCache {
			t.Errorf("mount is writable but is not the cache: %q", v)
		}
		if isCache && writable && !strings.HasSuffix(v, ":/gocache") {
			t.Errorf("the cache is mounted somewhere unexpected: %q", v)
		}
	}
	// The project's own go.mod requires third-party modules the sandbox cannot
	// fetch with the network off, so the image's minimal one must win.
	for _, v := range volumes {
		if strings.Contains(v, "go.mod") || strings.Contains(v, "go.sum") {
			t.Errorf("the host's module files are mounted: %q", v)
		}
	}

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--network none", // makes GOPROXY=off a fact rather than advice
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--memory 1g",
		"--pids-limit 256",
		"--pull never",
		"GOTOOLCHAIN=local", // or a toolchain download is attempted with no network
		"GOPROXY=off",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("invocation is missing %q", want)
		}
	}
	// A tmpfs is noexec by default, and `go test` runs the test binary it
	// builds in GOTMPDIR, so dropping exec breaks every run after a clean
	// compile. Guard the fix.
	if !strings.Contains(joined, "exec") || !strings.Contains(joined, "/tmp:rw,nosuid,nodev,exec") {
		t.Error("the sandbox tmpfs is not executable; go test cannot run its binary")
	}
}

func TestEachPersonGetsTheirOwnBuildCache(t *testing.T) {
	d := newDockerRunner("")
	seen := map[string]bool{}
	for _, id := range []int64{1, 7} {
		req := runRequest{
			Root:     "/r",
			PkgDir:   "solutions/0001-two-sum",
			UserID:   id,
			CacheDir: filepath.Join("/r", ".nc-cache", "users", "x"+string(rune('0'+id))),
		}
		for _, m := range d.mounts(req) {
			if strings.HasSuffix(m, ":/gocache") {
				seen[m] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Errorf("two people shared one writable build cache: %v", seen)
	}
}

// ---------- against a real daemon ----------

// requireDocker skips rather than fails. CI has no daemon, and `go test ./...`
// must stay green there.
func requireDocker(t *testing.T) {
	t.Helper()
	if err := dockerAvailable(sandboxImage); err != nil {
		t.Skipf("no sandbox available: %v", err)
	}
}

// repoRoot is the project root, which is where testkit and testbank are mounted
// from.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not in a git checkout: %v", err)
	}
	return strings.TrimSpace(string(root))
}

// sandboxPackage writes a throwaway package under the gitignored guest tree and
// returns a request that runs it.
func sandboxPackage(t *testing.T, name string, files map[string]string) runRequest {
	t.Helper()
	root := repoRoot(t)
	pkgDir := filepath.Join("_work", "sandbox-test", "solutions", name)
	dir := filepath.Join(root, pkgDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Join(root, "_work", "sandbox-test")) })
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return runRequest{
		Root:     root,
		PkgDir:   pkgDir,
		UserID:   4242,
		CacheDir: filepath.Join(root, ".nc-cache", "users", "sandbox-test"),
	}
}

func runInSandbox(t *testing.T, req runRequest, wait time.Duration) (testRun, error) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), wait)
	defer stop()
	return newDockerRunner("").RunTests(ctx, req)
}

// The reason the sandbox exists. A guest writes their own Go, and under the
// per-user layout their package sits inside this module, so without the narrow
// mount set this import would compile and read everybody's data.
func TestASolutionCannotReachTheStudyDatabase(t *testing.T) {
	requireDocker(t)
	req := sandboxPackage(t, "nosy", map[string]string{
		"solution.go": "package nosy\n\nfunc answer() int { return 1 }\n",
		"solution_test.go": `package nosy

import (
	"testing"

	"neetcode150/internal/studydb"
)

func TestSteal(t *testing.T) {
	db, err := studydb.Open("/src")
	if err != nil {
		t.Fatal(err)
	}
	notes, _ := db.Owner().Document("two-sum", "notes")
	t.Log(notes)
}
`,
	})
	run, err := runInSandbox(t, req, 2*time.Minute)
	if err == nil && run.OK {
		t.Fatal("a solution read the study database")
	}
	if !strings.Contains(run.Output, "studydb") {
		t.Errorf("expected the import to be refused, got:\n%s", run.Output)
	}
}

func TestASolutionHasNoNetwork(t *testing.T) {
	requireDocker(t)
	req := sandboxPackage(t, "phoning", map[string]string{
		"solution.go": "package phoning\n\nfunc answer() int { return 1 }\n",
		"solution_test.go": `package phoning

import (
	"net"
	"testing"
	"time"
)

func TestDial(t *testing.T) {
	if _, err := net.DialTimeout("tcp", "1.1.1.1:53", 3*time.Second); err == nil {
		t.Fatal("REACHED THE NETWORK")
	}
}
`,
	})
	run, err := runInSandbox(t, req, 2*time.Minute)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, run.Output)
	}
	if strings.Contains(run.Output, "REACHED THE NETWORK") {
		t.Fatalf("the sandbox has network access:\n%s", run.Output)
	}
	if !run.OK {
		t.Errorf("the dial should simply fail, got:\n%s", run.Output)
	}
}

func TestASolutionCannotWriteOverItsOwnSource(t *testing.T) {
	requireDocker(t)
	req := sandboxPackage(t, "scribbler", map[string]string{
		"solution.go": "package scribbler\n\nfunc answer() int { return 1 }\n",
		"solution_test.go": `package scribbler

import (
	"os"
	"testing"
)

func TestWrite(t *testing.T) {
	if err := os.WriteFile("solution.go", []byte("package scribbler\n"), 0o644); err == nil {
		t.Fatal("WROTE TO SOURCE")
	}
}
`,
	})
	run, err := runInSandbox(t, req, 2*time.Minute)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, run.Output)
	}
	if strings.Contains(run.Output, "WROTE TO SOURCE") {
		t.Fatalf("the source mount is writable:\n%s", run.Output)
	}
}

// An endless loop is the ordinary accident this has to survive, and the message
// has to say so rather than leave a blank pane.
func TestAnEndlessLoopIsCutOff(t *testing.T) {
	requireDocker(t)
	req := sandboxPackage(t, "spinner", map[string]string{
		"solution.go": "package spinner\n\nfunc answer() int { return 1 }\n",
		"solution_test.go": `package spinner

import "testing"

func TestSpin(t *testing.T) {
	for {
	}
}
`,
	})
	run, err := runInSandbox(t, req, 3*time.Minute)
	// go test's own -timeout fires first and names the test, which is the
	// useful diagnostic; the container kill is the backstop behind it.
	if err == nil && run.OK {
		t.Fatal("an endless loop was reported as a pass")
	}
	if !strings.Contains(run.Output, "timed out") && err == nil {
		t.Errorf("expected a timeout, got:\n%s", run.Output)
	}
}

// A problem that solves cleanly must still pass, or none of the above matters.
func TestAnOrdinarySolutionStillPasses(t *testing.T) {
	requireDocker(t)
	req := sandboxPackage(t, "fine", map[string]string{
		"solution.go": "package fine\n\nfunc double(n int) int { return n * 2 }\n",
		"solution_test.go": `package fine

import "testing"

func TestDouble(t *testing.T) {
	if double(3) != 6 {
		t.Fatal("bad arithmetic")
	}
}
`,
	})
	run, err := runInSandbox(t, req, 2*time.Minute)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, run.Output)
	}
	if !run.OK || run.Passed != 1 {
		t.Fatalf("ok=%v passed=%d\n%s", run.OK, run.Passed, run.Output)
	}
}

// A generated practice test imports internal/testkit, which is the one import
// that has to keep working.
func TestTheGeneratedPracticeSuiteStillCompiles(t *testing.T) {
	requireDocker(t)
	_ = catalog.Problem{}
	req := sandboxPackage(t, "practice", map[string]string{
		"solution.go": "package practice\n\nfunc answer() int { return 1 }\n",
		"solution_test.go": `package practice

import (
	"testing"

	"neetcode150/internal/testkit"
)

func TestKitIsReachable(t *testing.T) {
	testkit.Run(t, ` + "`" + `{"slug":"none","cases":[]}` + "`" + `)
}
`,
	})
	run, err := runInSandbox(t, req, 2*time.Minute)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, run.Output)
	}
	if strings.Contains(run.Output, "cannot find package") || strings.Contains(run.Output, "is not in std") {
		t.Fatalf("testkit is not importable from a guest package:\n%s", run.Output)
	}
}
