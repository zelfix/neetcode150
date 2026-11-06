package web

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The refusal that makes a shared instance safe to start. The local runner
// executes a guest's own Go as this user, with this user's filesystem and
// database, so there is nothing to warn about — only something to refuse.
func TestServingGuestsWithoutIsolationIsRefused(t *testing.T) {
	srv, _, _ := testSite(t)
	if _, err := srv.UseSandbox(SandboxLocal, "", 2); err != nil {
		t.Fatalf("owner-only instance should run locally: %v", err)
	}

	guest(t, srv, "dima")
	_, err := srv.UseSandbox(SandboxLocal, "", 2)
	if err == nil {
		t.Fatal("a guest was served with no isolation")
	}
	// The message has to say how to get out of it, or it is a dead end on a box
	// somebody has already shared.
	for _, want := range []string{"refusing to serve guests", "docker build", "nc user rm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

func TestSandboxDockerRefusesAMissingImage(t *testing.T) {
	srv, _, _ := testSite(t)
	_, err := srv.UseSandbox(SandboxDocker, "nc-sandbox:definitely-not-built", 1)
	if err == nil {
		t.Fatal("docker mode accepted an image that does not exist")
	}
	// Which reason comes back depends on the machine: a missing image where a
	// daemon is running, a missing daemon otherwise. Both have to be
	// actionable, because this is what somebody sees instead of a server.
	if !strings.Contains(err.Error(), "docker build") && !strings.Contains(err.Error(), "docker daemon") {
		t.Errorf("refusal explains nothing actionable: %v", err)
	}
}

func TestAnUnknownSandboxModeIsRejected(t *testing.T) {
	srv, _, _ := testSite(t)
	if _, err := srv.UseSandbox(Sandbox("sorta"), "", 1); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// ---------- the limiter ----------

// blockingRunner holds every run until released, so concurrency is observable.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	live    atomic.Int32
	peak    atomic.Int32
}

func (b *blockingRunner) RunTests(ctx context.Context, _ runRequest) (testRun, error) {
	n := b.live.Add(1)
	for {
		peak := b.peak.Load()
		if n <= peak || b.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	defer b.live.Add(-1)
	b.started <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return testRun{OK: true, Ran: true, Passed: 1}, nil
}

func TestTheLimiterCapsHowMuchCompilesAtOnce(t *testing.T) {
	const cap, callers = 2, 6
	inner := &blockingRunner{started: make(chan struct{}, callers), release: make(chan struct{})}
	l := newLimitedRunner(inner, cap)

	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Different problems, so the per-problem guard does not interfere.
			_, _ = l.RunTests(context.Background(), runRequest{UserID: int64(i), PkgDir: string(rune('a' + i))})
		}(i)
	}
	// Wait for the cap to be taken up, then let everybody through.
	for range cap {
		select {
		case <-inner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("the first runs never started")
		}
	}
	time.Sleep(100 * time.Millisecond) // give any over-admission a chance to show
	if peak := inner.peak.Load(); peak > cap {
		t.Errorf("%d ran at once with a cap of %d", peak, cap)
	}
	close(inner.release)
	wg.Wait()
	if peak := inner.peak.Load(); peak > cap {
		t.Errorf("peak concurrency %d exceeded the cap %d", peak, cap)
	}
}

// A double-click must not reserve a second container.
func TestASecondRunOfTheSameProblemIsRefusedRatherThanQueued(t *testing.T) {
	inner := &blockingRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	l := newLimitedRunner(inner, 4)
	req := runRequest{UserID: 1, PkgDir: "solutions/0001-two-sum"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = l.RunTests(context.Background(), req)
	}()
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first run never started")
	}

	if _, err := l.RunTests(context.Background(), req); err == nil {
		t.Fatal("a second run of the same problem was admitted")
	}
	close(inner.release)
	<-done

	// And once it is finished the same problem can run again.
	inner2 := &blockingRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	close(inner2.release)
	l2 := newLimitedRunner(inner2, 1)
	if _, err := l2.RunTests(context.Background(), req); err != nil {
		t.Fatalf("a fresh run was refused: %v", err)
	}
}

// Two people on the same problem are not each other's double-click.
func TestTwoPeopleMayRunTheSameProblemAtOnce(t *testing.T) {
	inner := &blockingRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	l := newLimitedRunner(inner, 4)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = l.RunTests(context.Background(), runRequest{UserID: int64(i + 1), PkgDir: "solutions/0001-two-sum"})
		}(i)
	}
	for range 2 {
		select {
		case <-inner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("both runs should have started")
		}
	}
	close(inner.release)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("person %d was refused: %v", i+1, err)
		}
	}
}

// A browser that navigates away gives its place back instead of holding it.
func TestWaitingForASlotStopsWhenTheRequestGoesAway(t *testing.T) {
	inner := &blockingRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(inner.release)
	l := newLimitedRunner(inner, 1)

	go func() { _, _ = l.RunTests(context.Background(), runRequest{UserID: 1, PkgDir: "a"}) }()
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first run never started")
	}

	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := l.RunTests(ctx, runRequest{UserID: 2, PkgDir: "b"}); err == nil {
		t.Fatal("a cancelled request still waited for a slot")
	}
}
