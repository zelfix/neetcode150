package progress

import (
	"os"
	"path/filepath"
	"sync"
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

func TestShakyLapsesTwoRungsButStaysScheduled(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	// Climb to stage 4, high enough that a two-rung fall is distinguishable
	// from a reset to 1.
	for range 3 {
		now = now.AddDate(0, 0, 1)
		if _, err := s.Rate("two-sum", 5, now); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Get("two-sum").ReviewStage; got != 4 {
		t.Fatalf("setup: stage = %d, want 4", got)
	}

	e, err := s.SetStatus("two-sum", Shaky, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ReviewStage != 2 {
		t.Errorf("stage = %d, want 2 — a lapse falls two rungs, it does not reset", e.ReviewStage)
	}
	if e.NextReview == nil {
		t.Error("a shaky problem must stay in the review queue")
	}
}

// A 1 means the solution had to be handed over; a 2 means a strong hint was
// enough. Treating them alike sends something you nearly knew back to tomorrow.
func TestRatingOneResetsButRatingTwoOnlyLapses(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	climb := func(t *testing.T) *Store {
		t.Helper()
		s := newStore(t)
		if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if _, err := s.Rate("two-sum", 5, now); err != nil {
				t.Fatal(err)
			}
		}
		if got := s.Get("two-sum").ReviewStage; got != 4 {
			t.Fatalf("setup: stage = %d, want 4", got)
		}
		return s
	}

	lost := climb(t)
	e, err := lost.Rate("two-sum", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ReviewStage != 1 || e.Status != Shaky {
		t.Errorf("rating 1: stage = %d, status = %q; want 1, %q", e.ReviewStage, e.Status, Shaky)
	}
	if got := days(e, now); got != 1 {
		t.Errorf("rating 1: next review in %d days, want 1", got)
	}

	hinted := climb(t)
	e, err = hinted.Rate("two-sum", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ReviewStage != 2 || e.Status != Shaky {
		t.Errorf("rating 2: stage = %d, status = %q; want 2, %q", e.ReviewStage, e.Status, Shaky)
	}
	if got := days(e, now); got != IntervalDays(2) {
		t.Errorf("rating 2: next review in %d days, want %d", got, IntervalDays(2))
	}
}

// A lapse must not fall out of the ladder, however low it starts.
func TestLapseNeverLeavesTheLadder(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil { // stage 1
		t.Fatal(err)
	}
	e, err := s.Rate("two-sum", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ReviewStage != 1 {
		t.Errorf("stage = %d, want 1 — the floor, not 0", e.ReviewStage)
	}
	if e.NextReview == nil {
		t.Error("a lapsed problem must stay scheduled")
	}
}

func TestRateHintedRecordsTheRung(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	if _, err := s.RateHinted("two-sum", 4, 3, now); err != nil {
		t.Fatal(err)
	}
	r, ok := s.Get("two-sum").LastReview()
	if !ok {
		t.Fatal("no review recorded")
	}
	if r.Rung != 3 {
		t.Errorf("Rung = %d, want 3", r.Rung)
	}
	// Plain Rate means "rung not noted", which must read as 0, not as unaided
	// by accident of a later change.
	if _, err := s.Rate("valid-anagram", 4, now); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Get("valid-anagram").LastReview(); r.Rung != 0 {
		t.Errorf("Rate() recorded rung %d, want 0", r.Rung)
	}
}

func TestRateHintedRejectsAnOutOfRangeRung(t *testing.T) {
	s := newStore(t)
	for _, rung := range []int{-1, 6} {
		if _, err := s.RateHinted("two-sum", 4, rung, time.Now()); err == nil {
			t.Errorf("RateHinted(rung=%d) accepted, want an error", rung)
		}
	}
}

func TestTimerMeasuresASittingAndRateClearsIt(t *testing.T) {
	s := newStore(t)
	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.Local)

	if _, err := s.BeginTimer("two-sum", start); err != nil {
		t.Fatal(err)
	}
	// A second start must not discard time already spent.
	if _, err := s.BeginTimer("two-sum", start.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	d, ok := s.Get("two-sum").Elapsed(start.Add(25 * time.Minute))
	if !ok || d != 25*time.Minute {
		t.Errorf("Elapsed = %v, %v; want 25m, true", d, ok)
	}

	if _, err := s.ResetTimer("two-sum", start.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Get("two-sum").Elapsed(start.Add(40 * time.Minute)); d != 10*time.Minute {
		t.Errorf("after ResetTimer, Elapsed = %v, want 10m", d)
	}

	if _, err := s.Rate("two-sum", 4, start.Add(40*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("two-sum").Elapsed(start.Add(50 * time.Minute)); ok {
		t.Error("Rate left the clock running; a rating ends the sitting")
	}
}

func TestElapsedIgnoresAClockThatWentBackwards(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	if _, err := s.BeginTimer("two-sum", now); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("two-sum").Elapsed(now.Add(-time.Hour)); ok {
		t.Error("Elapsed reported a negative duration as valid")
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

func TestRateAppendsToHistory(t *testing.T) {
	s := newStore(t)
	day := time.Date(2026, 3, 1, 9, 0, 0, 0, time.Local)

	for i, c := range []int{4, 2, 3} {
		if _, err := s.Rate("two-sum", c, day.AddDate(0, 0, i*5)); err != nil {
			t.Fatalf("Rate(%d): %v", c, err)
		}
	}

	e := s.Get("two-sum")
	if len(e.History) != 3 {
		t.Fatalf("len(History) = %d, want 3", len(e.History))
	}
	for i, want := range []int{4, 2, 3} {
		if got := e.History[i].Confidence; got != want {
			t.Errorf("History[%d].Confidence = %d, want %d", i, got, want)
		}
	}
	// A 2 flags the problem shaky and resets the ladder to stage 1; the stage
	// recorded must be the one that rating produced, not the one before it.
	if got := e.History[1].Stage; got != 1 {
		t.Errorf("History[1].Stage = %d, want 1 after a low rating", got)
	}
	if e.Confidence != 3 {
		t.Errorf("Confidence = %d, want the last rating 3", e.Confidence)
	}
}

func TestHistoryRecordsCumulativeMinutes(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := s.AddMinutes("two-sum", 30, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rate("two-sum", 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMinutes("two-sum", 12, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rate("two-sum", 5, now); err != nil {
		t.Fatal(err)
	}

	e := s.Get("two-sum")
	if got := e.History[0].MinutesTotal; got != 30 {
		t.Errorf("History[0].MinutesTotal = %d, want 30", got)
	}
	// The second sitting is the gap, not the total: 42 - 30 = 12.
	if got := e.History[1].MinutesTotal; got != 42 {
		t.Errorf("History[1].MinutesTotal = %d, want 42", got)
	}
}

func TestSlippedSpotsAFallingRating(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := s.Rate("two-sum", 4, now); err != nil {
		t.Fatal(err)
	}
	if e := s.Get("two-sum"); e.Slipped() {
		t.Error("Slipped() on a single rating = true, want false")
	}
	if _, err := s.Rate("two-sum", 2, now); err != nil {
		t.Fatal(err)
	}
	if e := s.Get("two-sum"); !e.Slipped() {
		t.Error("Slipped() after 4 then 2 = false, want true")
	}
	if _, err := s.Rate("two-sum", 5, now); err != nil {
		t.Fatal(err)
	}
	if e := s.Get("two-sum"); e.Slipped() {
		t.Error("Slipped() after 2 then 5 = true, want false")
	}
}

func TestLastReviewReportsTheMostRecentRating(t *testing.T) {
	s := newStore(t)
	if _, ok := s.Get("two-sum").LastReview(); ok {
		t.Error("LastReview() on an unrated problem reported a review")
	}
	if _, err := s.Rate("two-sum", 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	r, ok := s.Get("two-sum").LastReview()
	if !ok || r.Confidence != 3 {
		t.Errorf("LastReview() = %+v, %v; want confidence 3", r, ok)
	}
}

// A progress.json written before history existed must load and keep working:
// the field is absent, not empty, and the next rating starts the list.
func TestHistoryIsBackwardCompatible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	legacy := `{"two-sum":{"status":"solved","confidence":4,"reviewStage":2,"minutesSpent":12}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e := s.Get("two-sum"); e.History != nil {
		t.Errorf("History = %v on a legacy entry, want nil", e.History)
	}
	if _, err := s.Rate("two-sum", 5, time.Now()); err != nil {
		t.Fatal(err)
	}
	e := s.Get("two-sum")
	if len(e.History) != 1 || e.History[0].MinutesTotal != 12 {
		t.Errorf("History = %+v, want one entry carrying the existing 12 minutes", e.History)
	}
}

// Two processes read, then both write. The second write serialises the whole
// map, so a change it never saw is silently reverted.
//
// The freshness check in syncFromDisk compares modification time and size, and
// this test aims straight at its blind spot: bumping minutesSpent from 10 to 11
// leaves the file exactly as long as it was, and Chtimes puts the timestamp
// back to stand in for a filesystem with coarse mtime granularity. Both halves
// are realistic — a one-digit rating change is the same length too, and HFS+
// keeps mtime to the second.
func TestWritesMergeWhenTheTimestampLooksUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	// Seed the file so both stores cache a real mtime and size.
	seed, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.AddMinutes("contains-duplicate", 10, now); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.SetStatus("two-sum", Attempted, now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stamp, size := info.ModTime(), info.Size()

	site, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// The CLI's change keeps the file the same length.
	if _, err := cli.Update("contains-duplicate", func(e *Entry) { e.MinutesSpent = 11 }); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != size {
		t.Fatalf("setup: file is %d bytes, want the original %d — the collision this test needs did not happen", after.Size(), size)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	// The site now writes a different problem out of its stale map.
	if _, err := site.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get("contains-duplicate").MinutesSpent; got != 11 {
		t.Errorf("the CLI's change was reverted: minutesSpent = %d, want 11", got)
	}
	if got := reloaded.Get("two-sum").Status; got != Solved {
		t.Errorf("the site's change was lost: status = %q, want %q", got, Solved)
	}
}

// A corrupt file must not be allowed to destroy state through the write path
// either: Update re-reads before it writes, and a failed parse there has to
// leave memory alone rather than start from an empty map.
func TestUpdateKeepsMemoryWhenTheFileIsCorrupt(t *testing.T) {
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
	if err := os.WriteFile(path, []byte(`{"two-sum": {"stat`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Writing an unrelated problem must not take two-sum down with it.
	if _, err := s.SetStatus("valid-anagram", Attempted, now); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("two-sum").Status; got != Solved {
		t.Errorf("two-sum became %q after a write over a corrupt file, want %q", got, Solved)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("the file left on disk does not parse: %v", err)
	}
	if got := reloaded.Get("two-sum").Status; got != Solved {
		t.Errorf("on disk, two-sum = %q, want %q", got, Solved)
	}
}

// Two stores on one file, writing different problems at once. Under -race this
// also checks the locking itself; the assertion is that every write lands.
func TestConcurrentWritersOnDifferentProblemsAllLand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")

	slugs := []string{"two-sum", "valid-anagram", "contains-duplicate", "group-anagrams"}
	stores := make([]*Store, len(slugs))
	for i := range stores {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
	}

	const perSlug = 20
	var wg sync.WaitGroup
	for i, slug := range slugs {
		wg.Add(1)
		go func(s *Store, slug string) {
			defer wg.Done()
			for range perSlug {
				if _, err := s.AddMinutes(slug, 1, time.Now()); err != nil {
					t.Errorf("AddMinutes(%s): %v", slug, err)
					return
				}
			}
		}(stores[i], slug)
	}
	wg.Wait()

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range slugs {
		if got := reloaded.Get(slug).MinutesSpent; got != perSlug {
			t.Errorf("%s has %d minutes, want %d — writes were lost", slug, got, perSlug)
		}
	}
}
