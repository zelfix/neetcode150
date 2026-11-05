// Package studydb owns the local study data. Working Go files and curriculum
// content stay on disk; mutable study records have one transactional home.
package studydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const Filename = "study.sqlite3"

const schemaVersion = 3

type DB struct {
	SQL  *sql.DB
	Root string
}

func Open(root string) (*DB, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(abs, Filename)
	// A private file is created before SQLite opens it; personal notes and code
	// should not inherit SQLite's more permissive default file mode.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	u := url.URL{Scheme: "file", Path: path}
	conn, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	db := &DB{SQL: conn, Root: abs}
	fail := func(err error) (*DB, error) { conn.Close(); return nil, err }
	if _, err = conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fail(err)
	}
	if err = db.schema(); err != nil {
		return fail(err)
	}
	if err = db.importLegacy(); err != nil {
		return fail(fmt.Errorf("import legacy study data: %w", err))
	}
	return db, nil
}
func (d *DB) Close() error { return d.SQL.Close() }
func (d *DB) Write(fn func(*sql.Tx) error) error {
	tx, err := d.SQL.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (d *DB) schema() error {
	return d.Write(func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version > schemaVersion {
			return fmt.Errorf("database schema %d is newer than this program supports", version)
		}
		if version == schemaVersion {
			return nil
		}
		if version == 0 {
			_, err := tx.Exec(`
CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE progress (slug TEXT PRIMARY KEY, entry TEXT NOT NULL CHECK(json_valid(entry)));
CREATE TABLE documents (slug TEXT NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY(slug,kind));
CREATE TABLE attempts (id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL, created_at TEXT NOT NULL, code TEXT NOT NULL, review TEXT CHECK(review IS NULL OR json_valid(review)), legacy_path TEXT UNIQUE);
CREATE INDEX attempts_problem ON attempts(slug,id);
CREATE TABLE tutor_sessions (id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL, provider TEXT NOT NULL, started_at TEXT NOT NULL, retired INTEGER NOT NULL DEFAULT 0);
CREATE INDEX sessions_problem ON tutor_sessions(slug,id);
CREATE TABLE tutor_events (session_id INTEGER NOT NULL REFERENCES tutor_sessions(id), seq INTEGER NOT NULL, event TEXT NOT NULL CHECK(json_valid(event)), PRIMARY KEY(session_id,seq));
CREATE TABLE pending_submissions (slug TEXT PRIMARY KEY, payload TEXT NOT NULL CHECK(json_valid(payload)));
PRAGMA user_version=1;`)
			if err != nil {
				return err
			}
			version = 1
		}
		if version == 1 {
			// Early builds shipped both variants under version 1. Inspect the table so
			// either variant upgrades safely without rebuilding or replacing attempts.
			var exists int
			if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('attempts') WHERE name='submission_key'").Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if _, err := tx.Exec("ALTER TABLE attempts ADD COLUMN submission_key TEXT"); err != nil {
					return err
				}
			}
			if _, err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS attempts_submission_key ON attempts(submission_key)"); err != nil {
				return err
			}
			if _, err := tx.Exec("PRAGMA user_version=2"); err != nil {
				return err
			}
			version = 2
		}
		if version == 2 {
			// Which fixtures a run broke is the one thing the test output knows
			// and nothing kept. Counting them is what turns "it failed again"
			// into "you keep breaking the empty-input case".
			_, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS test_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL, ran_at TEXT NOT NULL, passed INTEGER NOT NULL, failed INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS test_runs_problem ON test_runs(slug,id);
CREATE TABLE IF NOT EXISTS case_failures (slug TEXT NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL, failures INTEGER NOT NULL DEFAULT 0, last_at TEXT NOT NULL, PRIMARY KEY(slug,kind,name));
PRAGMA user_version=3;`)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *DB) Document(slug, kind string) (string, error) {
	var text string
	err := d.SQL.QueryRow("SELECT text FROM documents WHERE slug=? AND kind=?", slug, kind).Scan(&text)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return text, err
}
func (d *DB) SaveDocument(slug, kind, text string) error {
	_, err := d.SQL.Exec("INSERT INTO documents VALUES(?,?,?) ON CONFLICT(slug,kind) DO UPDATE SET text=excluded.text", slug, kind, text)
	return err
}
func (d *DB) EnsureDocument(slug, kind, text string) error {
	_, err := d.SQL.Exec("INSERT OR IGNORE INTO documents VALUES(?,?,?)", slug, kind, text)
	return err
}

type Attempt struct {
	ID        int64
	Slug      string
	CreatedAt time.Time
	Code      string
	Review    json.RawMessage
}

func (a Attempt) Ref() string { return fmt.Sprintf("attempt:%d", a.ID) }
func InsertAttempt(tx *sql.Tx, slug, code string, review []byte, now time.Time, keys ...string) (string, error) {
	var report any
	if len(review) > 0 {
		report = string(review)
	}
	var key any
	if len(keys) > 0 && keys[0] != "" {
		key = keys[0]
	}
	res, err := tx.Exec("INSERT INTO attempts(slug,created_at,code,review,submission_key) VALUES(?,?,?,?,?)", slug, now.Format(time.RFC3339Nano), code, report, key)
	if err != nil {
		return "", err
	}
	id, err := res.LastInsertId()
	return fmt.Sprintf("attempt:%d", id), err
}
func (d *DB) Archive(slug, code string, now time.Time) (ref string, err error) {
	err = d.Write(func(tx *sql.Tx) error { ref, err = InsertAttempt(tx, slug, code, nil, now); return err })
	return
}
func (d *DB) Attempts(slug string) ([]Attempt, error) {
	rows, err := d.SQL.Query("SELECT id,slug,created_at,code,review FROM attempts WHERE slug=? ORDER BY id", slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		var created string
		var review sql.NullString
		if err := rows.Scan(&a.ID, &a.Slug, &created, &a.Code, &review); err != nil {
			return nil, err
		}
		a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		if review.Valid {
			a.Review = json.RawMessage(review.String)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (d *DB) Attempt(ref string) (Attempt, error) {
	var a Attempt
	var created string
	var review sql.NullString
	id := strings.TrimPrefix(ref, "attempt:")
	err := d.SQL.QueryRow("SELECT id,slug,created_at,code,review FROM attempts WHERE id=?", id).Scan(&a.ID, &a.Slug, &created, &a.Code, &review)
	if err != nil {
		return a, err
	}
	a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if review.Valid {
		a.Review = json.RawMessage(review.String)
	}
	return a, err
}
func (d *DB) SavePending(slug string, payload []byte) error {
	_, err := d.SQL.Exec("INSERT INTO pending_submissions VALUES(?,?) ON CONFLICT(slug) DO UPDATE SET payload=excluded.payload", slug, string(payload))
	return err
}
func (d *DB) Pending() (map[string]json.RawMessage, error) {
	rows, err := d.SQL.Query("SELECT slug,payload FROM pending_submissions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var slug, text string
		if err := rows.Scan(&slug, &text); err != nil {
			return nil, err
		}
		out[slug] = json.RawMessage(text)
	}
	return out, rows.Err()
}

type Session struct {
	ID             int64
	Slug, Provider string
	Started        time.Time
	Events         []json.RawMessage
}

func (d *DB) NewSession(slug, provider string, started time.Time) (int64, error) {
	r, e := d.SQL.Exec("INSERT INTO tutor_sessions(slug,provider,started_at) VALUES(?,?,?)", slug, provider, started.Format(time.RFC3339Nano))
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func (d *DB) Session(slug string) (*Session, error) {
	var s Session
	var started string
	err := d.SQL.QueryRow("SELECT id,slug,provider,started_at FROM tutor_sessions WHERE slug=? AND retired=0 ORDER BY id DESC LIMIT 1", slug).Scan(&s.ID, &s.Slug, &s.Provider, &started)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.Started, err = time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return nil, err
	}
	rows, err := d.SQL.Query("SELECT event FROM tutor_events WHERE session_id=? ORDER BY seq", s.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		s.Events = append(s.Events, json.RawMessage(text))
	}
	return &s, rows.Err()
}
func (d *DB) RetireSession(slug string) error {
	_, e := d.SQL.Exec("UPDATE tutor_sessions SET retired=1 WHERE slug=?", slug)
	return e
}
func (d *DB) AppendEvent(sessionID int64, seq int, event []byte) error {
	_, e := d.SQL.Exec("INSERT INTO tutor_events VALUES(?,?,?)", sessionID, seq, string(event))
	return e
}
