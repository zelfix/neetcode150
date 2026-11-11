package progress

import (
	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/studydb"
)

// Archived is which earlier attempt a comparison should use, and what was set
// aside to get there.
type Archived struct {
	Attempt  studydb.Attempt
	Found    bool
	Matching int // archived attempts in the asked-for language
	Skipped  int // archived in another language
}

// LastArchivedIn picks the newest attempt written in l, counting both what
// matched and what was passed over.
//
// A Go attempt against a Python solution is noise, not a diff, so the language
// filter lives here rather than in either caller: the CLI and the site both ask
// "what do I compare against", and two answers to that would eventually
// disagree. An attempt archived before the language was recorded reads as Go,
// which is what it was.
func LastArchivedIn(attempts []studydb.Attempt, l lang.ID) Archived {
	var out Archived
	for _, a := range attempts {
		if lang.Of(lang.ID(a.Language)).ID != l {
			out.Skipped++
			continue
		}
		out.Matching++
		out.Attempt, out.Found = a, true
	}
	return out
}

// LastArchived is LastArchivedIn over everything archived for slug, for callers
// that do not already hold the rows. Attempts comes back oldest first, so the
// last match wins.
func (s *Store) LastArchived(slug string, l lang.ID) (Archived, error) {
	attempts, err := s.scope.Attempts(slug)
	if err != nil {
		return Archived{}, err
	}
	return LastArchivedIn(attempts, l), nil
}

// DrillPool is what an interleaved drill may draw from: the problems not yet
// solved, or the solved ones for retrieval practice.
//
// Started but unfinished counts as unsolved. Leaving those out made a pool of
// twenty open problems report "nothing left".
func (s *Store) DrillPool(problems []catalog.Problem, solved bool) []catalog.Problem {
	var out []catalog.Problem
	for _, p := range problems {
		st := s.Get(p.Slug).Status
		if solved == (st == Solved || st == Shaky) {
			out = append(out, p)
		}
	}
	return out
}
