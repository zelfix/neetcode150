package web

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"neetcode150/internal/lang"
	"neetcode150/internal/studydb"
)

// errAlreadyRunning is returned rather than queued. A double-click must not
// reserve a second container, and the workspace can only show one run at a
// time anyway, so the second press is noise and not a request.
var errAlreadyRunning = errors.New("a run is already in progress for this problem")

// limitedRunner caps how much compiling the box does at once.
//
// A decorator rather than a change to either implementation, so both are capped
// and neither knows about it. Nothing in this repository had a concurrency
// limit before: one person cannot overlap runs with themselves, but ten can,
// and ten simultaneous cold compiles will bury a small VM.
//
// This cap and the tutor's own live-session cap are separate pools on the same
// machine. Sized together: four containers at a CPU each, plus four agent
// processes, will oversubscribe anything small.
type limitedRunner struct {
	inner testRunner
	slots chan struct{}

	mu   sync.Mutex
	busy map[runKey]bool
}

type runKey struct {
	userID int64
	pkgDir string
}

// defaultMaxRuns leaves the machine something to answer requests with.
func defaultMaxRuns() int {
	if n := runtime.NumCPU() / 2; n > 1 {
		return min(n, 4)
	}
	return 1
}

func newLimitedRunner(inner testRunner, maxRuns int) *limitedRunner {
	if maxRuns < 1 {
		maxRuns = 1
	}
	return &limitedRunner{
		inner: inner,
		slots: make(chan struct{}, maxRuns),
		busy:  map[runKey]bool{},
	}
}

func (l *limitedRunner) RunTests(ctx context.Context, req runRequest) (testRun, error) {
	key := runKey{userID: req.UserID, pkgDir: req.PkgDir}
	l.mu.Lock()
	if l.busy[key] {
		l.mu.Unlock()
		return testRun{}, errAlreadyRunning
	}
	l.busy[key] = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.busy, key)
		l.mu.Unlock()
	}()

	// Waiting on ctx as well as on a slot means a browser that navigates away
	// gives its place back instead of holding it for the full timeout.
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return testRun{}, fmt.Errorf("gave up waiting for a free slot: %w", ctx.Err())
	}
	defer func() { <-l.slots }()

	return l.inner.RunTests(ctx, req)
}

// Sandbox names the isolation a server runs tests under.
type Sandbox string

const (
	// SandboxAuto uses a container when one is available.
	SandboxAuto Sandbox = "auto"
	// SandboxDocker insists on a container and refuses to start without one.
	SandboxDocker Sandbox = "docker"
	// SandboxLocal runs `go test` on this machine with no isolation at all.
	SandboxLocal Sandbox = "local"
)

func (s Sandbox) Valid() bool {
	return s == SandboxAuto || s == SandboxDocker || s == SandboxLocal
}

// UseSandbox installs the test runner and reports what it chose, for the log.
//
// Resolved once, at startup, and never re-decided: falling back mid-session
// because a daemon died would silently drop isolation for whoever ran next,
// which is the one failure nobody would notice.
//
// The Go image decides whether tests run in a container at all; it is the
// one that must exist. The Python image only decides whether Python is
// offered: a missing one disables Python with the build command in the
// message, and never drops to running Python unisolated on a shared box.
func (s *Server) UseSandbox(mode Sandbox, images SandboxImages, maxRuns int) (string, error) {
	if !mode.Valid() {
		return "", fmt.Errorf("--sandbox must be auto, docker or local, got %q", mode)
	}
	guests, err := s.hasGuests()
	if err != nil {
		return "", err
	}
	if maxRuns < 1 {
		maxRuns = defaultMaxRuns()
	}

	dockerErr := dockerAvailable(images.For(lang.Go))
	useDocker := false
	switch mode {
	case SandboxDocker:
		if dockerErr != nil {
			return "", fmt.Errorf("--sandbox=docker: %w", dockerErr)
		}
		useDocker = true
	case SandboxAuto:
		useDocker = dockerErr == nil
	}

	if !useDocker && guests {
		// A guest writes their own Go and the local runner executes it as this
		// user, with this user's filesystem and this user's database. Refusing
		// is the only honest option: a warning here would be a warning nobody
		// reads on a box that is already shared.
		why := "--sandbox=local"
		if mode == SandboxAuto {
			why = fmt.Sprintf("no container available (%v)", dockerErr)
		}
		return "", fmt.Errorf("refusing to serve guests without isolation: %s.\n"+
			"Build the sandbox image with `%s`, "+
			"or remove the guests with `nc user rm <name> --yes` to run single-user", why, images.BuildHint(lang.Go))
	}

	s.unavailable = map[lang.ID]string{}
	if useDocker {
		s.runTests = newLimitedRunner(newDockerRunner(images), maxRuns)
		python := "python: " + images.For(lang.Python)
		if err := pythonImageAvailable(images); err != nil {
			s.unavailable[lang.Python] = err.Error()
			python = "python: off — " + err.Error()
		}
		return fmt.Sprintf("sandbox: %s, %d at a time; %s", images.For(lang.Go), maxRuns, python), nil
	}
	s.runTests = newLimitedRunner(localRunner{}, maxRuns)
	python := "python: python3 on this machine"
	if err := pythonAvailable(); err != nil {
		s.unavailable[lang.Python] = err.Error() + ", so Python problems cannot be tested here"
		python = "python: off — " + err.Error()
	}
	return fmt.Sprintf("sandbox: none — tests run unisolated on this machine, %d at a time; %s", maxRuns, python), nil
}

// RunUnavailable says why tests cannot run in a language on this server, or
// "" when they can. Decided once at startup with the rest of UseSandbox.
func (s *Server) RunUnavailable(id lang.ID) string {
	return s.unavailable[id]
}

func pythonImageAvailable(images SandboxImages) error {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return imagePresent(ctx, images.For(lang.Python), images.BuildHint(lang.Python))
}

// hasGuests reports whether anybody other than the owner can sign in.
func (s *Server) hasGuests() (bool, error) {
	users, err := s.db.Users()
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.ID != studydb.OwnerID {
			return true, nil
		}
	}
	return false, nil
}
