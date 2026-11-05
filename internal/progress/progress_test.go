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
	t.Cleanup(func() { s.Close() })
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

	wantStages := []int{2, 3, 4, 5, 6, 6}
	nominal := []int{3, 7, 16, 35, 90, 90}
	previous := 1
	for i, wantStage := range wantStages {
		now = now.AddDate(0, 0, nominal[i])
		e, err := s.Rate("two-sum", 5, now)
		if err != nil {
			t.Fatalf("Rate %d: %v", i, err)
		}
		if e.ReviewStage != wantStage {
			t.Errorf("review %d: stage = %d, want %d", i+1, e.ReviewStage, wantStage)
		}
		// Unaided fives earn ease, so each interval is the nominal rung or
		// wider, and never narrower than the one before it.
		got := days(e, now)
		if got < nominal[i] {
			t.Errorf("review %d: next in %d days, want at least the %d-day rung", i+1, got, nominal[i])
		}
		if got < previous {
			t.Errorf("review %d: next in %d days, narrower than the previous %d", i+1, got, previous)
		}
		previous = got
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

	if got := s.Get("two-sum").Status; got != Solved {
		t.Errorf("a corrupt file dropped the in-memory entry: %q", got)
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

// Independent connections must merge writes without filesystem freshness checks.
func TestWritesMergeAcrossDatabaseConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	site, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	cli, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	now := time.Now()
	if _, err := site.AddMinutes("contains-duplicate", 10, now); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.AddMinutes("contains-duplicate", 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := site.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	if got := site.Get("contains-duplicate").MinutesSpent; got != 11 {
		t.Fatalf("lost another connection's write: %d", got)
	}
	if got := cli.Get("two-sum").Status; got != Solved {
		t.Fatalf("stale status: %s", got)
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

func TestSittingsDerivesPerSittingFactsFromTheHistory(t *testing.T) {
	e := Entry{History: []Review{
		{Date: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC), Confidence: 3, Stage: 2, MinutesTotal: 26, Rung: 3},
		{Date: time.Date(2026, 3, 18, 9, 0, 0, 0, time.UTC), Confidence: 4, Stage: 3, MinutesTotal: 47, Rung: 3},
		{Date: time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC), Confidence: 2, Stage: 1, MinutesTotal: 47, Rung: 5},
	}}

	got := Sittings(e)
	if len(got) != 3 {
		t.Fatalf("got %d sittings, want 3", len(got))
	}

	// The stored total is cumulative, so a sitting is the difference.
	if got[0].Minutes != 26 || got[1].Minutes != 21 {
		t.Errorf("minutes = %d, %d; want 26, 21", got[0].Minutes, got[1].Minutes)
	}
	// A total that did not move means the clock was not running.
	if got[2].Minutes != 0 {
		t.Errorf("minutes = %d on a sitting the clock missed, want 0", got[2].Minutes)
	}

	// The first rating has nothing to have slipped from.
	if got[0].Slipped {
		t.Error("the first rating cannot have slipped")
	}
	if got[1].Slipped {
		t.Error("a rating that rose was reported as slipped")
	}
	if !got[2].Slipped {
		t.Error("a rating that fell was not reported as slipped")
	}

	// A 4 that needed rung 3 is the signature of an inflated self-assessment.
	if got[0].Generous {
		t.Error("a 3 at rung 3 is honest, not generous")
	}
	if !got[1].Generous {
		t.Error("a 4 at rung 3 should be flagged")
	}

	if got[1].IntervalDays != IntervalDays(3) {
		t.Errorf("intervalDays = %d, want %d", got[1].IntervalDays, IntervalDays(3))
	}
}

func TestSittingsOnAnEmptyHistory(t *testing.T) {
	if got := Sittings(Entry{}); len(got) != 0 {
		t.Errorf("got %d sittings from no history, want none", len(got))
	}
}

// The ladder is the documented promise — 1, 3, 7, 16, 35, 90 — and a problem
// with no history to stretch it should land on exactly those days, give or take
// the spread that stops reviews piling onto one date.
func TestNeutralEaseFollowsTheDocumentedLadder(t *testing.T) {
	for stage := 1; stage <= MaxStage; stage++ {
		want := reviewIntervals[stage]
		got := interval(Entry{ReviewStage: stage}, "two-sum")
		spread := want * jitterPercent / 100
		if got < want-spread || got > want+spread {
			t.Errorf("stage %d: interval %d days, want %d ± %d", stage, got, want, spread)
		}
	}
}

// Rung 3 is the invariant spelled out and rung 4 is pseudocode handed over.
// Neither is recall, so neither may buy a longer interval, however good the
// sitting felt afterwards.
func TestRungThreeHoldsTheStageAndRungFourLapses(t *testing.T) {
	cases := []struct {
		rung      int
		wantStage int
		why       string
	}{
		{0, 5, "unaided, so it advances"},
		{2, 5, "a nudge is still recall"},
		{3, 4, "the invariant was supplied, so it holds"},
		{4, 2, "pseudocode was handed over, so it falls"},
		{5, 2, "the answer was handed over, so it falls"},
	}
	for _, c := range cases {
		s := newStore(t)
		now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
		// Walk up to stage 4 without hints, then rate once at the rung.
		if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			now = now.AddDate(0, 0, 30)
			if _, err := s.RateHinted("two-sum", 5, 0, now); err != nil {
				t.Fatal(err)
			}
		}
		if e := s.Get("two-sum"); e.ReviewStage != 4 {
			t.Fatalf("setup left stage %d, want 4", e.ReviewStage)
		}
		now = now.AddDate(0, 0, 30)
		e, err := s.RateHinted("two-sum", 4, c.rung, now)
		if err != nil {
			t.Fatal(err)
		}
		if e.ReviewStage != c.wantStage {
			t.Errorf("rung %d: stage = %d, want %d — %s", c.rung, e.ReviewStage, c.wantStage, c.why)
		}
	}
}

// Two problems can sit on the same rung of the ladder without being equally
// secure. Ease is what tells them apart.
func TestEaseStretchesTheLadderPerProblem(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for _, slug := range []string{"two-sum", "valid-anagram"} {
		if _, err := s.SetStatus(slug, Solved, now); err != nil {
			t.Fatal(err)
		}
	}
	now = now.AddDate(0, 0, 1)
	// Same rating, same resulting stage; one needed the invariant, one did not.
	easy, err := s.RateHinted("two-sum", 4, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	hard, err := s.RateHinted("valid-anagram", 4, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if hard.Ease >= easy.Ease {
		t.Errorf("ease: hinted %.2f, unaided %.2f — the hinted one should be trusted less", hard.Ease, easy.Ease)
	}
	if got := days(hard, now); got >= days(easy, now) {
		t.Errorf("hinted comes back in %d days, unaided in %d — the hinted one should come back sooner",
			got, days(easy, now))
	}
}

// Ease is bounded so that the ladder stays recognisable: no amount of history
// may invert it or run an interval away.
func TestEaseStaysWithinItsBounds(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		now = now.AddDate(0, 0, 1)
		if _, err := s.RateHinted("two-sum", 5, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	if e := s.Get("two-sum"); e.Ease > easeMax {
		t.Errorf("ease ran to %.2f, want no more than %.2f", e.Ease, easeMax)
	}
	for range 30 {
		now = now.AddDate(0, 0, 1)
		if _, err := s.RateHinted("two-sum", 1, 5, now); err != nil {
			t.Fatal(err)
		}
	}
	if e := s.Get("two-sum"); e.Ease < easeMin {
		t.Errorf("ease fell to %.2f, want no less than %.2f", e.Ease, easeMin)
	}
}

// The spread must come from the problem, not from a random source: a date
// already shown to the user cannot move when the entry is read again.
func TestTheSpreadIsStableForTheSameProblem(t *testing.T) {
	e := Entry{ReviewStage: MaxStage}
	first := interval(e, "two-sum")
	for range 5 {
		if got := interval(e, "two-sum"); got != first {
			t.Fatalf("interval moved between reads: %d then %d", first, got)
		}
	}
	// And it must actually spread: some other problem on the same rung should
	// land on a different day.
	same := 0
	others := []string{"valid-anagram", "contains-duplicate", "group-anagrams", "top-k-frequent-elements"}
	for _, slug := range others {
		if interval(e, slug) == first {
			same++
		}
	}
	if same == len(others) {
		t.Errorf("every problem on stage %d landed on day %d — nothing is being spread", MaxStage, first)
	}
}

func TestUndoRestoresThePreviousSchedule(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	before, err := s.RateHinted("two-sum", 4, 0, now)
	if err != nil {
		t.Fatal(err)
	}

	// The slip: a 5 that should have been a 2.
	later := now.AddDate(0, 0, 3)
	if _, err := s.RateHinted("two-sum", 5, 0, later); err != nil {
		t.Fatal(err)
	}
	after, dropped, ok, err := s.Undo("two-sum", later)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Undo reported nothing to undo")
	}
	if dropped.Confidence != 5 {
		t.Errorf("dropped rating %d, want the 5", dropped.Confidence)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history has %d ratings, want %d", len(after.History), len(before.History))
	}
	if after.ReviewStage != before.ReviewStage {
		t.Errorf("stage = %d, want %d back", after.ReviewStage, before.ReviewStage)
	}
	if after.Confidence != before.Confidence {
		t.Errorf("confidence = %d, want %d back", after.Confidence, before.Confidence)
	}
	if after.NextReview == nil || before.NextReview == nil || !after.NextReview.Equal(*before.NextReview) {
		t.Errorf("next review = %v, want %v back", after.NextReview, before.NextReview)
	}
}

// Time spent is not given back by an undo: the sitting happened whichever
// rating was recorded for it.
func TestUndoKeepsTheMinutesAndThePerSittingSplit(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.AddMinutes("two-sum", 20, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateHinted("two-sum", 4, 0, now); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 3)
	if _, err := s.AddMinutes("two-sum", 15, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateHinted("two-sum", 5, 0, now); err != nil {
		t.Fatal(err)
	}
	e, _, _, err := s.Undo("two-sum", now)
	if err != nil {
		t.Fatal(err)
	}
	if e.MinutesSpent != 35 {
		t.Errorf("MinutesSpent = %d, want the 35 actually spent", e.MinutesSpent)
	}
	sit := Sittings(e)
	if len(sit) != 1 {
		t.Fatalf("got %d sittings, want 1", len(sit))
	}
	if sit[0].Minutes != 20 {
		t.Errorf("first sitting = %d minutes, want 20", sit[0].Minutes)
	}
}

func TestUndoOnAProblemWithNoHistory(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	_, _, ok, err := s.Undo("two-sum", now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Undo claimed to undo something on an untouched problem")
	}
}

func TestAmendReplacesTheLastRating(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.SetStatus("two-sum", Solved, now); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	if _, err := s.RateHinted("two-sum", 5, 0, now); err != nil {
		t.Fatal(err)
	}
	e, ok, err := s.Amend("two-sum", 2, 4, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Amend reported nothing to amend")
	}
	if len(e.History) != 1 {
		t.Errorf("history has %d ratings, want the one amended in place", len(e.History))
	}
	r, _ := e.LastReview()
	if r.Confidence != 2 || r.Rung != 4 {
		t.Errorf("last rating = %d at rung %d, want 2 at rung 4", r.Confidence, r.Rung)
	}
	if e.Status != Shaky {
		t.Errorf("status = %s, want shaky after a 2", e.Status)
	}
}

func TestAmendRejectsAnOutOfRangeRating(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.RateHinted("two-sum", 4, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Amend("two-sum", 6, 0, now); err == nil {
		t.Error("Amend accepted a confidence of 6")
	}
	if _, _, err := s.Amend("two-sum", 4, 9, now); err == nil {
		t.Error("Amend accepted a rung of 9")
	}
}

// Entries written before ease existed carry none. Replaying their history is
// what fills it in, so an undo or amend on old data schedules like new data.
func TestEaseIsDerivedForHistoryWithoutIt(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	e := Entry{
		Status:      Solved,
		Attempts:    2,
		Confidence:  4,
		ReviewStage: 3,
		History: []Review{
			{Date: now.AddDate(0, 0, -10), Confidence: 4, Stage: 2, Rung: 3},
			{Date: now, Confidence: 4, Stage: 3, Rung: 3},
		},
	}
	if e.ease() != easeDefault {
		t.Fatalf("ease() = %.2f on an entry without one, want the neutral %.2f", e.ease(), easeDefault)
	}
	replay(&e, "two-sum")
	if e.Ease == 0 {
		t.Error("replay left the ease unset")
	}
	if e.Ease >= easeDefault {
		t.Errorf("ease = %.2f after two hinted sittings, want below %.2f", e.Ease, easeDefault)
	}
	if len(e.History) != 2 {
		t.Errorf("replay left %d ratings, want 2", len(e.History))
	}
	// Both sittings needed the invariant, so the stage should have been held
	// rather than walked up twice.
	if e.ReviewStage != 1 {
		t.Errorf("stage = %d, want 1 — neither sitting was recall", e.ReviewStage)
	}
}

func TestWeakestRanksTheLeastSecureAndSaysWhy(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	// Solid: unaided and comfortable.
	if _, err := s.RateHinted("contains-duplicate", 5, 0, now); err != nil {
		t.Fatal(err)
	}
	// Weak: handed the answer, and it fell since last time.
	if _, err := s.RateHinted("two-sum", 4, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateHinted("two-sum", 2, 5, now.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}

	weak := s.Weakest(testProblems, 0)
	if len(weak) == 0 {
		t.Fatal("nothing was flagged")
	}
	if weak[0].Problem.Slug != "two-sum" {
		t.Errorf("weakest is %q, want two-sum", weak[0].Problem.Slug)
	}
	if len(weak[0].Reasons) == 0 {
		t.Error("the weakest problem came with no reasons")
	}
	for _, w := range weak {
		if w.Problem.Slug == "contains-duplicate" {
			t.Error("an unaided 5 was flagged as weak")
		}
	}
	if got := s.Weakest(testProblems, 1); len(got) != 1 {
		t.Errorf("limit 1 returned %d", len(got))
	}
}

func TestReviewQueuePutsTheMostOverdueFirstAndCapsTheDay(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	// Solved two days ago, so the first 1-day interval has already come due.
	for _, slug := range []string{"contains-duplicate", "valid-anagram", "two-sum"} {
		if _, err := s.SetStatus(slug, Solved, now.AddDate(0, 0, -2)); err != nil {
			t.Fatal(err)
		}
	}
	// Push two of them further into the past so the order is unambiguous.
	if _, err := s.RateHinted("two-sum", 4, 0, now.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateHinted("valid-anagram", 4, 0, now.AddDate(0, 0, -10)); err != nil {
		t.Fatal(err)
	}

	queue := s.ReviewQueue(testProblems, now, 0)
	if len(queue) != 3 {
		t.Fatalf("queue has %d problems, want all 3 due", len(queue))
	}
	if queue[0].Slug != "two-sum" {
		t.Errorf("queue starts with %q, want the longest overdue two-sum", queue[0].Slug)
	}
	if capped := s.ReviewQueue(testProblems, now, 2); len(capped) != 2 {
		t.Errorf("limit 2 returned %d", len(capped))
	}
}

func TestPatternsReportHowSecurelyEachIsHeld(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	// Two of the three are in one pattern: one unaided, one on a strong hint.
	if _, err := s.RateHinted("contains-duplicate", 5, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateHinted("valid-anagram", 2, 5, now); err != nil {
		t.Fatal(err)
	}

	reports := s.Patterns(testProblems, now)
	if len(reports) != 1 {
		t.Fatalf("got %d patterns, want the one the fixture has", len(reports))
	}
	r := reports[0]
	if r.Total != 3 {
		t.Errorf("Total = %d, want 3", r.Total)
	}
	if r.Rated != 2 {
		t.Errorf("Rated = %d, want 2 — the untouched problem is not rated", r.Rated)
	}
	if r.Unaided != 1 || r.UnaidedShare() != 50 {
		t.Errorf("Unaided = %d (%d%%), want 1 of 2", r.Unaided, r.UnaidedShare())
	}
	if r.Rung != 2.5 {
		t.Errorf("mean rung = %.2f, want 2.5 across rungs 0 and 5", r.Rung)
	}
	if r.Confidence != 3.5 {
		t.Errorf("mean confidence = %.2f, want 3.5 across 5 and 2", r.Confidence)
	}
	if r.Shaky != 1 {
		t.Errorf("Shaky = %d, want the one rated 2", r.Shaky)
	}
}

// A pattern going well must not be offered as something to go back to, or the
// list stops being worth reading.
func TestByConcernKeepsOnlyWhatNeedsWork(t *testing.T) {
	solid := PatternReport{Pattern: "solid", Total: 5, Rated: 5, Confidence: 4.6, Rung: 0}
	hinted := PatternReport{Pattern: "hinted", Total: 5, Rated: 5, Confidence: 3.4, Rung: 2.2, Slipped: 2}
	untouched := PatternReport{Pattern: "untouched", Total: 5}

	got := ByConcern([]PatternReport{solid, hinted, untouched})
	if len(got) != 1 || got[0].Pattern != "hinted" {
		names := []string{}
		for _, r := range got {
			names = append(names, r.Pattern)
		}
		t.Fatalf("ByConcern kept %v, want just the hinted one", names)
	}
	if untouched.Concern() != 0 {
		t.Errorf("an untouched pattern scored %.2f, want 0", untouched.Concern())
	}
	if solid.NeedsWork() {
		t.Error("a pattern averaging 4.6 unaided was flagged as needing work")
	}
}
