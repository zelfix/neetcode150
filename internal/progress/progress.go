// Package progress tracks what you have solved and when each problem is next
// due for review.
package progress

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"path/filepath"
	"sort"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
)

// Status is how far along a problem is.
type Status string

const (
	// New is untouched.
	New Status = "new"
	// Attempted means started but not solved.
	Attempted Status = "attempted"
	// Solved means solved with passing tests.
	Solved Status = "solved"
	// Shaky means solved once but not reliably — it keeps coming back sooner.
	Shaky Status = "shaky"
)

// Statuses lists every status in progression order.
var Statuses = []Status{New, Attempted, Solved, Shaky}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	for _, k := range Statuses {
		if k == s {
			return true
		}
	}
	return false
}

// Label is the status as shown in the UI.
func (s Status) Label() string {
	switch s {
	case Attempted:
		return "In progress"
	case Solved:
		return "Solved"
	case Shaky:
		return "Shaky"
	default:
		return "Not started"
	}
}

// reviewIntervals is the spaced-repetition ladder, in days per stage. Stage 0
// is "not scheduled"; a fresh solve enters at stage 1.
var reviewIntervals = [...]int{0, 1, 3, 7, 16, 35, 90}

// MaxStage is the last review stage; a problem there is considered retained.
const MaxStage = len(reviewIntervals) - 1

// The rung is the highest hint the sitting needed, counted up the ladder in
// tutor/TUTOR.md. Past a point a solve stops being recall: at rungHeld the
// invariant was supplied, so the problem keeps its stage instead of earning the
// next one, and at rungLapsed pseudocode or Go itself was handed over, which is
// reading rather than remembering, so the problem falls back down.
const (
	rungHeld   = 3
	rungLapsed = 4
)

// Ease is the per-problem interval multiplier. Two problems on the same stage
// are not equally secure — one may have been taken three times with hints — so
// the stage says how far along a problem is and the ease says how much to trust
// it there. The bounds keep the documented ladder recognisable: an interval can
// be shortened or stretched by two fifths, never inverted.
const (
	easeMin     = 0.6
	easeMax     = 1.4
	easeDefault = 1.0
)

// jitterPercent is how far a review may be nudged off its nominal date.
const jitterPercent = 15

// Review is one recorded sitting: the self-rating given, and the ladder stage
// it moved the problem to.
//
// The last rating on its own cannot tell retention from a lucky first solve.
// A problem that went 4 → 2 → 3 needs different treatment from one rated 3
// once, and only the sequence distinguishes them — so Rate appends rather than
// overwrites. MinutesTotal is the cumulative time at that moment; the gap
// between consecutive entries is how long that sitting took, which is the
// clearest retention signal there is.
type Review struct {
	Date         time.Time `json:"date"`
	Confidence   int       `json:"confidence"`
	Stage        int       `json:"stage"` // ladder stage after this rating
	MinutesTotal int       `json:"minutesTotal,omitempty"`
	Rung         int       `json:"rung,omitempty"` // highest hint rung needed, 0 = unaided
}

// Entry is the state of one problem.
type Entry struct {
	Status       Status     `json:"status"`
	Attempts     int        `json:"attempts,omitempty"`
	Confidence   int        `json:"confidence,omitempty"` // 1..5, last self-rating
	ReviewStage  int        `json:"reviewStage,omitempty"`
	Ease         float64    `json:"ease,omitempty"`
	FirstSolved  *time.Time `json:"firstSolved,omitempty"`
	LastTouched  *time.Time `json:"lastTouched,omitempty"`
	LastSolved   *time.Time `json:"lastSolved,omitempty"`
	NextReview   *time.Time `json:"nextReview,omitempty"`
	MinutesSpent int        `json:"minutesSpent,omitempty"`
	History      []Review   `json:"history,omitempty"`

	// Lang is the language this problem is being worked in, when it was
	// chosen for the problem itself; empty means the person's default. The
	// schedule above does not depend on it.
	Lang string `json:"lang,omitempty"`

	// AttemptStartedAt is the clock for the sitting in progress, so time spent
	// is measured rather than remembered. Rate consumes and clears it.
	AttemptStartedAt *time.Time `json:"attemptStartedAt,omitempty"`
}

// LastReview returns the most recent recorded rating.
func (e Entry) LastReview() (Review, bool) {
	if len(e.History) == 0 {
		return Review{}, false
	}
	return e.History[len(e.History)-1], true
}

// Slipped reports whether the latest rating came in below the one before it —
// the signal that the previous interval was too long.
func (e Entry) Slipped() bool {
	n := len(e.History)
	return n >= 2 && e.History[n-1].Confidence < e.History[n-2].Confidence
}

// ease is the entry's interval multiplier. The zero value — every entry written
// before ease existed — reads as neutral, so old progress needs no migration.
func (e Entry) ease() float64 {
	if e.Ease == 0 {
		return easeDefault
	}
	return min(easeMax, max(easeMin, e.Ease))
}

// Sitting is one rating in a problem's history, carrying the comparisons that
// only mean anything next to the rating before it.
//
// It lives here rather than in either caller because the CLI and the site both
// report this history, and two implementations of "did the rating slip" would
// eventually disagree about the same JSON.
type Sitting struct {
	Review
	// Minutes is this sitting alone, not the running total. Zero when the
	// clock was not running.
	Minutes int
	// IntervalDays is the interval the rating bought.
	IntervalDays int
	// Slipped marks a rating below the one before it: the previous interval
	// was too long.
	Slipped bool
	// Generous marks a high rating next to a high hint rung. The rung is the
	// honest measure where the rating is the felt one, so the pair is what
	// exposes an inflated self-assessment.
	Generous bool
}

// Sittings expands a history into one row per rating, newest last.
func Sittings(e Entry) []Sitting {
	out := make([]Sitting, 0, len(e.History))
	prevMinutes, prevRating := 0, 0
	for i, r := range e.History {
		s := Sitting{Review: r, IntervalDays: IntervalDays(r.Stage)}
		if d := r.MinutesTotal - prevMinutes; d > 0 {
			s.Minutes = d
		}
		s.Slipped = i > 0 && r.Confidence < prevRating
		s.Generous = r.Confidence >= 4 && r.Rung >= 3
		out = append(out, s)
		prevMinutes, prevRating = r.MinutesTotal, r.Confidence
	}
	return out
}

// Due reports whether the problem is due for review at or before now.
func (e Entry) Due(now time.Time) bool {
	return e.NextReview != nil && !e.NextReview.After(now)
}

// Store keeps scheduling rules in Go and persists each change in SQLite.
//
// A Store is one person's view. The scheduling half of this package is pure and
// never sees a user; everything that reads or writes goes through the scope, so
// the filter lives in one place rather than in every query.
type Store struct {
	scope *studydb.Scope
	// owned is the database this Store opened, and is nil for the per-request
	// stores the site builds. Only the opener may close it.
	owned *studydb.DB
}

// Load accepts the old progress path so existing callers can migrate in place.
// The JSON file is imported once and is never written again.
//
// It yields the owner's view: the CLI is the owner's own tool.
func Load(path string) (*Store, error) {
	db, err := studydb.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	return &Store{scope: db.Owner(), owned: db}, nil
}

// NewScoped wraps one user's view. It opens nothing, so the site can build one
// per request.
func NewScoped(scope *studydb.Scope) *Store { return &Store{scope: scope} }

// Scope is this store's user view, for callers that need the storage API.
func (s *Store) Scope() *studydb.Scope { return s.scope }

// DB reaches the shared handle for the few genuinely global statements.
func (s *Store) DB() *studydb.DB { return s.scope.Unscoped() }

// UserID is whose progress this store reads and writes.
func (s *Store) UserID() int64 { return s.scope.UserID() }

func (s *Store) Close() error {
	if s.owned == nil {
		return nil
	}
	return s.owned.Close()
}

func entryOf(entries map[string]Entry, slug string) Entry {
	e := entries[slug]
	if e.Status == "" {
		e.Status = New
	}
	return e
}
func (s *Store) snapshot() map[string]Entry {
	rows, err := s.scope.Unscoped().SQL.Query("SELECT slug,entry FROM progress WHERE user_id=?", s.scope.UserID())
	if err != nil {
		panic(fmt.Errorf("read study progress: %w", err))
	}
	defer rows.Close()
	entries := map[string]Entry{}
	for rows.Next() {
		var slug, raw string
		if err := rows.Scan(&slug, &raw); err != nil {
			panic(err)
		}
		var e Entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			panic(err)
		}
		entries[slug] = e
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	return entries
}
func (s *Store) Get(slug string) Entry { return entryOf(s.snapshot(), slug) }
func (s *Store) All() map[string]Entry { return s.snapshot() }

func (s *Store) UpdateIn(tx *sql.Tx, slug string, fn func(*Entry)) (Entry, error) {
	e := Entry{Status: New}
	var raw string
	err := tx.QueryRow("SELECT entry FROM progress WHERE user_id=? AND slug=?", s.scope.UserID(), slug).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return e, err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return e, err
		}
	}
	fn(&e)
	data, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	_, err = tx.Exec("INSERT INTO progress(user_id,slug,entry) VALUES(?,?,?) ON CONFLICT(user_id,slug) DO UPDATE SET entry=excluded.entry", s.scope.UserID(), slug, string(data))
	return e, err
}
func (s *Store) Update(slug string, fn func(*Entry)) (e Entry, err error) {
	err = s.scope.Write(func(tx *sql.Tx) error { e, err = s.UpdateIn(tx, slug, fn); return err })
	return
}

// SetStatus records a status change, keeping the review schedule consistent:
// a first solve enters the ladder, and marking something shaky pulls it back.
// SetLanguage records the language a problem is worked in; empty clears the
// choice back to the person's default.
func (s *Store) SetLanguage(slug, lang string) (Entry, error) {
	return s.Update(slug, func(e *Entry) { e.Lang = lang })
}

func (s *Store) SetStatus(slug string, status Status, now time.Time) (Entry, error) {
	return s.Update(slug, func(e *Entry) {
		prev := e.Status
		e.Status = status
		touched := now
		e.LastTouched = &touched

		switch status {
		case Attempted:
			if prev != Attempted {
				e.Attempts++
			}
		case Solved:
			if prev != Solved {
				e.Attempts++
			}
			solved := now
			e.LastSolved = &solved
			if e.FirstSolved == nil {
				first := now
				e.FirstSolved = &first
			}
			if e.ReviewStage < 1 {
				e.ReviewStage = 1
			}
			schedule(e, slug, now)
		case Shaky:
			lapse(e)
			schedule(e, slug, now)
		case New:
			e.ReviewStage = 0
			e.NextReview = nil
			e.Confidence = 0
			e.Ease = 0
			e.AttemptStartedAt = nil
		}
	})
}

// StartAttempt records a deliberate new sitting, even when the previous
// sitting ended as Attempted and the status itself therefore does not change.
func (s *Store) StartAttempt(slug string, now time.Time) (Entry, error) {
	return s.Update(slug, func(e *Entry) {
		e.Attempts++
		e.Status = Attempted
		touched := now
		e.LastTouched = &touched
	})
}

// lapseRungs is how far a problem falls when it slips but is not wholly lost.
//
// A full reset to stage 1 is too harsh for something you got with a hint: a
// problem you held for 35 days comes back tomorrow, and that is time spent on
// what you nearly knew. Dropping two rungs is the Anki "Hard" behaviour, as
// against "Again", which does reset — here, a rating of 1.
const lapseRungs = 2

// lapse moves a slipped problem back down the ladder without dropping it out.
// SetStatus(Shaky) and a rating of 2 are the same event reported two ways, so
// they must land on the same stage.
func lapse(e *Entry) {
	e.ReviewStage = max(1, e.ReviewStage-lapseRungs)
}

// BeginTimer stamps the start of a sitting, leaving a running clock alone so
// that re-running `nc start` does not discard time already spent.
func (s *Store) BeginTimer(slug string, now time.Time) (Entry, error) {
	return s.Update(slug, func(e *Entry) {
		if e.AttemptStartedAt == nil {
			t := now
			e.AttemptStartedAt = &t
		}
	})
}

// ResetTimer starts the clock over, for a re-solve.
func (s *Store) ResetTimer(slug string, now time.Time) (Entry, error) {
	return s.Update(slug, func(e *Entry) {
		t := now
		e.AttemptStartedAt = &t
	})
}

// StopTimer ends the current sitting without changing its status or rating.
func (s *Store) StopTimer(slug string, now time.Time) (Entry, error) {
	return s.Update(slug, func(e *Entry) {
		e.AttemptStartedAt = nil
		touched := now
		e.LastTouched = &touched
	})
}

// Elapsed is how long the sitting in progress has been running.
func (e Entry) Elapsed(now time.Time) (time.Duration, bool) {
	if e.AttemptStartedAt == nil {
		return 0, false
	}
	d := now.Sub(*e.AttemptStartedAt)
	if d < 0 {
		return 0, false
	}
	return d, true
}

// MaxSitting caps what one clock is allowed to contribute. A session left open
// overnight would otherwise log eleven hours and poison the median that Weakest
// reads. A browser tab is easier to leave open than a terminal, so the cap
// matters more on the site than it ever did in the CLI.
const MaxSitting = 6 * time.Hour

// ClockMinutes is what a sitting of this length is worth, and whether the cap
// threw it away. Anything under a minute is worth nothing, rather than rounding
// up to one and making a glance at a statement look like practice.
//
// Every caller that turns a clock into minutes goes through here, so the rule
// cannot drift between the CLI and the site.
func ClockMinutes(d time.Duration) (minutes int, capped bool) {
	if d > MaxSitting {
		return 0, true
	}
	if d < time.Minute {
		return 0, false
	}
	return int(d.Minutes()), false
}

// Rate records a confidence score from 1 (lost) to 5 (instant) and moves the
// problem along the review ladder accordingly, noting no hint rung.
func (s *Store) Rate(slug string, confidence int, now time.Time) (Entry, error) {
	return s.RateHinted(slug, confidence, 0, now)
}

// RateHinted is Rate, also recording the highest hint rung the problem needed:
// 0 for unaided, 1..5 up the ladder in tutor/TUTOR.md.
//
// The rung is the honest measure and the rating is the felt one. Recording both
// is what exposes an inflated rating — "rated 4, needed rung 3" is a problem
// that is not yet known, however comfortable it felt afterwards.
func (s *Store) RateHinted(slug string, confidence, rung int, now time.Time) (Entry, error) {
	if err := checkRating(confidence, rung); err != nil {
		return Entry{}, err
	}
	return s.Update(slug, func(e *Entry) { applyRating(e, confidence, rung, slug, now) })
}

func checkRating(confidence, rung int) error {
	if confidence < 1 || confidence > 5 {
		return fmt.Errorf("confidence must be 1..5, got %d", confidence)
	}
	if rung < 0 || rung > 5 {
		return fmt.Errorf("rung must be 0..5, got %d", rung)
	}
	return nil
}

func applyRating(e *Entry, confidence, rung int, slug string, now time.Time) {
	e.Confidence = confidence
	touched := now
	e.LastTouched = &touched
	earned := e.ReviewStage
	switch confidence {
	case 1:
		// Lost it entirely: back to the start of the ladder.
		e.Status = Shaky
		e.ReviewStage = 1
	case 2:
		// Got there, but on a strong hint. A partial fall, not a reset.
		e.Status = Shaky
		lapse(e)
	case 3:
		if e.ReviewStage < 1 {
			e.ReviewStage = 1
		}
		if e.Status == New || e.Status == Attempted {
			e.Status = Solved
		}
	default:
		e.ReviewStage = min(MaxStage, max(1, e.ReviewStage)+1)
		e.Status = Solved
	}
	// A stage won on a strong hint was not won. The gate only ever holds or
	// lowers, so ratings of 1 and 2 — which already fell — are left where the
	// confidence switch put them.
	if confidence >= 3 {
		switch {
		case rung >= rungLapsed:
			// Falls from the stage it held, not from the one this rating just
			// handed it — otherwise a 4 on rung 5 would land above a plain 2.
			e.ReviewStage = max(1, earned)
			lapse(e)
		case rung >= rungHeld:
			e.ReviewStage = max(1, min(e.ReviewStage, earned))
		}
	}
	// Before the append, so "did this rating slip" still looks at the one before.
	adjustEase(e, confidence, rung)
	if e.FirstSolved == nil && e.Status == Solved {
		first := now
		e.FirstSolved = &first
	}
	schedule(e, slug, now)
	// Appended after scheduling so Stage is the one this rating produced.
	e.History = append(e.History, Review{
		Date:         now,
		Confidence:   confidence,
		Stage:        e.ReviewStage,
		MinutesTotal: e.MinutesSpent,
		Rung:         rung,
	})
	e.AttemptStartedAt = nil // the sitting is over
}

// adjustEase moves a problem's interval multiplier for one rating. The rung
// carries most of the weight: it is the honest measure where the confidence is
// the felt one, so a comfortable 4 that needed the invariant spelled out still
// shortens the next interval.
func adjustEase(e *Entry, confidence, rung int) {
	ease := e.ease()
	switch {
	case confidence <= 2:
		ease -= 0.15
	case rung >= rungLapsed:
		ease -= 0.15
	case rung >= rungHeld:
		ease -= 0.10
	case rung > 0:
		ease -= 0.05
	case confidence >= 4:
		// Unaided and comfortable: this one has earned a longer leash.
		ease += 0.10
	}
	if prev, ok := e.LastReview(); ok && confidence < prev.Confidence {
		// Whatever the rung, a falling rating says the last interval was long.
		ease -= 0.05
	}
	e.Ease = min(easeMax, max(easeMin, ease))
}

// replay recomputes an entry from its recorded history. Every rating carries
// its date, confidence and rung, which is enough to derive the stage, the ease
// and the schedule from nothing — so dropping or correcting a rating is a
// rebuild rather than an attempt to invert the arithmetic. It also fills in the
// ease of entries written before ease existed.
func replay(e *Entry, slug string) {
	history := e.History
	clock := e.AttemptStartedAt
	e.History = nil
	e.ReviewStage = 0
	e.NextReview = nil
	e.Confidence = 0
	e.Ease = 0
	e.FirstSolved = nil
	if len(history) == 0 {
		if e.Status == Solved || e.Status == Shaky {
			e.Status = Attempted
		}
		e.AttemptStartedAt = clock
		return
	}
	e.Status = Attempted
	for _, r := range history {
		applyRating(e, r.Confidence, r.Rung, slug, r.Date)
		// applyRating stamps the live total; keep what the sitting recorded, or
		// the per-sitting minutes Sittings derives from the gaps are lost.
		e.History[len(e.History)-1].MinutesTotal = r.MinutesTotal
	}
	e.AttemptStartedAt = clock
}

// Undo drops the most recent rating and rebuilds the entry from what is left,
// for one entered by mistake. Minutes are not given back: the time was spent
// whichever rating was recorded.
func (s *Store) Undo(slug string, now time.Time) (Entry, Review, bool, error) {
	var dropped Review
	var found bool
	e, err := s.Update(slug, func(e *Entry) {
		n := len(e.History)
		if n == 0 {
			return
		}
		dropped, found = e.History[n-1], true
		e.History = e.History[:n-1]
		replay(e, slug)
		touched := now
		e.LastTouched = &touched
	})
	return e, dropped, found, err
}

// Amend replaces the most recent rating, for one entered wrongly. The sitting
// itself stands — only what it is recorded as changes.
func (s *Store) Amend(slug string, confidence, rung int, now time.Time) (Entry, bool, error) {
	if err := checkRating(confidence, rung); err != nil {
		return Entry{}, false, err
	}
	var found bool
	e, err := s.Update(slug, func(e *Entry) {
		n := len(e.History)
		if n == 0 {
			return
		}
		found = true
		e.History[n-1].Confidence = confidence
		e.History[n-1].Rung = rung
		replay(e, slug)
		touched := now
		e.LastTouched = &touched
	})
	return e, found, err
}

// CompleteIn commits the rating alongside its solution snapshot and tutor report.
func (s *Store) CompleteIn(tx *sql.Tx, slug string, confidence, rung, minutes int, passed bool, now time.Time) error {
	if confidence < 1 || confidence > 5 || rung < 0 || rung > 5 || minutes < 0 {
		return fmt.Errorf("invalid completed review")
	}
	_, err := s.UpdateIn(tx, slug, func(e *Entry) {
		e.MinutesSpent += minutes
		applyRating(e, confidence, rung, slug, now)
		if !passed {
			e.Status = Attempted
		}
	})
	return err
}

// AddMinutes adds time spent on a problem.
func (s *Store) AddMinutes(slug string, minutes int, now time.Time) (Entry, error) {
	if minutes < 0 {
		return Entry{}, fmt.Errorf("minutes must not be negative, got %d", minutes)
	}
	return s.Update(slug, func(e *Entry) {
		e.MinutesSpent += minutes
		touched := now
		e.LastTouched = &touched
	})
}

// schedule sets NextReview from the entry's current stage.
//
// Reviews fall due by the day, not by the hour: a problem rated at 21:00 has
// to be waiting when you sit down at 09:00 on its due date. Keeping the clock
// time would also let the hour drift later with every review.
func schedule(e *Entry, slug string, now time.Time) {
	stage := min(MaxStage, max(0, e.ReviewStage))
	e.ReviewStage = stage
	if stage == 0 {
		e.NextReview = nil
		return
	}
	next := startOfDay(now.AddDate(0, 0, interval(*e, slug)))
	e.NextReview = &next
}

// interval is the interval one entry actually gets: its ladder stage, stretched
// by the problem's ease, then nudged off the exact date.
func interval(e Entry, slug string) int {
	stage := min(MaxStage, max(0, e.ReviewStage))
	base := reviewIntervals[stage]
	if base == 0 {
		return 0
	}
	days := int(math.Round(float64(base) * e.ease()))
	return max(1, days+jitter(slug, stage, days))
}

// jitter spreads reviews that would otherwise fall on one day: work through a
// pattern in a single sitting and its 90-day reviews all come back together.
// The offset comes from the slug rather than from a random source so that it is
// stable — the same problem at the same stage always moves the same way, which
// keeps it testable and stops a date already shown from shifting on re-read.
// Short intervals round to no offset, which is deliberate: a 1-day review is
// due tomorrow and nothing is gained by moving it.
func jitter(slug string, stage, days int) int {
	spread := days * jitterPercent / 100
	if spread <= 0 {
		return 0
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "%s/%d", slug, stage)
	return int(h.Sum32()%uint32(2*spread+1)) - spread
}

// startOfDay is local midnight on t's calendar date.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// IntervalDays is the interval in force for this entry: the days from its last
// rating to its next review. That is the stretched, nudged figure rather than
// the ladder nominal, so it is what the UI should show. Measuring it off the
// dates rather than recomputing it keeps the displayed number the one the
// schedule is actually holding to.
func (e Entry) IntervalDays() int {
	if e.NextReview == nil {
		return 0
	}
	from := e.LastTouched
	if r, ok := e.LastReview(); ok {
		from = &r.Date
	}
	if from == nil {
		return 0
	}
	days := int(startOfDay(*e.NextReview).Sub(startOfDay(*from)).Hours() / 24)
	return max(0, days)
}

// IntervalDays is the nominal review interval, in days, for a stage — the
// ladder rung itself, before any one problem's ease is applied.
func IntervalDays(stage int) int {
	if stage < 0 || stage >= len(reviewIntervals) {
		return 0
	}
	return reviewIntervals[stage]
}

// Summary is an aggregate count of statuses.
type Summary struct {
	Total     int
	New       int
	Attempted int
	Solved    int
	Shaky     int
	DueNow    int
	Minutes   int
}

// Done counts problems that have been solved at least once, shaky included.
func (s Summary) Done() int { return s.Solved + s.Shaky }

// Percent is Done as a whole-number percentage of Total.
func (s Summary) Percent() int {
	if s.Total == 0 {
		return 0
	}
	return s.Done() * 100 / s.Total
}

// Summarize counts the statuses of the given problems.
func (s *Store) Summarize(problems []catalog.Problem, now time.Time) Summary {
	entries := s.snapshot()
	sum := Summary{Total: len(problems)}
	for _, p := range problems {
		e := entryOf(entries, p.Slug)
		switch e.Status {
		case Attempted:
			sum.Attempted++
		case Solved:
			sum.Solved++
		case Shaky:
			sum.Shaky++
		default:
			sum.New++
		}
		sum.Minutes += e.MinutesSpent
		if e.Due(now) {
			sum.DueNow++
		}
	}
	return sum
}

// DueReview returns the problems due for review, soonest due date first.
func (s *Store) DueReview(problems []catalog.Problem, now time.Time) []catalog.Problem {
	entries := s.snapshot()
	var due []catalog.Problem
	for _, p := range problems {
		if entryOf(entries, p.Slug).Due(now) {
			due = append(due, p)
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		a, b := entryOf(entries, due[i].Slug).NextReview, entryOf(entries, due[j].Slug).NextReview
		if a == nil || b == nil {
			return b == nil && a != nil
		}
		return a.Before(*b)
	})
	return due
}

// Weakness is one problem that cannot be trusted yet, and the reasons it was
// flagged.
//
// The scoring lives here rather than in either caller because the CLI and the
// site both rank weakness, and two implementations of "what counts as weak"
// would eventually disagree about the same history.
type Weakness struct {
	Problem catalog.Problem
	Score   int
	Reasons []string
}

// Weakest ranks problems by how little they can be trusted, weakest first,
// keeping at most limit of them. A limit of zero or less keeps all. Only
// problems that have been solved at least once can be weak; anything untouched
// is simply not done yet.
func (s *Store) Weakest(problems []catalog.Problem, limit int) []Weakness {
	entries := s.snapshot()

	// The median is taken across everything with time on it, not just the
	// problems being ranked, so that "well over the median" means the same
	// thing whichever subset is asked about.
	var minutes []int
	for slug := range entries {
		if e := entryOf(entries, slug); e.MinutesSpent > 0 {
			minutes = append(minutes, e.MinutesSpent)
		}
	}
	sort.Ints(minutes)
	median := 0
	if n := len(minutes); n > 0 {
		median = minutes[n/2]
	}

	var weak []Weakness
	for _, p := range problems {
		e := entryOf(entries, p.Slug)
		if e.Status != Solved && e.Status != Shaky {
			continue
		}
		if w := weigh(p, e, median); w.Score > 0 {
			weak = append(weak, w)
		}
	}
	sort.SliceStable(weak, func(i, j int) bool { return weak[i].Score > weak[j].Score })
	if limit > 0 && len(weak) > limit {
		weak = weak[:limit]
	}
	return weak
}

func weigh(p catalog.Problem, e Entry, median int) Weakness {
	w := Weakness{Problem: p}
	add := func(score int, reason string) {
		w.Score += score
		w.Reasons = append(w.Reasons, reason)
	}
	if e.Slipped() {
		add(3, "rating fell since last time")
	}
	if e.Status == Shaky {
		add(2, "flagged shaky")
	}
	switch e.Confidence {
	case 1, 2:
		add(3, fmt.Sprintf("last rated %d", e.Confidence))
	case 3:
		add(1, "last rated 3")
	}
	if r, ok := e.LastReview(); ok && r.Rung >= 4 {
		add(3, fmt.Sprintf("needed hint rung %d", r.Rung))
	} else if ok && r.Rung >= 2 {
		add(1, fmt.Sprintf("needed hint rung %d", r.Rung))
	}
	if e.Attempts >= 2 {
		add(1, plural(e.Attempts, "attempt", "attempts"))
	}
	if median > 0 && e.MinutesSpent >= 2*median {
		add(1, fmt.Sprintf("%s, well over the %d-minute median",
			plural(e.MinutesSpent, "minute", "minutes"), median))
	}
	return w
}

// ReviewQueue is the due list in the order worth working through: longest
// overdue first, then weakest.
//
// Due reviews arrive in heaps — a pattern worked through in one sitting comes
// back in one day — and a queue in catalogue order spends the best attention of
// the session on whichever problem happens to sort first. A limit of zero or
// less keeps all of them.
func (s *Store) ReviewQueue(problems []catalog.Problem, now time.Time, limit int) []catalog.Problem {
	due := s.DueReview(problems, now)
	if len(due) <= 1 {
		return due
	}
	entries := s.snapshot()
	score := map[string]int{}
	for _, w := range s.Weakest(due, 0) {
		score[w.Problem.Slug] = w.Score
	}
	// DueReview already sorts by due date, so a stable sort on the same key
	// only adds the weakness tie-break.
	sort.SliceStable(due, func(i, j int) bool {
		a := overdueDays(entryOf(entries, due[i].Slug), now)
		b := overdueDays(entryOf(entries, due[j].Slug), now)
		if a != b {
			return a > b
		}
		return score[due[i].Slug] > score[due[j].Slug]
	})
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	return due
}

// overdueDays is how many days past its due date a problem is; 0 when it falls
// due today.
func overdueDays(e Entry, now time.Time) int {
	if e.NextReview == nil {
		return 0
	}
	days := int(startOfDay(now).Sub(startOfDay(*e.NextReview)).Hours() / 24)
	return max(0, days)
}

// plural renders a count with the right noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Next suggests what to work on: anything due for review first, then the
// earliest unfinished problem in curriculum order.
func (s *Store) Next(problems []catalog.Problem, now time.Time) (catalog.Problem, string, bool) {
	if due := s.DueReview(problems, now); len(due) > 0 {
		return due[0], "due for review", true
	}
	entries := s.snapshot()
	ordered := make([]catalog.Problem, len(problems))
	copy(ordered, problems)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Order < ordered[j].Order })
	for _, p := range ordered {
		if entryOf(entries, p.Slug).Status == Attempted {
			return p, "already in progress", true
		}
	}
	for _, p := range ordered {
		if entryOf(entries, p.Slug).Status == New {
			return p, "next in the roadmap", true
		}
	}
	return catalog.Problem{}, "", false
}

// PatternReport is one pattern's standing: how much of it is done, and how
// securely.
//
// Counting solved problems flatters a pattern that was only ever solved with
// help, which is exactly the pattern worth going back to. So the hint rungs,
// the slips and the ratings are carried alongside the count.
type PatternReport struct {
	Pattern     string
	PatternSlug string
	Total       int
	Solved      int // solved or shaky
	Shaky       int
	Due         int
	Minutes     int

	// Rated is how many of the pattern's problems have at least one recorded
	// rating. It is the denominator for everything below, and it is not the
	// same as Solved: a problem can be rated without being counted solved.
	Rated      int
	Confidence float64 // mean of the last rating of each rated problem
	Rung       float64 // mean of the last hint rung
	Unaided    int     // last sitting needed no hint at all
	Slipped    int     // last rating came in below the one before it
}

// Percent is solved problems as a whole-number percentage of the pattern.
func (r PatternReport) Percent() int {
	if r.Total == 0 {
		return 0
	}
	return r.Solved * 100 / r.Total
}

// UnaidedShare is the percentage of rated problems whose last sitting needed
// no hint. This is the number that answers "do I actually know this pattern".
func (r PatternReport) UnaidedShare() int {
	if r.Rated == 0 {
		return 0
	}
	return r.Unaided * 100 / r.Rated
}

// SlipShare is the percentage of rated problems whose last rating fell.
func (r PatternReport) SlipShare() int {
	if r.Rated == 0 {
		return 0
	}
	return r.Slipped * 100 / r.Rated
}

// Concern ranks patterns for attention: needing hints, slipping and rating low
// all push a pattern up. A pattern with nothing rated scores zero — it is not
// a weakness, it is simply not started, and mixing the two would send the user
// back over old ground instead of forward.
func (r PatternReport) Concern() float64 {
	if r.Rated == 0 {
		return 0
	}
	return r.Rung + (5 - r.Confidence) + 2*float64(r.Slipped)/float64(r.Rated)
}

// Patterns reports each pattern's standing, in curriculum order.
func (s *Store) Patterns(problems []catalog.Problem, now time.Time) []PatternReport {
	entries := s.snapshot()
	order := []string{}
	bySlug := map[string]*PatternReport{}
	sums := map[string]struct{ confidence, rung float64 }{}

	for _, p := range problems {
		r, ok := bySlug[p.PatternSlug]
		if !ok {
			r = &PatternReport{Pattern: p.Pattern, PatternSlug: p.PatternSlug}
			bySlug[p.PatternSlug] = r
			order = append(order, p.PatternSlug)
		}
		e := entryOf(entries, p.Slug)
		r.Total++
		r.Minutes += e.MinutesSpent
		if e.Status == Solved || e.Status == Shaky {
			r.Solved++
		}
		if e.Status == Shaky {
			r.Shaky++
		}
		if e.Due(now) {
			r.Due++
		}
		last, rated := e.LastReview()
		if !rated {
			continue
		}
		r.Rated++
		if last.Rung == 0 {
			r.Unaided++
		}
		if e.Slipped() {
			r.Slipped++
		}
		acc := sums[p.PatternSlug]
		acc.confidence += float64(last.Confidence)
		acc.rung += float64(last.Rung)
		sums[p.PatternSlug] = acc
	}

	out := make([]PatternReport, 0, len(order))
	for _, slug := range order {
		r := bySlug[slug]
		if r.Rated > 0 {
			acc := sums[slug]
			r.Confidence = acc.confidence / float64(r.Rated)
			r.Rung = acc.rung / float64(r.Rated)
		}
		out = append(out, *r)
	}
	return out
}

// concernFloor is the score below which a pattern is not worth raising.
//
// A pattern averaging a rating of 4, unaided, with no slips scores 1.0 — that
// is a pattern going well, and listing it as something to go back to is how a
// coach stops being believed. The floor sits above that: something has to be
// actually off, whether a hint, a slip, or a run of middling ratings.
const concernFloor = 1.5

// NeedsWork reports whether a pattern is worth returning to.
func (r PatternReport) NeedsWork() bool { return r.Concern() >= concernFloor }

// ByConcern orders reports for attention, most concerning first. Patterns with
// nothing rated are dropped — there is nothing to say about them — and so are
// the ones going well.
func ByConcern(reports []PatternReport) []PatternReport {
	out := make([]PatternReport, 0, len(reports))
	for _, r := range reports {
		if r.NeedsWork() {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Concern() > out[j].Concern() })
	return out
}
