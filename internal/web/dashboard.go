package web

import (
	"net/http"
	"sort"
	"time"

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

// dashboardPage is the home page: what to work on next, and what the ratings
// say about what has been worked on already.
//
// The second half was a separate Coach page. Both read the same ratings, and
// the split meant a count of solved problems sat on one page while the figures
// that qualify it sat on another.
type dashboardPage struct {
	common

	Next       *problemView
	NextAction string
	NextReason string
	DueSoon    []problemView
	Recent     []problemView

	// Rated is how many problems have a recorded rating, Unaided how many of
	// those last went unaided.
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
func (p dashboardPage) UnaidedShare() int {
	if p.Rated == 0 {
		return 0
	}
	return p.Unaided * 100 / p.Rated
}

func (s *session) handleDashboard(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	patterns := s.progress.Patterns(s.cat.Problems, now)
	page := dashboardPage{
		common:   s.common("Dashboard", "dashboard"),
		Patterns: patterns,
		Concerns: progress.ByConcern(patterns),
		Weakest:  s.progress.Weakest(s.cat.Problems, 10),
	}
	if p, reason, ok := s.progress.Next(s.cat.Problems, now); ok {
		v := s.view(p, now)
		_, hasSolution := s.readSolution(p)
		page.Next, page.NextReason = &v, reason
		page.NextAction = workAction(v, hasSolution)
	}
	due := s.views(s.progress.DueReview(s.cat.Problems, now), now)
	page.DueSoon = due[:min(len(due), 5)]
	page.Recent = s.recent(now, 6)

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
	s.render(w, "dashboard", page)
}

// recent lists the problems you touched most recently.
func (s *session) recent(now time.Time, limit int) []problemView {
	var touched []problemView
	for _, p := range s.cat.Problems {
		v := s.view(p, now)
		if v.Entry.LastTouched != nil {
			touched = append(touched, v)
		}
	}
	sort.SliceStable(touched, func(i, j int) bool {
		return touched[i].Entry.LastTouched.After(*touched[j].Entry.LastTouched)
	})
	return touched[:min(len(touched), limit)]
}
