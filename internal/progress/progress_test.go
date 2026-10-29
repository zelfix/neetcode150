package progress

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"neetcode150/internal/catalog"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Load(filepath.Join(t.TempDir(), "progress.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

// days is the number of calendar days from `from` to the scheduled review.
func days(e Entry, from time.Time) int {
	if e.NextReview == nil {
		return -1
	}
	y0, m0, d0 := from.Date()
	y1, m1, d1 := e.NextReview.Date()
	a := time.Date(y0, m0, d0, 0, 0, 0, 0, from.Location())
	b := time.Date(y1, m1, d1, 0, 0, 0, 0, e.NextReview.Location())
	return int(b.Sub(a).Hours() / 24)
}

func TestSolveEntersTheReviewLadder(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	e, err := s.SetStatus("two-sum", Solved, now)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if e.ReviewStage != 1 {
		t.Errorf("stage = %d, want 1", e.ReviewStage)
	}
	if got := days(e, now); got != 1 {
		t.Errorf("next review in %d days, want 1", got)
	}
	if e.FirstSolved == nil || e.LastSolved == nil {
		t.Error("solve timestamps not recorded")
	}
	if e.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", e.Attempts)
	}
}

func TestConfidentReviewsWalkUpTheLadder(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}

	wantDays := []int{3, 7, 16, 35, 90, 90}
	for i, want := range wantDays {
		now = now.AddDate(0, 0, want)
		e, err := s.Rate("two-sum", 5, now)
		if err != nil {
			t.Fatalf("Rate %d: %v", i, err)
		}
		if got := days(e, now); got != want {
			t.Errorf("review %d: next in %d days, want %d (stage %d)", i+1, got, want, e.ReviewStage)
		}
	}
	if e := s.Get("two-sum"); e.ReviewStage != MaxStage {
		t.Errorf("stage = %d, want the ladder to cap at %d", e.ReviewStage, MaxStage)
	}
}

func TestLowConfidenceResetsAndFlagsShaky(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		now = now.AddDate(0, 0, 7)
		if _, err := s.Rate("two-sum", 5, now); err != nil {
			t.Fatal(err)
		}
	}

	e, err := s.Rate("two-sum", 1, now)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if e.Status != Shaky {
		t.Errorf("status = %q, want %q", e.Status, Shaky)
	}
	if e.ReviewStage != 1 {
		t.Errorf("stage = %d, want 1 after a lost attempt", e.ReviewStage)
	}
	if got := days(e, now); got != 1 {
		t.Errorf("next review in %d days, want 1", got)
	}
}

func TestShakyDropsOneRungButStaysScheduled(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	if _, err := s.Rate("two-sum", 5, now); err != nil { // stage 2
		t.Fatal(err)
	}

	e, err := s.SetStatus("two-sum", Shaky, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ReviewStage != 1 {
		t.Errorf("stage = %d, want 1", e.ReviewStage)
	}
	if e.NextReview == nil {
		t.Error("a shaky problem must stay in the review queue")
	}
}

func TestResetToNewLeavesTheLadder(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	e, err := s.SetStatus("two-sum", New, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.NextReview != nil || e.ReviewStage != 0 {
		t.Errorf("reset left stage %d / next %v", e.ReviewStage, e.NextReview)
	}
}

func TestRateRejectsOutOfRange(t *testing.T) {
	s := newStore(t)
	for _, c := range []int{0, -1, 6} {
		if _, err := s.Rate("two-sum", c, time.Now()); err == nil {
			t.Errorf("Rate(%d) accepted, want an error", c)
		}
	}
}

func TestProgressSurvivesAReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	if _, err := first.AddMinutes("two-sum", 25, now); err != nil {
		t.Fatal(err)
	}

	second, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	e := second.Get("two-sum")
	if e.Status != Solved {
		t.Errorf("status = %q after reload, want %q", e.Status, Solved)
	}
	if e.MinutesSpent != 25 {
		t.Errorf("minutes = %d after reload, want 25", e.MinutesSpent)
	}
}

func TestLoadTreatsMissingFileAsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Get("two-sum").Status; got != New {
		t.Errorf("status = %q, want %q", got, New)
	}
}

var testProblems = []catalog.Problem{
	{Slug: "contains-duplicate", Order: 0, Pattern: "Arrays & Hashing"},
	{Slug: "valid-anagram", Order: 1, Pattern: "Arrays & Hashing"},
	{Slug: "two-sum", Order: 2, Pattern: "Arrays & Hashing"},
}

func TestNextPrefersDueReviewsThenInProgressThenNew(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	p, why, ok := s.Next(testProblems, now)
	if !ok || p.Slug != "contains-duplicate" {
		t.Fatalf("on an empty slate got %q (%s), want contains-duplicate", p.Slug, why)
	}

	if _, err := s.SetStatus("valid-anagram", Attempted, now); err != nil {
		t.Fatal(err)
	}
	p, why, _ = s.Next(testProblems, now)
	if p.Slug != "valid-anagram" {
		t.Errorf("got %q (%s), want the in-progress problem", p.Slug, why)
	}

	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	later := now.AddDate(0, 0, 2) // past the 1-day first interval
	p, why, _ = s.Next(testProblems, later)
	if p.Slug != "two-sum" || why != "due for review" {
		t.Errorf("got %q (%s), want two-sum due for review", p.Slug, why)
	}
}

func TestSummarizeCountsStatusesAndDueReviews(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus("valid-anagram", Attempted, now); err != nil {
		t.Fatal(err)
	}

	sum := s.Summarize(testProblems, now.AddDate(0, 0, 2))
	if sum.Total != 3 || sum.Solved != 1 || sum.Attempted != 1 || sum.New != 1 {
		t.Errorf("unexpected summary: %+v", sum)
	}
	if sum.DueNow != 1 {
		t.Errorf("DueNow = %d, want 1", sum.DueNow)
	}
	if sum.Percent() != 33 {
		t.Errorf("Percent() = %d, want 33", sum.Percent())
	}
}

// The CLI and the site are separate processes on one file. A store that cached
// the file for its lifetime would show a stale dashboard after `nc rate`.
func TestStoreSeesAnotherProcessWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	site, err := Load(path) // stands in for a long-running `nc serve`
	if err != nil {
		t.Fatal(err)
	}
	if got := site.Get("two-sum").Status; got != New {
		t.Fatalf("status = %q, want %q", got, New)
	}

	cli, err := Load(path) // stands in for `nc rate` in the terminal
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	bumpMTime(t, path)

	if got := site.Get("two-sum").Status; got != Solved {
		t.Errorf("the site still reads %q — it did not reload the file", got)
	}
	if sum := site.Summarize(testProblems, now); sum.Solved != 1 {
		t.Errorf("Summarize saw %d solved, want 1", sum.Solved)
	}
	if p, why, _ := site.Next(testProblems, now); p.Slug == "two-sum" {
		t.Errorf("Next still offers the solved problem (%s)", why)
	}
}

// A write through the site must not revert a change the CLI made first.
func TestWritesDoNotClobberAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	site, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cli.SetStatus("contains-duplicate", Solved, now); err != nil {
		t.Fatal(err)
	}
	bumpMTime(t, path)

	// The site writes a different problem; the CLI's entry must survive.
	if _, err := site.SetStatus("two-sum", Attempted, now); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get("contains-duplicate").Status; got != Solved {
		t.Errorf("the CLI's change was lost: %q", got)
	}
	if got := reloaded.Get("two-sum").Status; got != Attempted {
		t.Errorf("the site's change was lost: %q", got)
	}
}

func TestReloadIgnoresACorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	// A truncated write must not wipe what is already in memory.
	if err := os.WriteFile(path, []byte(`{"two-sum": {"stat`), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpMTime(t, path)

	if got := s.Get("two-sum").Status; got != Solved {
		t.Errorf("a corrupt file dropped the in-memory entry: %q", got)
	}
}

// bumpMTime pushes the file's modification time forward, so the change is
// visible regardless of the filesystem's timestamp granularity.
func bumpMTime(t *testing.T, path string) {
	t.Helper()
	ahead := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, ahead, ahead); err != nil {
		t.Fatal(err)
	}
}

// A problem rated late in the evening must be in the queue the next morning,
// not 24 hours later to the minute.
func TestReviewFallsDueByTheDayNotTheHour(t *testing.T) {
	s := newStore(t)
	evening := time.Date(2026, 10, 28, 21, 37, 0, 0, time.Local)
	if _, err := s.Rate("group-anagrams", 3, evening); err != nil {
		t.Fatal(err)
	}

	e := s.Get("group-anagrams")
	if e.NextReview == nil {
		t.Fatal("no review scheduled")
	}
	if h, m, sec := e.NextReview.Clock(); h != 0 || m != 0 || sec != 0 {
		t.Errorf("scheduled at %02d:%02d:%02d, want local midnight", h, m, sec)
	}
	if got := e.NextReview.Format("2006-01-02"); got != "2026-10-29" {
		t.Errorf("scheduled for %s, want 2026-10-29", got)
	}

	morning := time.Date(2026, 10, 29, 9, 0, 0, 0, time.Local)
	if !e.Due(morning) {
		t.Errorf("not due at %s though it was scheduled for that day", morning.Format("2006-01-02 15:04"))
	}
	sameEvening := time.Date(2026, 10, 28, 23, 59, 0, 0, time.Local)
	if e.Due(sameEvening) {
		t.Error("due on the day it was rated — the interval was skipped")
	}
}

// The due hour must not creep later with each review.
func TestReviewHourDoesNotDrift(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 28, 21, 37, 0, 0, time.Local)
	for i := range 4 {
		if _, err := s.Rate("two-sum", 5, now); err != nil {
			t.Fatalf("review %d: %v", i, err)
		}
		e := s.Get("two-sum")
		if h, _, _ := e.NextReview.Clock(); h != 0 {
			t.Fatalf("review %d scheduled at hour %d, want midnight", i, h)
		}
		// Sit down to review late in the evening of the due date.
		now = e.NextReview.Add(21*time.Hour + 37*time.Minute)
	}
}
