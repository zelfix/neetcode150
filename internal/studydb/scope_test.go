package studydb

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// personalTables is every table holding something that belongs to one person.
var personalTables = []string{
	"progress", "documents", "attempts", "tutor_sessions",
	"pending_submissions", "test_runs", "case_failures",
}

// The schema carries NOT NULL DEFAULT 1 on two of these columns, so an insert
// that forgets user_id lands on the owner without raising anything. Nothing in
// SQLite will catch that, which makes this list the guard: a table that gains
// personal data without a user, or a column list that drifts, fails here rather
// than in somebody's data.
func TestEveryPersonalTableIsScoped(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range personalTables {
		var n int
		err := db.SQL.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name='user_id'", table).Scan(&n)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("%s has no user_id column", table)
		}
	}
	// tutor_events is reached only through its session, and relies on the cascade
	// to disappear with it.
	var ddl string
	if err := db.SQL.QueryRow("SELECT sql FROM sqlite_master WHERE name='tutor_events'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "ON DELETE CASCADE") {
		t.Error("tutor_events does not cascade from its session")
	}
}

func TestScopesDoNotSeeEachOther(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	guest, err := db.CreateUser("guest", "token-guest", 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner, other := db.Owner(), db.For(guest.ID)
	now := time.Now()

	for _, c := range []struct {
		scope *Scope
		mark  string
	}{{owner, "owner"}, {other, "guest"}} {
		if err := c.scope.SaveDocument("two-sum", "notes", c.mark+" notes"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.scope.Archive("two-sum", "go", c.mark+" code", now); err != nil {
			t.Fatal(err)
		}
		if err := c.scope.SavePending("two-sum", []byte(`{"Code":"`+c.mark+`"}`)); err != nil {
			t.Fatal(err)
		}
		if err := c.scope.RecordTestRun("two-sum", now, 1, 1, []FailedCase{{Kind: "edge", Name: c.mark}}); err != nil {
			t.Fatal(err)
		}
		id, err := c.scope.NewSession("two-sum", "claude", "tutor", now)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.scope.AppendEvent(id, 1, []byte(`{"kind":"text","text":"`+c.mark+`"}`)); err != nil {
			t.Fatal(err)
		}
	}

	if text, err := owner.Document("two-sum", "notes"); err != nil || text != "owner notes" {
		t.Fatalf("owner notes = %q, %v", text, err)
	}
	if text, err := other.Document("two-sum", "notes"); err != nil || text != "guest notes" {
		t.Fatalf("guest notes = %q, %v", text, err)
	}
	for _, c := range []struct {
		scope *Scope
		mark  string
	}{{owner, "owner"}, {other, "guest"}} {
		attempts, err := c.scope.Attempts("two-sum")
		if err != nil {
			t.Fatal(err)
		}
		if len(attempts) != 1 || attempts[0].Code != c.mark+" code" {
			t.Fatalf("%s sees %d attempts, %+v", c.mark, len(attempts), attempts)
		}
		pending, err := c.scope.Pending()
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 || !strings.Contains(string(pending["two-sum"]), c.mark) {
			t.Fatalf("%s sees pending %v", c.mark, pending)
		}
		tally, err := c.scope.Tally("")
		if err != nil || tally.Runs != 1 {
			t.Fatalf("%s sees %d runs, %v", c.mark, tally.Runs, err)
		}
		failures, err := c.scope.CaseFailures("", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(failures) != 1 || failures[0].Name != c.mark {
			t.Fatalf("%s sees failures %+v", c.mark, failures)
		}
		kinds, err := c.scope.FailuresByKind()
		if err != nil || kinds["edge"] != 1 {
			t.Fatalf("%s sees kinds %v, %v", c.mark, kinds, err)
		}
		sess, err := c.scope.Session("two-sum", "tutor")
		if err != nil || sess == nil {
			t.Fatalf("%s has no session: %v", c.mark, err)
		}
		if len(sess.Events) != 1 || !strings.Contains(string(sess.Events[0]), c.mark) {
			t.Fatalf("%s sees events %v", c.mark, sess.Events)
		}
	}

	// An attempt reference is a bare global integer, so the user filter is the
	// only thing standing between one reference and another person's code.
	ownerAttempts, err := owner.Attempts("two-sum")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Attempt(ownerAttempts[0].Ref()); err == nil {
		t.Fatal("a guest read the owner's attempt by reference")
	}

	// Retiring and deleting must not reach across either.
	if err := other.RetireSession("two-sum", "tutor"); err != nil {
		t.Fatal(err)
	}
	if sess, err := owner.Session("two-sum", "tutor"); err != nil || sess == nil {
		t.Fatal("retiring the guest's session retired the owner's")
	}
	// The sharpest of the leaks this replaces: the old DELETE was by slug alone,
	// so one person submitting wiped everybody else's pending submission for that
	// problem.
	if err := other.Write(func(tx *sql.Tx) error { return other.DeletePendingIn(tx, "two-sum") }); err != nil {
		t.Fatal(err)
	}
	if pending, err := owner.Pending(); err != nil || len(pending) != 1 {
		t.Fatalf("the guest's submission cleared the owner's pending work: %v %v", pending, err)
	}
	if pending, err := other.Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("the guest's own pending work survived: %v %v", pending, err)
	}
}

func TestDeletingAUserRemovesEverythingOfTheirs(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	guest, err := db.CreateUser("guest", "token-guest", 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scope, now := db.For(guest.ID), time.Now()
	if err := scope.SaveDocument("two-sum", "notes", "guest notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Archive("two-sum", "go", "guest code", now); err != nil {
		t.Fatal(err)
	}
	if err := scope.RecordTestRun("two-sum", now, 1, 0, nil); err != nil {
		t.Fatal(err)
	}
	id, err := scope.NewSession("two-sum", "claude", "tutor", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.AppendEvent(id, 1, []byte(`{"kind":"text"}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(guest.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range append(personalTables, "tutor_events") {
		var n int
		query := "SELECT count(*) FROM " + table
		if table != "tutor_events" {
			query += " WHERE user_id=?"
		}
		var args []any
		if table != "tutor_events" {
			args = append(args, guest.ID)
		}
		if err := db.SQL.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows", table, n)
		}
	}
	if err := db.DeleteUser(OwnerID); err == nil {
		t.Error("the owner was deletable")
	}
}

func TestRevokedTokenStopsResolving(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	guest, err := db.CreateUser("guest", HashToken("secret"), 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u, err := db.UserByToken("secret"); err != nil || u.ID != guest.ID {
		t.Fatalf("token did not resolve: %v", err)
	}
	if _, err := db.UserByToken("wrong"); err != ErrNoUser {
		t.Fatalf("a wrong token resolved: %v", err)
	}
	if err := db.SetUserDisabled(guest.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByToken("secret"); err != ErrNoUser {
		t.Fatal("a disabled user still logged in")
	}
}

func TestTutorSpendRestartsEachMonth(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	guest, err := db.CreateUser("guest", "", 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if total, err := db.AddTutorSpend(guest.ID, 0.30, "2026-11"); err != nil || total != 0.30 {
		t.Fatalf("total=%v err=%v", total, err)
	}
	total, err := db.AddTutorSpend(guest.ID, 0.15, "2026-11")
	if err != nil || total < 0.449 || total > 0.451 {
		t.Fatalf("total=%v err=%v", total, err)
	}
	if total, err := db.AddTutorSpend(guest.ID, 0.10, "2026-12"); err != nil || total != 0.10 {
		t.Fatalf("new month total=%v err=%v", total, err)
	}
}
