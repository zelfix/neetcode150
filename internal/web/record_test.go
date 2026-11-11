package web

import (
	"strings"
	"testing"
	"time"

	"neetcode150/internal/lang"
	"neetcode150/internal/progress"
)

// The record pane is swapped out by the editor when an attempt starts, which is
// exactly when it matters whether this problem has slipped before.
func TestRatingsStayReachableDuringAnAttempt(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	now := srv.now()

	if _, err := prog.RateHinted("two-sum", 4, 3, now.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.RateHinted("two-sum", 2, 5, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Before the attempt begins, the record pane carries the table.
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); !strings.Contains(body, "record-table") {
		t.Fatal("the record pane has no ratings table to begin with")
	}

	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}

	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if !strings.Contains(body, "record-table") {
		t.Error("the ratings table vanished once the attempt started")
	}
	if !strings.Contains(body, "rating slipped") {
		t.Error("the slipped flag is not shown; a fallen rating is the reason to look")
	}

	// The reading view used to show aggregates only.
	if body := get(t, h, "/problems/two-sum").Body.String(); !strings.Contains(body, "record-table") {
		t.Error("the reading view has no per-rating history")
	}
}

// A browser tab is easier to leave open than a terminal, and the web had no cap
// at all: cancelling an attempt opened the night before logged eleven hours.
func TestCancellingAForgottenAttemptLogsNothing(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	now := srv.now()

	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}

	cancelAfter := func(d time.Duration) int {
		t.Helper()
		if _, err := prog.ResetTimer("two-sum", now.Add(-d)); err != nil {
			t.Fatal(err)
		}
		if rec := postJSON(t, h, "/problems/two-sum/cancel", map[string]any{"code": "package main\n"}); rec.Code != 200 {
			t.Fatalf("cancel returned %d: %s", rec.Code, rec.Body)
		}
		return prog.Get("two-sum").MinutesSpent
	}

	if got := cancelAfter(11 * time.Hour); got != 0 {
		t.Errorf("MinutesSpent = %d after an overnight tab; want 0", got)
	}
	// A sitting inside the cap is still logged in full.
	if got := cancelAfter(45 * time.Minute); got != 45 {
		t.Errorf("MinutesSpent = %d for a 45 minute sitting; want 45", got)
	}
}

// Every web submission was archived as Go, whatever language it was written in,
// which makes the same-language rule that `nc diff` rests on quietly wrong.
func TestSubmissionArchivesTheLanguageItWasWrittenIn(t *testing.T) {
	srv, _, prog := testSite(t)
	s := ownerSession(t, srv)
	p, _ := srv.cat.BySlug("two-sum")

	pending := pendingSubmission{Token: "tok", Code: "def two_sum(nums, target):\n    return []\n",
		Lang: string(lang.Python), Passed: true, Elapsed: 20 * time.Minute}
	if _, err := s.recordSubmission(p, pending, tutorEvaluation{Confidence: 4, Rung: 1, Feedback: "fine"}); err != nil {
		t.Fatal(err)
	}

	attempts, err := prog.Scope().Attempts("two-sum")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("archived %d attempts, want 1", len(attempts))
	}
	if got := attempts[0].Language; got != string(lang.Python) {
		t.Errorf("archived language = %q, want %q", got, lang.Python)
	}
}

// The cap is one rule in one place, so the CLI and the site cannot drift.
func TestSubmittedMinutesShareTheClockCap(t *testing.T) {
	if minutes, capped := progress.ClockMinutes(11 * time.Hour); minutes != 0 || !capped {
		t.Errorf("ClockMinutes(11h) = %d, %v; want 0, true", minutes, capped)
	}
}
