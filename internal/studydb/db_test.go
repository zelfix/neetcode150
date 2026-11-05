package studydb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, path, text string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestImportPreservesLegacyDataAndRunsOnlyOnce(t *testing.T) {
	root := t.TempDir()
	progress := `{"two-sum":{"status":"solved","confidence":4,"history":[{"confidence":4,"date":"2026-10-31T07:00:00Z","stage":2}]}}`
	put(t, root, "progress.json", progress)
	notes := "---\ntitle: Notes\n---\n\nMy own explanation.\n"
	put(t, root, "content/problems/0001-two-sum/notes.md", notes)
	put(t, root, "content/problems/0001-two-sum/hints.md", "## First hint\nA recorded hint.")
	put(t, root, "tutor/log.md", "A past session.\n")
	put(t, root, "solutions/0001-two-sum/solution.go", "working file stays unchanged")
	for n := 1; n <= 12; n++ {
		name := "2026-10-31.go.txt"
		if n > 1 {
			name = fmt.Sprintf("2026-10-31-%d.go.txt", n)
		}
		put(t, root, "solutions/0001-two-sum/attempts/"+name, fmt.Sprintf("snapshot %d", n))
		put(t, root, "solutions/0001-two-sum/attempts/"+name+".review.json", `{"date":"2026-10-31T07:00:00Z","confidence":4,"rung":0,"feedback":"Passed independently."}`)
	}
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := db.Attempts("two-sum")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 12 {
		t.Fatalf("imported %d attempts", len(attempts))
	}
	for i, a := range attempts {
		if a.Code != fmt.Sprintf("snapshot %d", i+1) {
			t.Fatalf("wrong import order at %d: %s", i, a.Code)
		}
	}
	if text, err := db.Document("two-sum", "notes"); err != nil || text != notes {
		t.Fatalf("notes changed: %q, %v", text, err)
	}
	var raw string
	if err := db.SQL.QueryRow("SELECT entry FROM progress WHERE slug='two-sum'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var imported map[string]any
	if json.Unmarshal([]byte(raw), &imported) != nil || imported["confidence"] != float64(4) {
		t.Fatal("progress changed")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The source is a backup, not a competing database after migration.
	put(t, root, "progress.json", `{"two-sum":{"status":"new"}}`)
	put(t, root, "content/problems/0001-two-sum/notes.md", "stale legacy edit")
	db, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if text, _ := db.Document("two-sum", "notes"); text != notes {
		t.Fatal("legacy files reimported")
	}
	var count int
	db.SQL.QueryRow("SELECT count(*) FROM attempts").Scan(&count)
	if count != 12 {
		t.Fatal("duplicate import")
	}
	data, _ := os.ReadFile(filepath.Join(root, "solutions/0001-two-sum/solution.go"))
	if string(data) != "working file stays unchanged" {
		t.Fatal("working solution touched")
	}
}

func TestInvalidImportRollsBackAndCanBeRetried(t *testing.T) {
	root := t.TempDir()
	put(t, root, "progress.json", `{"two-sum":{"status":"solved"}}`)
	put(t, root, "content/problems/0001-two-sum/notes.md", "keep these notes")
	path := "solutions/0001-two-sum/attempts/2026-10-31.go.txt"
	put(t, root, path, "keep this snapshot")
	put(t, root, path+".review.json", `{"confidence":`)
	if db, err := Open(root); err == nil {
		db.Close()
		t.Fatal("corrupt report accepted")
	}
	put(t, root, path+".review.json", `{"confidence":4,"rung":0}`)
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, want := range map[string]int{"progress": 1, "documents": 1, "attempts": 1} {
		var n int
		if err := db.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s has %d rows, want %d (%v)", table, n, want, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(root, path)); string(data) != "keep this snapshot" {
		t.Fatal("source was changed")
	}
}
func TestNewerDatabaseVersionIsRejected(t *testing.T) {
	root := t.TempDir()
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := Open(root); err == nil {
		db.Close()
		t.Fatal("newer schema accepted")
	} else if !strings.Contains(err.Error(), "newer") {
		t.Fatal(err)
	}
}

func TestVersionOneWithoutSubmissionKeyUpgradesWithoutLosingData(t *testing.T) {
	root := t.TempDir()
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := db.Archive("two-sum", "existing snapshot", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveDocument("two-sum", "notes", "existing notes"); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePending("two-sum", []byte(`{"Token":"pending-token","Code":"pending snapshot"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO progress VALUES('two-sum','{"status":"attempted","confidence":4}'); DROP INDEX attempts_submission_key; ALTER TABLE attempts DROP COLUMN submission_key; PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(root)
	if err != nil {
		t.Fatalf("upgrade v1: %v", err)
	}
	var version int
	db.SQL.QueryRow("PRAGMA user_version").Scan(&version)
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}
	attempt, err := db.Attempt(ref)
	if err != nil || attempt.Code != "existing snapshot" {
		t.Fatal("existing attempt lost")
	}
	if text, _ := db.Document("two-sum", "notes"); text != "existing notes" {
		t.Fatal("notes lost")
	}
	pending, err := db.Pending()
	if err != nil || !strings.Contains(string(pending["two-sum"]), "pending snapshot") {
		t.Fatal("pending submission lost")
	}
	var raw string
	if err := db.SQL.QueryRow("SELECT entry FROM progress WHERE slug='two-sum'").Scan(&raw); err != nil || !strings.Contains(raw, "attempted") {
		t.Fatal("progress lost")
	}
	insert := func() error {
		return db.Write(func(tx *sql.Tx) error {
			_, err := InsertAttempt(tx, "two-sum", "new snapshot", nil, time.Now(), "submission-token")
			return err
		})
	}
	if err := insert(); err != nil {
		t.Fatalf("new submission failed: %v", err)
	}
	if err := insert(); err == nil {
		t.Fatal("duplicate submission key was accepted")
	}
	db.Close()
	db, err = Open(root)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer db.Close()
	var count int
	db.SQL.QueryRow("SELECT count(*) FROM attempts").Scan(&count)
	if count != 2 {
		t.Fatalf("got %d attempts, want 2", count)
	}
}

func TestVersionOneWithSubmissionKeyAlsoUpgrades(t *testing.T) {
	root := t.TempDir()
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(root)
	if err != nil {
		t.Fatalf("upgrade v1 with column already present: %v", err)
	}
	defer db.Close()
	var version int
	db.SQL.QueryRow("PRAGMA user_version").Scan(&version)
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}
}
