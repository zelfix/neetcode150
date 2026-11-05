package studydb

import (
	"database/sql"
	"time"
)

// FailedCase is one practice fixture a run reported as failing. Kind is the
// fixture's own tag — "example" for a worked example out of the statement,
// "edge" for a corner case — which is what makes the two countable apart.
type FailedCase struct {
	Kind string
	Name string
}

// CaseFailure is how often one fixture has failed across every recorded run.
type CaseFailure struct {
	Slug     string
	Kind     string
	Name     string
	Failures int
	LastAt   time.Time
}

// TestTally is how many runs a solution has had, and how many were clean.
type TestTally struct {
	Runs  int
	Clean int
}

// Clean reports the share of runs that passed, as a whole-number percentage.
func (t TestTally) Share() int {
	if t.Runs == 0 {
		return 0
	}
	return t.Clean * 100 / t.Runs
}

// RecordTestRun stores the outcome of one `go test` and increments the counter
// for each fixture it broke. The run and its failures land together so a
// failure can never be counted against a run that was not recorded.
func (s *Scope) RecordTestRun(slug string, now time.Time, passed, failed int, cases []FailedCase) error {
	stamp := now.Format(time.RFC3339Nano)
	return s.db.Write(func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO test_runs(user_id,slug,ran_at,passed,failed) VALUES(?,?,?,?,?)",
			s.userID, slug, stamp, passed, failed); err != nil {
			return err
		}
		for _, c := range cases {
			if _, err := tx.Exec(`INSERT INTO case_failures(user_id,slug,kind,name,failures,last_at) VALUES(?,?,?,?,1,?)
ON CONFLICT(user_id,slug,kind,name) DO UPDATE SET failures=failures+1, last_at=excluded.last_at`,
				s.userID, slug, c.Kind, c.Name, stamp); err != nil {
				return err
			}
		}
		return nil
	})
}

// Tally counts the runs recorded for one of this user's problems, or for every
// problem of theirs when slug is empty.
func (s *Scope) Tally(slug string) (TestTally, error) {
	var t TestTally
	query := "SELECT count(*), coalesce(sum(failed=0),0) FROM test_runs WHERE user_id=?"
	args := []any{s.userID}
	if slug != "" {
		query += " AND slug=?"
		args = append(args, slug)
	}
	err := s.db.SQL.QueryRow(query, args...).Scan(&t.Runs, &t.Clean)
	return t, err
}

// CaseFailures lists the fixtures that have failed most often, for one problem
// or across all of them when slug is empty. A limit of zero or less keeps all.
func (s *Scope) CaseFailures(slug string, limit int) ([]CaseFailure, error) {
	query := "SELECT slug,kind,name,failures,last_at FROM case_failures WHERE user_id=?"
	args := []any{s.userID}
	if slug != "" {
		query += " AND slug=?"
		args = append(args, slug)
	}
	query += " ORDER BY failures DESC, last_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.SQL.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CaseFailure
	for rows.Next() {
		var c CaseFailure
		var last string
		if err := rows.Scan(&c.Slug, &c.Kind, &c.Name, &c.Failures, &last); err != nil {
			return nil, err
		}
		if c.LastAt, err = time.Parse(time.RFC3339Nano, last); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FailuresByKind totals failures for examples against corner cases. Breaking a
// worked example means the approach is wrong; breaking only corner cases means
// it is nearly right and under-tested, which calls for different advice.
func (s *Scope) FailuresByKind() (map[string]int, error) {
	rows, err := s.db.SQL.Query("SELECT kind, sum(failures) FROM case_failures WHERE user_id=? GROUP BY kind", s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	return out, rows.Err()
}
