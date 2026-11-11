package web

import (
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/progress"
)

func mustProblem(t *testing.T, srv *Server, slug string) catalog.Problem {
	t.Helper()
	p, ok := srv.cat.BySlug(slug)
	if !ok {
		t.Fatalf("no problem %q in the test catalog", slug)
	}
	return p
}

// archive puts an attempt aside the way a submission does, so the comparison
// has something to read back.
func archive(t *testing.T, srv *Server, slug, language, code string) {
	t.Helper()
	s := ownerSession(t, srv)
	p, ok := srv.cat.BySlug(slug)
	if !ok {
		t.Fatalf("no problem %q in the test catalog", slug)
	}
	pending := pendingSubmission{Token: "tok-" + language, Code: code, Lang: language,
		Passed: true, Elapsed: 10 * time.Minute}
	if _, err := s.recordSubmission(p, pending, tutorEvaluation{Confidence: 4, Rung: 0, Feedback: "ok"}); err != nil {
		t.Fatal(err)
	}
}

// resolving a submission ends the attempt, so a re-solve has to be put back in
// progress before the comparison applies to it.
func reactivate(t *testing.T, srv *Server, prog *progress.Store, slug string) {
	t.Helper()
	if _, err := prog.SetStatus(slug, progress.Attempted, srv.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.BeginTimer(slug, srv.now()); err != nil {
		t.Fatal(err)
	}
}

func TestDiffAgainstTheLastArchivedAttempt(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}
	archive(t, srv, "two-sum", string(lang.Go), "package main\n\nfunc twoSum() {}\n")
	reactivate(t, srv, prog, "two-sum")

	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if !strings.Contains(body, "Changes since attempt:") {
		t.Fatal("the workspace offers no comparison against the archived attempt")
	}
	if !strings.Contains(body, "diff-del") || !strings.Contains(body, "diff-add") {
		t.Error("the comparison has no added and removed lines")
	}
}

func TestDiffSaysWhenNothingChanged(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}
	current, exists := ownerSession(t, srv).readSolution(mustProblem(t, srv, "two-sum"))
	if !exists {
		t.Fatal("create scaffolded no solution to compare")
	}
	archive(t, srv, "two-sum", string(lang.Go), current)
	reactivate(t, srv, prog, "two-sum")

	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if !strings.Contains(body, "Identical, line for line.") {
		t.Error("an unchanged re-solve is not called out; that is the question worth asking")
	}
}

// The same-language rule has to be visible, or the absence of a comparison
// looks like a bug rather than a decision.
func TestDiffSkipsAnotherLanguageAndSaysSo(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}
	archive(t, srv, "two-sum", string(lang.Python), "def two_sum():\n    pass\n")
	reactivate(t, srv, prog, "two-sum")

	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if strings.Contains(body, "Changes since attempt:") {
		t.Error("a Python archive was compared against a Go solution")
	}
	if !strings.Contains(body, "archived in another language") {
		t.Error("the page does not say why there is no comparison")
	}
}

// Submitting archives the code and leaves it in the file, so afterwards the
// newest archive *is* the working file. Offering the comparison then answers
// "identical, line for line" as a tautology — and that sentence is the page's
// way of asking whether a re-solve was memory rather than understanding, so it
// must never be said by accident.
func TestNoComparisonOnceTheAttemptIsOver(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}
	current, _ := ownerSession(t, srv).readSolution(mustProblem(t, srv, "two-sum"))
	archive(t, srv, "two-sum", string(lang.Go), current)

	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if strings.Contains(body, "Identical, line for line.") {
		t.Error("a finished submission was compared against itself")
	}
	if strings.Contains(body, "Changes since attempt:") {
		t.Error("a comparison is offered when no attempt is in progress")
	}
}

func TestNoArchiveMeansNoComparison(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}
	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if strings.Contains(body, "Changes since attempt:") || strings.Contains(body, "archived in another language") {
		t.Error("a first attempt was offered a comparison it cannot have")
	}
}

// What a passing run hands back, which is what the page renders in place of the
// comparison it drew at load time.
func TestDiffPayloadShapesTheThreeOutcomes(t *testing.T) {
	srv, _, _ := testSite(t)
	s := ownerSession(t, srv)
	p := mustProblem(t, srv, "two-sum")

	if payload := s.diffAgainstLast(nil, "package main\n", lang.Of(lang.Go), true).payload(); payload != nil {
		t.Error("a problem with no archive produced a comparison")
	}

	archive(t, srv, "two-sum", string(lang.Go), "package main\n\nfunc old() {}\n")
	attempts, err := s.progress.Scope().Attempts(p.Slug)
	if err != nil {
		t.Fatal(err)
	}

	changed := s.diffAgainstLast(attempts, "package main\n\nfunc new() {}\n", lang.Of(lang.Go), true).payload()
	if changed == nil {
		t.Fatal("no comparison against an archived attempt in the same language")
	}
	if changed.Added == 0 || changed.Removed == 0 || changed.Identical {
		t.Errorf("payload = %+v; want added and removed lines", changed)
	}
	if len(changed.Lines) == 0 || changed.Lines[0].Sign == "" {
		t.Error("lines carry no gutter mark, so they read the same copied out")
	}

	same := s.diffAgainstLast(attempts, "package main\n\nfunc old() {}\n", lang.Of(lang.Go), true).payload()
	if same == nil || !same.Identical {
		t.Errorf("payload = %+v; want Identical", same)
	}

	huge := strings.Repeat("x\n", maxDiffLines+2)
	big := s.diffAgainstLast(attempts, huge, lang.Of(lang.Go), true).payload()
	if big == nil || !big.TooBig {
		t.Errorf("payload = %+v; want TooBig past %d lines", big, maxDiffLines)
	}
	if len(big.Lines) != 0 {
		t.Error("a refused comparison still returned lines")
	}
}
