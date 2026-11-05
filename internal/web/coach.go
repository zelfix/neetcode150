package web

import (
	"net/http"
	"sort"

	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
)

// caseFailureView pairs a broken fixture with the problem it belongs to, so
// the page can link to the problem rather than print a slug.
type caseFailureView struct {
	studydb.CaseFailure
	Title string
	ID    int
}

type coachPage struct {
	common

	// Rated is how many problems have a recorded rating, and Unaided how many
	// of those last went unaided. The pair is the headline: not how much has
	// been covered, but how much can be done without help.
	Rated   int
	Unaided int

	Concerns []progress.PatternReport
	Patterns []progress.PatternReport
	Weakest  []progress.Weakness

	Tally    studydb.TestTally
	Cases    []caseFailureView
	Examples int
	Edges    int
}

// UnaidedShare is the percentage of rated problems last solved without a hint.
func (p coachPage) UnaidedShare() int {
	if p.Rated == 0 {
		return 0
	}
	return p.Unaided * 100 / p.Rated
}

// handleCoach reports what the ratings add up to.
//
// The dashboard answers "how far along am I", which a count of solved problems
// can flatter. This page answers the harder question — what is not secure —
// off the data the ratings already carry: hint rungs, slips, and which test
// fixtures keep breaking.
func (s *session) handleCoach(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	patterns := s.progress.Patterns(s.cat.Problems, now)
	page := coachPage{
		common:   s.common("Coach", "coach"),
		Patterns: patterns,
		Concerns: progress.ByConcern(patterns),
		Weakest:  s.progress.Weakest(s.cat.Problems, 10),
	}
	for _, rep := range patterns {
		page.Rated += rep.Rated
		page.Unaided += rep.Unaided
	}
	page.Concerns = page.Concerns[:min(len(page.Concerns), 6)]

	// The test record is a nicety, not the point of the page: if it cannot be
	// read, the rest still renders.
	if tally, err := s.progress.Scope().Tally(""); err == nil {
		page.Tally = tally
	}
	if kinds, err := s.progress.Scope().FailuresByKind(); err == nil {
		page.Examples, page.Edges = kinds["example"], kinds["edge"]
	}
	if cases, err := s.progress.Scope().CaseFailures("", 12); err == nil {
		for _, c := range cases {
			v := caseFailureView{CaseFailure: c, Title: c.Slug}
			if p, ok := s.cat.BySlug(c.Slug); ok {
				v.Title, v.ID = p.Title, p.ID
			}
			page.Cases = append(page.Cases, v)
		}
	}
	sort.SliceStable(page.Cases, func(i, j int) bool {
		return page.Cases[i].Failures > page.Cases[j].Failures
	})
	s.render(w, "coach", page)
}
