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

const schemaVersion = 4

// OwnerID is the first user. The CLI is the owner's own tool and always acts as
// this id; the legacy import and every pre-multi-user row belong to it.
const OwnerID int64 = 1

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
	// Rollback after a Commit is a no-op that reports ErrTxDone; there is
	// nothing to do with that.
	defer func() { _ = tx.Rollback() }()
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
			// Without this the ladder stops here while user_version already says 3,
			// so a v2 database would run one process lifetime on half a schema and
			// only heal on the next Open.
			version = 3
		}
		if version == 3 {
			if err := migrateV4(tx); err != nil {
				return err
			}
			// foreign_key_check reports violations as result rows rather than as an
			// error, so Exec would discard exactly what we want to catch.
			rows, err := tx.Query("PRAGMA foreign_key_check")
			if err != nil {
				return err
			}
			dangling := rows.Next()
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if dangling {
				return fmt.Errorf("schema v4 left dangling references")
			}
			if _, err := tx.Exec("PRAGMA user_version=4"); err != nil {
				return err
			}
			version = 4
		}
		return nil
	})
}

// migrateV4 adds the user dimension.
//
// Only the four slug-keyed tables get a composite primary key, and none of them
// takes part in a foreign key, so nothing here needs foreign keys deferred or
// switched off — PRAGMA foreign_keys is a no-op inside a transaction anyway.
//
// attempts, tutor_sessions and test_runs keep their integer primary keys on
// purpose. A composite key there would leave `id` non-unique, and
// tutor_events.session_id REFERENCES tutor_sessions(id) would become a foreign
// key mismatch — which SQLite raises on the first insert rather than at CREATE
// TABLE, so the migration would look clean and AppendEvent would fail later.
//
// Every step is written to be safe to re-run. The rungs above do the same (v1
// probes pragma_table_info because two variants shipped under that number), and
// the reason is the same: a database can arrive here with user_version wound
// back while its tables already hold the newer shape.
func migrateV4(tx *sql.Tx) error {
	exec := func(stmt string) error {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("schema v4 (%.60s): %w", strings.TrimSpace(stmt), err)
		}
		return nil
	}
	// AUTOINCREMENT so a deleted user's id is never handed to the next one:
	// tutor_sessions and test_runs carry no cascade, and a reused id would
	// inherit their rows.
	if err := exec(`CREATE TABLE IF NOT EXISTS users (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  name             TEXT NOT NULL UNIQUE,
  token_hash       TEXT UNIQUE,
  created_at       TEXT NOT NULL,
  tutor_budget_usd REAL NOT NULL DEFAULT 0,
  tutor_spend_usd  REAL NOT NULL DEFAULT 0,
  spend_month      TEXT NOT NULL DEFAULT '',
  disabled         INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		return err
	}
	// strftime keeps this argument-free; the layout still parses with
	// time.RFC3339Nano, whose fractional part is optional.
	if err := exec(`INSERT OR IGNORE INTO users(id,name,token_hash,created_at)
  VALUES(1,'owner',NULL,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return err
	}

	for _, r := range v4Rebuilds {
		has, err := hasColumn(tx, r.table, "user_id")
		if err != nil {
			return err
		}
		if has {
			continue
		}
		for _, stmt := range r.stmts {
			if err := exec(stmt); err != nil {
				return err
			}
		}
	}

	// ADD COLUMN only, to keep id unique for tutor_events' foreign key. The price
	// is no cascade from users, so deleting a user clears these two explicitly.
	for _, t := range []string{"tutor_sessions", "test_runs"} {
		has, err := hasColumn(tx, t, "user_id")
		if err != nil {
			return err
		}
		if !has {
			// NOT NULL DEFAULT 1 is a silent-correctness hazard: an insert that
			// forgets user_id lands on the owner without an error. The guard is the
			// Go API plus TestEveryPersonalTableIsScoped, not the schema.
			if err := exec("ALTER TABLE " + t + " ADD COLUMN user_id INTEGER NOT NULL DEFAULT 1"); err != nil {
				return err
			}
		}
	}
	for _, idx := range []string{
		"DROP INDEX IF EXISTS sessions_problem",
		"CREATE INDEX sessions_problem ON tutor_sessions(user_id,slug,id)",
		"DROP INDEX IF EXISTS test_runs_problem",
		"CREATE INDEX test_runs_problem ON test_runs(user_id,slug,id)",
	} {
		if err := exec(idx); err != nil {
			return err
		}
	}

	// tutor_events needs no user of its own: it is reachable only through its
	// session. Rebuilding it buys the cascade that makes retiring one cheap.
	// There is no new column to probe, so the probe is the cascade itself.
	var ddl string
	if err := tx.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='tutor_events'").Scan(&ddl); err != nil {
		return err
	}
	if !strings.Contains(ddl, "ON DELETE CASCADE") {
		for _, stmt := range v4TutorEvents {
			if err := exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasColumn(tx *sql.Tx, table, column string) (bool, error) {
	var n int
	err := tx.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&n)
	return n > 0, err
}

// v4Rebuilds are the tables whose primary key has to change, which in SQLite
// means create, copy, drop, rename.
var v4Rebuilds = []struct {
	table string
	stmts []string
}{
	{"progress", []string{
		`CREATE TABLE progress_v4 (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug    TEXT NOT NULL,
  entry   TEXT NOT NULL CHECK(json_valid(entry)),
  PRIMARY KEY(user_id,slug)
)`,
		`INSERT INTO progress_v4(user_id,slug,entry) SELECT 1,slug,entry FROM progress`,
		`DROP TABLE progress`,
		`ALTER TABLE progress_v4 RENAME TO progress`,
	}},
	// The ('', 'log') row the legacy import writes needs no special case: an
	// empty slug satisfies NOT NULL and (1,'','log') is a valid composite key.
	{"documents", []string{
		`CREATE TABLE documents_v4 (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug    TEXT NOT NULL,
  kind    TEXT NOT NULL,
  text    TEXT NOT NULL,
  PRIMARY KEY(user_id,slug,kind)
)`,
		`INSERT INTO documents_v4(user_id,slug,kind,text) SELECT 1,slug,kind,text FROM documents`,
		`DROP TABLE documents`,
		`ALTER TABLE documents_v4 RENAME TO documents`,
	}},
	{"pending_submissions", []string{
		`CREATE TABLE pending_submissions_v4 (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug    TEXT NOT NULL,
  payload TEXT NOT NULL CHECK(json_valid(payload)),
  PRIMARY KEY(user_id,slug)
)`,
		`INSERT INTO pending_submissions_v4(user_id,slug,payload) SELECT 1,slug,payload FROM pending_submissions`,
		`DROP TABLE pending_submissions`,
		`ALTER TABLE pending_submissions_v4 RENAME TO pending_submissions`,
	}},
	{"case_failures", []string{
		`CREATE TABLE case_failures_v4 (
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug     TEXT NOT NULL,
  kind     TEXT NOT NULL,
  name     TEXT NOT NULL,
  failures INTEGER NOT NULL DEFAULT 0,
  last_at  TEXT NOT NULL,
  PRIMARY KEY(user_id,slug,kind,name)
)`,
		`INSERT INTO case_failures_v4(user_id,slug,kind,name,failures,last_at)
  SELECT 1,slug,kind,name,failures,last_at FROM case_failures`,
		`DROP TABLE case_failures`,
		`ALTER TABLE case_failures_v4 RENAME TO case_failures`,
	}},
	// attempts is rebuilt rather than altered: both UNIQUE constraints are column
	// constraints and have to become per-user. The explicit id in the copy is what
	// keeps attempt:<id> references and submission_key lookups valid.
	{"attempts", []string{
		`CREATE TABLE attempts_v4 (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug           TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  code           TEXT NOT NULL,
  review         TEXT CHECK(review IS NULL OR json_valid(review)),
  legacy_path    TEXT,
  submission_key TEXT,
  UNIQUE(user_id,legacy_path),
  UNIQUE(user_id,submission_key)
)`,
		`INSERT INTO attempts_v4(id,user_id,slug,created_at,code,review,legacy_path,submission_key)
  SELECT id,1,slug,created_at,code,review,legacy_path,submission_key FROM attempts`,
		`DROP TABLE attempts`,
		`ALTER TABLE attempts_v4 RENAME TO attempts`,
		`DROP INDEX IF EXISTS attempts_problem`,
		`CREATE INDEX attempts_problem ON attempts(user_id,slug,id)`,
	}},
}

var v4TutorEvents = []string{
	`CREATE TABLE tutor_events_v4 (
  session_id INTEGER NOT NULL REFERENCES tutor_sessions(id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  event      TEXT NOT NULL CHECK(json_valid(event)),
  PRIMARY KEY(session_id,seq)
)`,
	`INSERT INTO tutor_events_v4(session_id,seq,event) SELECT session_id,seq,event FROM tutor_events`,
	`DROP TABLE tutor_events`,
	`ALTER TABLE tutor_events_v4 RENAME TO tutor_events`,
}

// Scope is one user's view of the study data. Every personal read and write goes
// through it, so nothing reaches another user's rows by forgetting a filter.
//
// Scoped methods live here rather than on DB on purpose: taking them off DB is
// what makes the compiler name each call site that still needs a user, instead
// of leaving a silent default.
type Scope struct {
	db     *DB
	userID int64
}

// For returns the view belonging to one user. It opens nothing: the connection
// and its single write lane are shared, so a per-request Scope is free.
func (d *DB) For(userID int64) *Scope { return &Scope{db: d, userID: userID} }

// Owner is the view the CLI uses.
func (d *DB) Owner() *Scope { return d.For(OwnerID) }

func (s *Scope) UserID() int64 { return s.userID }

// Unscoped reaches the shared handle for the few statements that are genuinely
// global, such as the schema version and the legacy-import marker.
func (s *Scope) Unscoped() *DB { return s.db }

// Write runs fn in a transaction on the underlying database.
func (s *Scope) Write(fn func(*sql.Tx) error) error { return s.db.Write(fn) }

func (s *Scope) Document(slug, kind string) (string, error) {
	var text string
	err := s.db.SQL.QueryRow("SELECT text FROM documents WHERE user_id=? AND slug=? AND kind=?", s.userID, slug, kind).Scan(&text)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return text, err
}
func (s *Scope) SaveDocument(slug, kind, text string) error {
	_, err := s.db.SQL.Exec("INSERT INTO documents(user_id,slug,kind,text) VALUES(?,?,?,?) ON CONFLICT(user_id,slug,kind) DO UPDATE SET text=excluded.text", s.userID, slug, kind, text)
	return err
}
func (s *Scope) EnsureDocument(slug, kind, text string) error {
	_, err := s.db.SQL.Exec("INSERT OR IGNORE INTO documents(user_id,slug,kind,text) VALUES(?,?,?,?)", s.userID, slug, kind, text)
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

// InsertAttempt takes userID explicitly because it runs inside a caller's
// transaction, where there is no Scope to carry it.
func InsertAttempt(tx *sql.Tx, userID int64, slug, code string, review []byte, now time.Time, keys ...string) (string, error) {
	var report any
	if len(review) > 0 {
		report = string(review)
	}
	var key any
	if len(keys) > 0 && keys[0] != "" {
		key = keys[0]
	}
	res, err := tx.Exec("INSERT INTO attempts(user_id,slug,created_at,code,review,submission_key) VALUES(?,?,?,?,?,?)", userID, slug, now.Format(time.RFC3339Nano), code, report, key)
	if err != nil {
		return "", err
	}
	id, err := res.LastInsertId()
	return fmt.Sprintf("attempt:%d", id), err
}
func (s *Scope) Archive(slug, code string, now time.Time) (ref string, err error) {
	err = s.db.Write(func(tx *sql.Tx) error {
		ref, err = InsertAttempt(tx, s.userID, slug, code, nil, now)
		return err
	})
	return
}
func (s *Scope) Attempts(slug string) ([]Attempt, error) {
	rows, err := s.db.SQL.Query("SELECT id,slug,created_at,code,review FROM attempts WHERE user_id=? AND slug=? ORDER BY id", s.userID, slug)
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

// Attempt looks an archived attempt up by its "attempt:<id>" reference. The id
// is a global integer, so the user filter is what keeps one reference from
// reading another user's code.
func (s *Scope) Attempt(ref string) (Attempt, error) {
	var a Attempt
	var created string
	var review sql.NullString
	id := strings.TrimPrefix(ref, "attempt:")
	err := s.db.SQL.QueryRow("SELECT id,slug,created_at,code,review FROM attempts WHERE id=? AND user_id=?", id, s.userID).Scan(&a.ID, &a.Slug, &created, &a.Code, &review)
	if err != nil {
		return a, err
	}
	a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if review.Valid {
		a.Review = json.RawMessage(review.String)
	}
	return a, err
}
func (s *Scope) SavePending(slug string, payload []byte) error {
	_, err := s.db.SQL.Exec("INSERT INTO pending_submissions(user_id,slug,payload) VALUES(?,?,?) ON CONFLICT(user_id,slug) DO UPDATE SET payload=excluded.payload", s.userID, slug, string(payload))
	return err
}
func (s *Scope) DeletePendingIn(tx *sql.Tx, slug string) error {
	_, err := tx.Exec("DELETE FROM pending_submissions WHERE user_id=? AND slug=?", s.userID, slug)
	return err
}
func (s *Scope) Pending() (map[string]json.RawMessage, error) {
	rows, err := s.db.SQL.Query("SELECT slug,payload FROM pending_submissions WHERE user_id=?", s.userID)
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

func (s *Scope) NewSession(slug, provider string, started time.Time) (int64, error) {
	r, e := s.db.SQL.Exec("INSERT INTO tutor_sessions(user_id,slug,provider,started_at) VALUES(?,?,?,?)", s.userID, slug, provider, started.Format(time.RFC3339Nano))
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func (s *Scope) Session(slug string) (*Session, error) {
	var rec Session
	var started string
	err := s.db.SQL.QueryRow("SELECT id,slug,provider,started_at FROM tutor_sessions WHERE user_id=? AND slug=? AND retired=0 ORDER BY id DESC LIMIT 1", s.userID, slug).Scan(&rec.ID, &rec.Slug, &rec.Provider, &started)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.Started, err = time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.SQL.Query("SELECT event FROM tutor_events WHERE session_id=? ORDER BY seq", rec.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		rec.Events = append(rec.Events, json.RawMessage(text))
	}
	return &rec, rows.Err()
}
func (s *Scope) RetireSession(slug string) error {
	_, e := s.db.SQL.Exec("UPDATE tutor_sessions SET retired=1 WHERE user_id=? AND slug=?", s.userID, slug)
	return e
}

// AppendEvent guards on ownership as well as on the session id. The id comes
// from a scoped lookup already, so this is belt and braces — but it is one
// subquery, and it makes the absence of a cross-user write provable.
func (s *Scope) AppendEvent(sessionID int64, seq int, event []byte) error {
	res, err := s.db.SQL.Exec(
		"INSERT INTO tutor_events(session_id,seq,event) SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM tutor_sessions WHERE id=? AND user_id=?)",
		sessionID, seq, string(event), sessionID, s.userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("tutor session %d does not belong to user %d", sessionID, s.userID)
	}
	return nil
}
