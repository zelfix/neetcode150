package studydb

import (
	"testing"
	"time"
)

// An attempt records the language it was written in, which is what makes a
// same-language comparison possible. There is no per-person default to record:
// the language belongs to the problem.
func TestArchivedAttemptsRecordTheirLanguage(t *testing.T) {
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
