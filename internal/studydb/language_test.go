package studydb

import (
	"testing"
	"time"
)

// Everyone who existed before languages did is a Go user, and nothing about
// their rows changes; the column is what makes a later choice possible.
func TestLanguageDefaultsToGoAndCanBeSet(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.SQL.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
	owner, err := db.User(OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Language != "go" {
		t.Errorf("owner language = %q, want go", owner.Language)
	}
	if err := db.SetUserLanguage(OwnerID, "python"); err != nil {
		t.Fatal(err)
	}
	if owner, _ = db.User(OwnerID); owner.Language != "python" {
		t.Errorf("after SetUserLanguage: %q", owner.Language)
	}

	now := time.Now()
	for _, l := range []string{"", "python"} {
		if _, err := db.Owner().Archive("two-sum", l, "code", now); err != nil {
			t.Fatal(err)
		}
	}
	attempts, err := db.Owner().Attempts("two-sum")
	if err != nil || len(attempts) != 2 {
		t.Fatalf("attempts: %v %v", attempts, err)
	}
	if attempts[0].Language != "go" || attempts[1].Language != "python" {
		t.Errorf("attempt languages = %q, %q", attempts[0].Language, attempts[1].Language)
	}

	// The step is re-runnable, like every rung above it.
	if err := db.Write(migrateV5); err != nil {
		t.Errorf("migrateV5 is not idempotent: %v", err)
	}
}
