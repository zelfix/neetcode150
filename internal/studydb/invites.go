package studydb

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNoInvite is returned for a token that is unknown, past its deadline, or
// out of uses. The three are deliberately one error: the site answers all of
// them identically, so that the endpoint cannot be used to find out which
// invites exist.
var ErrNoInvite = errors.New("no live invite for that token")

// ErrNameTaken is a name somebody already has. It is its own error because the
// person choosing it can fix it and a caller must be able to say so, rather
// than reporting a server fault for what is an ordinary collision.
//
// The comparison is case-insensitive even though users.name's UNIQUE is not:
// findUser in the CLI matches with EqualFold, so "Dima" and "dima" would both
// be legal rows that `nc user revoke Dima` could not tell apart.
var ErrNameTaken = errors.New("that name is taken")

// Invite is the authority to create an identity, delegated once and bounded.
//
// Every column that bounds it is a column rather than a convention: the
// deadline, the number of uses, and the ceiling each new person gets. The
// plaintext token is never here — only its hash, as with a person's own.
type Invite struct {
	ID        int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsesLeft  int
	BudgetUSD float64
	Note      string
}

const inviteColumns = "id,created_at,expires_at,uses_left,budget_usd,note"

func scanInvite(row interface{ Scan(...any) error }) (Invite, error) {
	var v Invite
	var created, expires string
	if err := row.Scan(&v.ID, &created, &expires, &v.UsesLeft, &v.BudgetUSD, &v.Note); err != nil {
		return v, err
	}
	var err error
	if v.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return v, err
	}
	v.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	return v, err
}

// CreateInvite records one. budgetUSD must be above zero: a ceiling of zero
// means no ceiling at all (see tutorBudgetLeft), and an invite is precisely
// where an unmetered account must not come from.
func (d *DB) CreateInvite(tokenHash string, uses int, budgetUSD float64, note string, now, expires time.Time) (Invite, error) {
	if uses < 1 {
		return Invite{}, fmt.Errorf("an invite needs at least one use, got %d", uses)
	}
	if budgetUSD <= 0 {
		return Invite{}, fmt.Errorf("an invite needs a tutor ceiling above zero: zero means unlimited")
	}
	if !expires.After(now) {
		return Invite{}, fmt.Errorf("an invite needs a deadline in the future")
	}
	res, err := d.SQL.Exec(
		"INSERT INTO invites(token_hash,created_at,expires_at,uses_left,budget_usd,note) VALUES(?,?,?,?,?,?)",
		tokenHash, now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), uses, budgetUSD, note)
	if err != nil {
		return Invite{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Invite{}, err
	}
	return scanInvite(d.SQL.QueryRow("SELECT "+inviteColumns+" FROM invites WHERE id=?", id))
}

// InviteByToken resolves a token to a *live* invite, or ErrNoInvite.
func (d *DB) InviteByToken(token string, now time.Time) (Invite, error) {
	if token == "" {
		return Invite{}, ErrNoInvite
	}
	v, err := scanInvite(d.SQL.QueryRow(
		"SELECT "+inviteColumns+" FROM invites WHERE token_hash=?", HashToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrNoInvite
	}
	if err != nil {
		return Invite{}, err
	}
	if v.UsesLeft < 1 || !v.ExpiresAt.After(now) {
		return Invite{}, ErrNoInvite
	}
	return v, nil
}

// Invites lists them newest first, for `nc invite list`. The tokens are not
// here to be listed: only what each one still allows.
func (d *DB) Invites() ([]Invite, error) {
	rows, err := d.SQL.Query("SELECT " + inviteColumns + " FROM invites ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		v, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeInvite spends the rest of an invite's uses without creating anybody,
// which is how a link that went to the wrong chat is put out of action. The
// row stays, so `invite list` can still show that it existed.
func (d *DB) RevokeInvite(id int64) error {
	res, err := d.SQL.Exec("UPDATE invites SET uses_left=0 WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no invite %d", id)
	}
	return nil
}

// RedeemInvite spends one use and creates the person it was spent on, in one
// transaction.
//
// The transaction is what makes two people opening the same link at the same
// moment safe: SQLite runs write transactions one at a time, so the whole
// read-modify-write here is serialised, and a check followed by a decrement
// would in fact be correct too — measured, not assumed.
//
// The single-statement form is kept anyway, because it does not depend on that
// being true. `uses_left > 0` lives in the WHERE clause, so the loser changes
// no rows and is told the invite is gone, which stays correct if this ever runs
// against something with weaker isolation than one writer at a time.
//
// personalTokenHash is the hash of the newcomer's *own* token, which the
// caller mints and keeps the plaintext of — the same contract CreateUser has,
// so that minting stays with the code that owns a source of randomness and
// hashing stays here. Deriving it from anything (the name, the invite) would
// make it guessable; it must come from crypto/rand.
//
// name must be free; users.name is NOT NULL UNIQUE, so a collision surfaces
// here as an error for the caller to show rather than a 500.
func (d *DB) RedeemInvite(inviteToken, name, personalTokenHash string, now time.Time) (User, error) {
	if inviteToken == "" || personalTokenHash == "" {
		return User{}, ErrNoInvite
	}
	var id int64
	err := d.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"UPDATE invites SET uses_left=uses_left-1 WHERE token_hash=? AND uses_left>0 AND expires_at>?",
			HashToken(inviteToken), now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		spent, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if spent == 0 {
			return ErrNoInvite
		}

		// Inside the transaction, where it cannot race with another redemption
		// choosing the same name a moment later.
		if err := claimName(tx, name, 0); err != nil {
			return err
		}

		var budget float64
		if err := tx.QueryRow("SELECT budget_usd FROM invites WHERE token_hash=?",
			HashToken(inviteToken)).Scan(&budget); err != nil {
			return err
		}
		// The ceiling comes off the invite, never from a default here: a row
		// created without one would be unmetered.
		person, err := tx.Exec(
			"INSERT INTO users(name,token_hash,created_at,tutor_budget_usd) VALUES(?,?,?,?)",
			name, personalTokenHash, now.Format(time.RFC3339Nano), budget)
		if err != nil {
			return err
		}
		id, err = person.LastInsertId()
		return err
	})
	if err != nil {
		return User{}, err
	}
	return d.User(id)
}

// claimName reports whether a name is free, inside the caller's transaction so
// that two people choosing it at once cannot both be told yes. exceptID is the
// row allowed to already hold it, for a rename that only changes capitalisation.
//
// Case-insensitive on purpose: users.name's UNIQUE is not, but findUser in the
// CLI matches with EqualFold, so "Dima" beside "dima" would be two rows that
// `nc user revoke Dima` could not tell apart.
func claimName(tx *sql.Tx, name string, exceptID int64) error {
	var clashes int
	if err := tx.QueryRow(
		"SELECT count(*) FROM users WHERE name=? COLLATE NOCASE AND id<>?", name, exceptID).Scan(&clashes); err != nil {
		return err
	}
	if clashes > 0 {
		return ErrNameTaken
	}
	return nil
}

// RenameUser changes what somebody is called. Progress is keyed by id, so a
// rename moves no work: it is a label, which is the whole reason people may
// choose their own.
func (d *DB) RenameUser(id int64, name string) error {
	if name == "" {
		return fmt.Errorf("a name cannot be empty")
	}
	return d.Write(func(tx *sql.Tx) error {
		if err := claimName(tx, name, id); err != nil {
			return err
		}
		res, err := tx.Exec("UPDATE users SET name=? WHERE id=?", name, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no user %d", id)
		}
		return nil
	})
}
