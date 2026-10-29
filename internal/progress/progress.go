// Package progress tracks what you have solved and when each problem is next
// due for review.
package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"neetcode150/internal/catalog"
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

// Entry is the state of one problem.
type Entry struct {
	Status       Status     `json:"status"`
	Attempts     int        `json:"attempts,omitempty"`
	Confidence   int        `json:"confidence,omitempty"` // 1..5, last self-rating
	ReviewStage  int        `json:"reviewStage,omitempty"`
	FirstSolved  *time.Time `json:"firstSolved,omitempty"`
	LastTouched  *time.Time `json:"lastTouched,omitempty"`
	LastSolved   *time.Time `json:"lastSolved,omitempty"`
	NextReview   *time.Time `json:"nextReview,omitempty"`
	MinutesSpent int        `json:"minutesSpent,omitempty"`
}

// Due reports whether the problem is due for review at or before now.
func (e Entry) Due(now time.Time) bool {
	return e.NextReview != nil && !e.NextReview.After(now)
}

// Store is the on-disk progress file, safe for concurrent use.
//
// The CLI and the site are separate processes writing the same file, so reads
// re-check the file's modification time and reload when it has changed on disk.
// Without that, a long-running `nc serve` would never see a `nc rate` made in
// the terminal.
type Store struct {
	path    string
	mu      sync.RWMutex
	entries map[string]Entry
	modTime time.Time
	size    int64
}

// Load opens the progress file at path, treating a missing file as empty.
func Load(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
	if info, err := os.Stat(path); err == nil {
		s.modTime, s.size = info.ModTime(), info.Size()
	}
	return s, nil
}

// syncFromDisk reloads the file if another process has written it. It must be
// called with no lock held. A file that cannot be read is left alone: whatever
// is in memory is better than dropping the user's progress.
func (s *Store) syncFromDisk() {
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	s.mu.RLock()
	fresh := info.ModTime().Equal(s.modTime) && info.Size() == s.size
	s.mu.RUnlock()
	if fresh {
		return
	}

	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	entries := map[string]Entry{}
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &entries); err != nil {
			return // a half-written or corrupt file must not wipe memory
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under the write lock: another goroutine may have just reloaded.
	if info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return
	}
	s.entries = entries
	s.modTime, s.size = info.ModTime(), info.Size()
}

// snapshot reloads if needed and returns a copy, so an aggregate walk over many
// problems stats the file once instead of once per problem.
func (s *Store) snapshot() map[string]Entry {
	s.syncFromDisk()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Entry, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}

// entryOf reads one entry out of a snapshot, defaulting to New.
func entryOf(entries map[string]Entry, slug string) Entry {
	e, ok := entries[slug]
	if !ok {
		return Entry{Status: New}
	}
	if e.Status == "" {
		e.Status = New
	}
	return e
}

// Get returns the entry for a slug; an untouched problem reads as New.
func (s *Store) Get(slug string) Entry {
	s.syncFromDisk()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entryOf(s.entries, slug)
}

// All returns a copy of every stored entry.
func (s *Store) All() map[string]Entry { return s.snapshot() }

// save persists the file. The caller must hold the write lock.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := catalog.WriteFileAtomic(s.path, append(b, '\n')); err != nil {
		return err
	}
	if info, err := os.Stat(s.path); err == nil {
		s.modTime, s.size = info.ModTime(), info.Size()
	}
	return nil
}

// Update applies fn to a problem's entry and persists the result.
func (s *Store) Update(slug string, fn func(*Entry)) (Entry, error) {
	s.syncFromDisk()
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[slug]
	if !ok {
		e = Entry{Status: New}
	}
	fn(&e)
	s.entries[slug] = e
	if err := s.save(); err != nil {
		return e, err
	}
	return e, nil
}

// SetStatus records a status change, keeping the review schedule consistent:
// a first solve enters the ladder, and marking something shaky pulls it back.
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
			schedule(e, now)
		case Shaky:
			// Drop a rung, but never out of the ladder entirely.
			e.ReviewStage = max(1, e.ReviewStage-1)
			schedule(e, now)
		case New:
			e.ReviewStage = 0
			e.NextReview = nil
			e.Confidence = 0
		}
	})
}

// Rate records a confidence score from 1 (lost) to 5 (instant) and moves the
// problem along the review ladder accordingly.
func (s *Store) Rate(slug string, confidence int, now time.Time) (Entry, error) {
	if confidence < 1 || confidence > 5 {
		return Entry{}, fmt.Errorf("confidence must be 1..5, got %d", confidence)
	}
	return s.Update(slug, func(e *Entry) {
		e.Confidence = confidence
		touched := now
		e.LastTouched = &touched
		switch {
		case confidence <= 2:
			// Needed real help: repeat sooner and flag it.
			e.Status = Shaky
			e.ReviewStage = 1
		case confidence == 3:
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
		if e.FirstSolved == nil && e.Status == Solved {
			first := now
			e.FirstSolved = &first
		}
		schedule(e, now)
	})
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
func schedule(e *Entry, now time.Time) {
	stage := min(MaxStage, max(0, e.ReviewStage))
	e.ReviewStage = stage
	if stage == 0 {
		e.NextReview = nil
		return
	}
	next := startOfDay(now.AddDate(0, 0, reviewIntervals[stage]))
	e.NextReview = &next
}

// startOfDay is local midnight on t's calendar date.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// IntervalDays is the review interval, in days, for a stage.
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
