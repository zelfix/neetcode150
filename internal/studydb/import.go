package studydb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The marker and all imported records commit together. Legacy files are never
// changed, and subsequent starts never read them as live application state.
func (d *DB) importLegacy() error {
	return d.Write(func(tx *sql.Tx) error {
		var marker string
		err := tx.QueryRow("SELECT value FROM metadata WHERE key='legacy_import_v1'").Scan(&marker)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		counts := map[string]int{"progress": 0, "documents": 0, "attempts": 0}
		read := func(path string) ([]byte, error) {
			b, e := os.ReadFile(path)
			if os.IsNotExist(e) {
				return nil, nil
			}
			return b, e
		}
		progress, err := read(filepath.Join(d.Root, "progress.json"))
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(string(progress))) > 0 {
			var entries map[string]json.RawMessage
			if err := json.Unmarshal(progress, &entries); err != nil {
				return fmt.Errorf("progress.json: %w", err)
			}
			for slug, entry := range entries {
				if len(entry) == 0 || entry[0] != '{' {
					return fmt.Errorf("invalid progress entry for %s", slug)
				}
				if _, err := tx.Exec("INSERT INTO progress(user_id,slug,entry) VALUES(?,?,?)", OwnerID, slug, string(entry)); err != nil {
					return err
				}
				counts["progress"]++
			}
		}
		docs, err := filepath.Glob(filepath.Join(d.Root, "content/problems/*"))
		if err != nil {
			return err
		}
		for _, dir := range docs {
			info, err := os.Stat(dir)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				continue
			}
			_, slug, ok := strings.Cut(filepath.Base(dir), "-")
			if !ok {
				continue
			}
			for _, kind := range []string{"notes", "hints"} {
				path := filepath.Join(dir, kind+".md")
				if _, err := os.Stat(path); os.IsNotExist(err) {
					continue
				} else if err != nil {
					return err
				}
				text, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if _, err := tx.Exec("INSERT INTO documents(user_id,slug,kind,text) VALUES(?,?,?,?)", OwnerID, slug, kind, string(text)); err != nil {
					return err
				}
				counts["documents"]++
			}
		}
		if text, err := read(filepath.Join(d.Root, "tutor/log.md")); err != nil {
			return err
		} else if text != nil {
			if _, err := tx.Exec("INSERT INTO documents(user_id,slug,kind,text) VALUES(?,'','log',?)", OwnerID, string(text)); err != nil {
				return err
			}
			counts["documents"]++
		}
		paths, err := filepath.Glob(filepath.Join(d.Root, "solutions/*/attempts/*.go.txt"))
		if err != nil {
			return err
		}
		sort.Slice(paths, func(i, j int) bool {
			di, ni := legacyOrder(paths[i])
			dj, nj := legacyOrder(paths[j])
			if di != dj {
				return di < dj
			}
			if ni != nj {
				return ni < nj
			}
			return paths[i] < paths[j]
		})
		for _, path := range paths {
			code, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			dirname := filepath.Base(filepath.Dir(filepath.Dir(path)))
			_, slug, ok := strings.Cut(dirname, "-")
			if !ok {
				return fmt.Errorf("invalid archive directory %s", dirname)
			}
			date, _ := legacyOrder(path)
			created, err := time.Parse("2006-01-02", date)
			if err != nil {
				return fmt.Errorf("archive %s: %w", path, err)
			}
			report, err := read(path + ".review.json")
			if err != nil {
				return err
			}
			var review any
			if report != nil {
				var parsed struct {
					Date       time.Time `json:"date"`
					Confidence int       `json:"confidence"`
					Rung       int       `json:"rung"`
				}
				if err := json.Unmarshal(report, &parsed); err != nil {
					return fmt.Errorf("%s.review.json: %w", path, err)
				}
				if parsed.Confidence < 1 || parsed.Confidence > 5 || parsed.Rung < 0 || parsed.Rung > 5 {
					return fmt.Errorf("invalid rating in %s.review.json", path)
				}
				if !parsed.Date.IsZero() {
					created = parsed.Date
				}
				review = string(report)
			}
			rel, err := filepath.Rel(d.Root, path)
			if err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO attempts(user_id,slug,created_at,code,review,legacy_path) VALUES(?,?,?,?,?,?)", OwnerID, slug, created.Format(time.RFC3339Nano), string(code), review, filepath.ToSlash(rel)); err != nil {
				return err
			}
			var stored string
			if err := tx.QueryRow("SELECT code FROM attempts WHERE user_id=? AND legacy_path=?", OwnerID, filepath.ToSlash(rel)).Scan(&stored); err != nil {
				return err
			}
			if stored != string(code) {
				return fmt.Errorf("archive verification failed: %s", path)
			}
			counts["attempts"]++
		}
		for _, table := range []string{"progress", "documents", "attempts"} {
			var n int
			if err := tx.QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", OwnerID).Scan(&n); err != nil {
				return err
			}
			if n != counts[table] {
				return fmt.Errorf("import verification for %s: %d != %d", table, n, counts[table])
			}
		}
		audit, _ := json.Marshal(counts)
		_, err = tx.Exec("INSERT INTO metadata VALUES('legacy_import_v1',?)", string(audit))
		return err
	})
}
func legacyOrder(path string) (string, int) {
	name := strings.TrimSuffix(filepath.Base(path), ".go.txt")
	if i := strings.LastIndex(name, "-"); i >= 10 {
		if n, e := strconv.Atoi(name[i+1:]); e == nil {
			return name[:i], n
		}
	}
	return name, 1
}
