package studydb

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func inviteDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateV6IsReRunnable(t *testing.T) {
	db := inviteDB(t)
	var version int
	if err := db.SQL.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
	// The step is re-runnable, like every rung below it.
	if err := db.Write(migrateV6); err != nil {
		t.Errorf("migrateV6 is not idempotent: %v", err)
	}
}

// Every column that bounds an invite has to actually bound it.
func TestCreateInviteRefusesWhatWouldBeUnbounded(t *testing.T) {
	db := inviteDB(t)
	now := time.Now()
	soon := now.Add(time.Hour)

	// Zero is not "no money", it is "no ceiling" — see tutorBudgetLeft.
	if _, err := db.CreateInvite(HashToken("a"), 1, 0, "", now, soon); err == nil {
		t.Error("an invite with no ceiling was accepted")
	}
	if _, err := db.CreateInvite(HashToken("b"), 0, 5, "", now, soon); err == nil {
		t.Error("an invite with no uses was accepted")
	}
	if _, err := db.CreateInvite(HashToken("c"), 1, 5, "", now, now.Add(-time.Hour)); err == nil {
		t.Error("an invite that had already expired was accepted")
	}
}

// Unknown, expired and exhausted are one error on purpose: the site answers
// all three identically so the endpoint cannot be used to find live invites.
func TestInviteByTokenIsLiveOnly(t *testing.T) {
	db := inviteDB(t)
	now := time.Now()

	if _, err := db.InviteByToken("never-existed", now); !errors.Is(err, ErrNoInvite) {
		t.Errorf("unknown token: %v, want ErrNoInvite", err)
	}

	if _, err := db.CreateInvite(HashToken("live"), 2, 5, "study group", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	v, err := db.InviteByToken("live", now)
	if err != nil {
		t.Fatalf("a live invite did not resolve: %v", err)
	}
	if v.UsesLeft != 2 || v.BudgetUSD != 5 || v.Note != "study group" {
		t.Errorf("invite = %+v", v)
	}

	// Past its deadline it is gone, without anything having to sweep it.
	if _, err := db.InviteByToken("live", now.Add(2*time.Hour)); !errors.Is(err, ErrNoInvite) {
		t.Errorf("expired token: %v, want ErrNoInvite", err)
	}

	if err := db.RevokeInvite(v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InviteByToken("live", now); !errors.Is(err, ErrNoInvite) {
		t.Errorf("revoked token: %v, want ErrNoInvite", err)
	}
	// The row survives so `invite list` can still account for it.
	all, err := db.Invites()
	if err != nil || len(all) != 1 {
		t.Fatalf("invites = %v, %v", all, err)
	}
}

func TestRedeemInviteSpendsAUseAndTakesTheCeilingFromIt(t *testing.T) {
	db := inviteDB(t)
	now := time.Now()
	if _, err := db.CreateInvite(HashToken("tok"), 2, 7.5, "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	u, err := db.RedeemInvite("tok", "dima", HashToken("dima-personal"), now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "dima" || !u.HasToken {
		t.Errorf("user = %+v", u)
	}
	if u.BudgetUSD != 7.5 {
		t.Errorf("budget = %v, want the invite's 7.5", u.BudgetUSD)
	}
	// They sign in with their own token, not the invite's, which is what makes
	// them revocable one at a time.
	if _, err := db.UserByToken("dima-personal"); err != nil {
		t.Errorf("the newcomer cannot sign in with their own token: %v", err)
	}
	if _, err := db.UserByToken("tok"); err == nil {
		t.Error("the invite token signs somebody in by itself")
	}

	v, err := db.InviteByToken("tok", now)
	if err != nil || v.UsesLeft != 1 {
		t.Fatalf("after one redemption: %+v, %v", v, err)
	}

	// A name is a name: users.name is UNIQUE, and the caller has to be told.
	if _, err := db.RedeemInvite("tok", "dima", HashToken("other"), now); err == nil {
		t.Error("a duplicate name was accepted")
	}
}

// Two people opening the same link at the same moment must not both take the
// last use.
//
// Note what this test does and does not do. It pins the invariant — exactly one
// account comes out of a one-use invite — which is worth having. It does not
// discriminate between implementations: a deliberately naive
// check-then-decrement was tried here and also passed, because SQLite runs
// write transactions one at a time and the whole body is inside one. The
// guarantee is the transaction; do not read a green run here as evidence that
// the single-statement decrement is what saves it.
func TestRedeemInviteCannotOverspendUnderRace(t *testing.T) {
	db := inviteDB(t)
	now := time.Now()
	if _, err := db.CreateInvite(HashToken("one-use"), 1, 5, "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won int
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a'+i)) + "-racer"
			if _, err := db.RedeemInvite("one-use", name, HashToken(name), now); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if won != 1 {
		t.Errorf("%d of %d redemptions succeeded on a one-use invite, want exactly 1", won, racers)
	}
	users, err := db.Users()
	if err != nil {
		t.Fatal(err)
	}
	// The owner plus exactly one newcomer.
	if len(users) != 2 {
		t.Errorf("%d users exist, want the owner and one newcomer", len(users))
	}
}

// users.name's UNIQUE is case-sensitive but the CLI matches names with
// EqualFold, so "Dima" beside "dima" would be two rows that
// `nc user revoke Dima` could not tell apart.
func TestRedeemInviteRefusesANameThatOnlyDiffersInCase(t *testing.T) {
	db := inviteDB(t)
	now := time.Now()
	if _, err := db.CreateInvite(HashToken("tok"), 3, 5, "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RedeemInvite("tok", "Dima", HashToken("one"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RedeemInvite("tok", "dima", HashToken("two"), now); !errors.Is(err, ErrNameTaken) {
		t.Errorf("second redemption: %v, want ErrNameTaken", err)
	}
	// The owner's own name is not available either.
	if _, err := db.RedeemInvite("tok", "OWNER", HashToken("three"), now); !errors.Is(err, ErrNameTaken) {
		t.Errorf("taking the owner's name: %v, want ErrNameTaken", err)
	}
	// A refused name must not have cost a use.
	v, err := db.InviteByToken("tok", now)
	if err != nil || v.UsesLeft != 2 {
		t.Errorf("uses left = %+v (%v), want 2 after one success and two refusals", v, err)
	}
}
