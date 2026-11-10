package studydb

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Export is one person's rows, table by table, in a shape that does not
// depend on this code to read: column names as keys, JSON-friendly values.
type Export struct {
	User   User                        `json:"user"`
	Tables map[string][]map[string]any `json:"tables"`
}

// ExportUser gathers everything stored about one person. Tables are found by
// their user_id column rather than listed, so a table added later is included
// rather than silently left behind; tutor_events hangs off tutor_sessions and
// is followed through that.
//
// The users row itself is reduced to the User struct: the token hash is the
// one thing in the database that must not travel with the data.
func (d *DB) ExportUser(id int64) (Export, error) {
	u, err := d.User(id)
	if err != nil {
		return Export{}, err
	}
	out := Export{User: u, Tables: map[string][]map[string]any{}}

	tables, err := d.tablesWith("user_id")
	if err != nil {
		return Export{}, err
	}
	for _, t := range tables {
		if t == "users" {
			continue
		}
		rows, err := d.rowsAsMaps("SELECT * FROM "+t+" WHERE user_id=? ORDER BY rowid", id)
		if err != nil {
			return Export{}, fmt.Errorf("export %s: %w", t, err)
		}
		out.Tables[t] = rows
	}
	events, err := d.rowsAsMaps(`SELECT e.* FROM tutor_events e
		JOIN tutor_sessions s ON s.id = e.session_id
		WHERE s.user_id=? ORDER BY e.session_id, e.seq`, id)
	if err != nil {
		return Export{}, fmt.Errorf("export tutor_events: %w", err)
	}
	out.Tables["tutor_events"] = events
	return out, nil
}

// tablesWith lists the tables that have a column of that name, sorted so the
// export is stable from one run to the next.
func (d *DB) tablesWith(column string) ([]string, error) {
	rows, err := d.SQL.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []string
	for _, name := range names {
		has, err := d.hasColumn(name, column)
		if err != nil {
			return nil, err
		}
		if has {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (d *DB) hasColumn(table, column string) (bool, error) {
	rows, err := d.SQL.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// rowsAsMaps runs a query and returns each row keyed by column name. Text
// comes back from the driver as []byte, which encoding/json would base64;
// it is a string here.
func (d *DB) rowsAsMaps(query string, args ...any) ([]map[string]any, error) {
	rows, err := d.SQL.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
