package web

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
)

// personRow is one line of the people page.
//
// Note what is NOT here, and why it is not an oversight: no rating, no hint
// rung, no share of problems solved unaided. Those are the numbers the review
// schedule reads, and they only work while they are honest. Ranking people by
// them would put a price on admitting a hint was needed — the same failure this
// project names for a tutor that leaks the answer, where what breaks is not
// politeness but the signal. What is ranked here is effort: to climb it you
// have to practise more, so the incentive and the point agree.
type personRow struct {
	Name    string
	Solved  int
	Minutes int
	Days    int // distinct days with work recorded on them
	You     bool
}

type peoplePage struct {
	common

	Rows []personRow
	// Name is this person's own, for the form that changes it.
	Name string
	Err  string
	OK   bool
}

// daysActive counts the separate days somebody has touched a problem. It is a
// plainer measure of keeping at it than a streak, which punishes a holiday.
func daysActive(entries map[string]progress.Entry) int {
	days := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.LastTouched != nil {
			days[e.LastTouched.Format(time.DateOnly)] = struct{}{}
		}
	}
	return len(days)
}

// people builds the table. One scoped read per person, reusing Summarize
// rather than a second definition of what "solved" means: ten people is ten
// cheap queries against a shared connection, and a bespoke aggregate here
// would be a place for the two answers to drift.
func (s *session) people() ([]personRow, error) {
	users, err := s.db.Users()
	if err != nil {
		return nil, err
	}
	now := s.now()
	rows := make([]personRow, 0, len(users))
	for _, u := range users {
		if u.Disabled {
			continue
		}
		store := progress.NewScoped(s.db.For(u.ID))
		sum := store.Summarize(s.cat.Problems, now)
		rows = append(rows, personRow{
			Name:    u.Name,
			Solved:  sum.Done(),
			Minutes: sum.Minutes,
			Days:    daysActive(store.All()),
			You:     u.ID == s.user.ID,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Solved != rows[j].Solved {
			return rows[i].Solved > rows[j].Solved
		}
		if rows[i].Minutes != rows[j].Minutes {
			return rows[i].Minutes > rows[j].Minutes
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows, nil
}

func (s *session) handlePeople(w http.ResponseWriter, r *http.Request) {
	s.renderPeople(w, "", false)
}

func (s *session) renderPeople(w http.ResponseWriter, msg string, ok bool) {
	rows, err := s.people()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "people", peoplePage{
		common: s.common("People", "people"),
		Rows:   rows,
		Name:   s.user.Name,
		Err:    msg,
		OK:     ok,
	})
}

// handleSetName changes what this person is called.
//
// A name is a label: progress is keyed by id, so renaming moves no work. It is
// behind guard like the other form posts, and it can only ever change the row
// of whoever is signed in — there is no id in the request to tamper with.
func (s *session) handleSetName(w http.ResponseWriter, r *http.Request) {
	if !limitForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	switch {
	case name == "":
		s.renderPeople(w, "A name cannot be empty.", false)
		return
	case len([]rune(name)) > maxNameLen:
		s.renderPeople(w, "That name is too long; 32 characters at most.", false)
		return
	case strings.ContainsAny(name, "\n\r\t"):
		s.renderPeople(w, "A name is one line.", false)
		return
	}

	err := s.db.RenameUser(s.user.ID, name)
	switch {
	case errors.Is(err, studydb.ErrNameTaken):
		s.renderPeople(w, "Somebody already goes by that. Try another.", false)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.user.Name = name
	s.renderPeople(w, "", true)
}
