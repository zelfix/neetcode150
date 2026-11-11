package web

import (
	"net/http"

	"neetcode150/internal/catalog"
)

// drillPage draws one problem at random and withholds its pattern.
//
// Working the roadmap in order means the pattern is known before the statement
// is read, which is most of the work. Interleaved practice feels worse during a
// session and holds up better weeks later, so it needs its own entry point.
type drillPage struct {
	common

	Drawn      *catalog.Problem
	Candidates int
	Groups     []catalog.PatternGroup
	Pattern    string // the pattern slug asked for, to keep the select on it
	Solved     bool
	Empty      string // why nothing was drawn
}

func (s *session) handleDrill(w http.ResponseWriter, r *http.Request) {
	pattern := r.URL.Query().Get("pattern")
	solved := r.URL.Query().Get("solved") != ""
	page := drillPage{
		common:  s.common("Drill", "drill"),
		Groups:  s.cat.Groups(),
		Pattern: pattern,
		Solved:  solved,
	}

	pool := catalog.MatchPattern(s.cat.Problems, pattern)
	candidates := s.progress.DrillPool(pool, solved)
	page.Candidates = len(candidates)
	switch {
	case len(pool) == 0:
		page.Empty = "No pattern matches that."
	case len(candidates) == 0 && solved:
		page.Empty = "Nothing solved yet to drill."
	case len(candidates) == 0:
		page.Empty = "Nothing unsolved left in that pool — try the solved ones for retrieval practice."
	default:
		drawn := candidates[s.pick(len(candidates))]
		page.Drawn = &drawn
	}
	s.render(w, "drill", page)
}
