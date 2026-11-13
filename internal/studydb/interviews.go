package studydb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Interview is one interview sitting. The state machine itself lives in the
// payload, which the web package owns: what is in the columns is only what the
// database has to search or constrain on.
type Interview struct {
	ID       int64
	Started  time.Time
	Finished *time.Time
	Payload  json.RawMessage
}

// Live reports whether this interview is the one still in progress.
func (i Interview) Live() bool { return i.Finished == nil }

// CreateInterview starts one. A second live interview is refused by the partial
// unique index rather than by a check here, so two tabs cannot both open one.
func (s *Scope) CreateInterview(started time.Time, payload []byte) (int64, error) {
	if !json.Valid(payload) {
		return 0, fmt.Errorf("interview payload is not JSON")
	}
	res, err := s.db.SQL.Exec("INSERT INTO interviews(user_id,started_at,payload) VALUES(?,?,?)",
		s.userID, started.Format(time.RFC3339Nano), string(payload))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LiveInterview is the sitting in progress, or nil.
func (s *Scope) LiveInterview() (*Interview, error) {
	return s.interviewWhere("finished_at IS NULL ORDER BY id DESC LIMIT 1")
}

// InterviewByID is one of this person's interviews, finished or not.
func (s *Scope) InterviewByID(id int64) (*Interview, error) {
	return s.interviewWhere("id=?", id)
}

func (s *Scope) interviewWhere(cond string, args ...any) (*Interview, error) {
	var rec Interview
	var started string
	var finished sql.NullString
	var payload string
	row := s.db.SQL.QueryRow(
		"SELECT id,started_at,finished_at,payload FROM interviews WHERE user_id=? AND "+cond,
		append([]any{s.userID}, args...)...)
	switch err := row.Scan(&rec.ID, &started, &finished, &payload); {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, err
	}
	var err error
	if rec.Started, err = time.Parse(time.RFC3339Nano, started); err != nil {
		return nil, err
	}
	if finished.Valid {
		t, err := time.Parse(time.RFC3339Nano, finished.String)
		if err != nil {
			return nil, err
		}
		rec.Finished = &t
	}
	rec.Payload = json.RawMessage(payload)
	return &rec, nil
}

// SaveInterview replaces the payload of one interview, leaving its dates alone.
func (s *Scope) SaveInterview(id int64, payload []byte) error {
	if !json.Valid(payload) {
		return fmt.Errorf("interview payload is not JSON")
	}
	res, err := s.db.SQL.Exec("UPDATE interviews SET payload=? WHERE user_id=? AND id=?",
		string(payload), s.userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no interview %d", id)
	}
	return nil
}

// FinishInterview stamps it done and saves the final payload in one statement,
// so an interview can never be finished without the report it finished with.
func (s *Scope) FinishInterview(id int64, finished time.Time, payload []byte) error {
	if !json.Valid(payload) {
		return fmt.Errorf("interview payload is not JSON")
	}
	res, err := s.db.SQL.Exec(
		"UPDATE interviews SET finished_at=?, payload=? WHERE user_id=? AND id=? AND finished_at IS NULL",
		finished.Format(time.RFC3339Nano), string(payload), s.userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no live interview %d", id)
	}
	return nil
}

// Interviews lists this person's interviews, most recent first.
func (s *Scope) Interviews(limit int) ([]Interview, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.SQL.Query(
		"SELECT id,started_at,finished_at,payload FROM interviews WHERE user_id=? ORDER BY id DESC LIMIT ?",
		s.userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Interview
	for rows.Next() {
		var rec Interview
		var started string
		var finished sql.NullString
		var payload string
		if err := rows.Scan(&rec.ID, &started, &finished, &payload); err != nil {
			return nil, err
		}
		if rec.Started, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return nil, err
		}
		if finished.Valid {
			t, err := time.Parse(time.RFC3339Nano, finished.String)
			if err != nil {
				return nil, err
			}
			rec.Finished = &t
		}
		rec.Payload = json.RawMessage(payload)
		out = append(out, rec)
	}
	return out, rows.Err()
}
