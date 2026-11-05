package studydb

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNoUser is returned when a token matches nobody, or names a disabled user.
var ErrNoUser = errors.New("no such user")

// User is one person's identity and tutor allowance. The join token itself is
// never stored — only its hash, so a copy of the database does not hand over
// anyone's account.
type User struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	BudgetUSD  float64
	SpendUSD   float64
	SpendMonth string
	Disabled   bool
	HasToken   bool
}

// HashToken is the one definition of how a join token maps to what is stored.
// The CLI mints the token and the site verifies it, so both sides must agree.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const userColumns = "id,name,created_at,tutor_budget_usd,tutor_spend_usd,spend_month,disabled,token_hash IS NOT NULL"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created string
	if err := row.Scan(&u.ID, &u.Name, &created, &u.BudgetUSD, &u.SpendUSD, &u.SpendMonth, &u.Disabled, &u.HasToken); err != nil {
		return u, err
	}
	var err error
	u.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return u, err
}

// CreateUser adds a person and returns them. tokenHash may be empty for an
// identity that has no web access yet.
func (d *DB) CreateUser(name, tokenHash string, budgetUSD float64, now time.Time) (User, error) {
	var hash any
	if tokenHash != "" {
		hash = tokenHash
	}
	res, err := d.SQL.Exec(
		"INSERT INTO users(name,token_hash,created_at,tutor_budget_usd) VALUES(?,?,?,?)",
		name, hash, now.Format(time.RFC3339Nano), budgetUSD)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return d.User(id)
}

func (d *DB) User(id int64) (User, error) {
	return scanUser(d.SQL.QueryRow("SELECT "+userColumns+" FROM users WHERE id=?", id))
}

// UserByToken resolves a join token. A disabled user is reported as absent so a
// revoked link behaves exactly like one that never existed.
func (d *DB) UserByToken(token string) (User, error) {
	if token == "" {
		return User{}, ErrNoUser
	}
	u, err := scanUser(d.SQL.QueryRow("SELECT "+userColumns+" FROM users WHERE token_hash=?", HashToken(token)))
	if err == sql.ErrNoRows {
		return User{}, ErrNoUser
	}
	if err != nil {
		return User{}, err
	}
	if u.Disabled {
		return User{}, ErrNoUser
	}
	return u, nil
}

func (d *DB) Users() ([]User, error) {
	rows, err := d.SQL.Query("SELECT " + userColumns + " FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserToken replaces a person's join link, or clears it when token is empty.
// Revoking this way keeps their study data; Disable keeps the row but refuses
// the login.
func (d *DB) SetUserToken(id int64, token string) error {
	var hash any
	if token != "" {
		hash = HashToken(token)
	}
	_, err := d.SQL.Exec("UPDATE users SET token_hash=? WHERE id=?", hash, id)
	return err
}

func (d *DB) SetUserDisabled(id int64, disabled bool) error {
	_, err := d.SQL.Exec("UPDATE users SET disabled=? WHERE id=?", disabled, id)
	return err
}

// AddTutorSpend accumulates what the tutor has cost this person and reports the
// new total. month identifies the budget period; when it changes the counter
// starts again, which is what makes the ceiling monthly rather than lifetime.
//
// This has to be written as the cost arrives. Session cost lives only in
// memory, so a restart would otherwise reset everyone's spend to zero.
func (d *DB) AddTutorSpend(id int64, amount float64, month string) (float64, error) {
	var total float64
	err := d.Write(func(tx *sql.Tx) error {
		var spend float64
		var stored string
		if err := tx.QueryRow("SELECT tutor_spend_usd,spend_month FROM users WHERE id=?", id).Scan(&spend, &stored); err != nil {
			return err
		}
		if stored != month {
			spend = 0
		}
		total = spend + amount
		_, err := tx.Exec("UPDATE users SET tutor_spend_usd=?,spend_month=? WHERE id=?", total, month, id)
		return err
	})
	return total, err
}

// DeleteUser removes a person and everything of theirs.
//
// The order matters: progress, documents, pending_submissions, case_failures and
// attempts cascade from users, but tutor_sessions and test_runs deliberately
// carry no foreign key — a composite key on tutor_sessions would have broken
// tutor_events' reference to its id — so those two are cleared by hand.
func (d *DB) DeleteUser(id int64) error {
	if id == OwnerID {
		return fmt.Errorf("refusing to delete the owner")
	}
	return d.Write(func(tx *sql.Tx) error {
		for _, stmt := range []string{
			"DELETE FROM test_runs WHERE user_id=?",
			"DELETE FROM tutor_sessions WHERE user_id=?", // cascades tutor_events
			"DELETE FROM users WHERE id=?",               // cascades the rest
		} {
			if _, err := tx.Exec(stmt, id); err != nil {
				return err
			}
		}
		return nil
	})
}
