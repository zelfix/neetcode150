package studydb

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An export is one person's rows and nobody else's, and never the token hash.
func TestExportUserKeepsToOnePersonAndLeavesTheTokenOut(t *testing.T) {
	root := t.TempDir()
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	guest, err := db.CreateUser("dima", HashToken("dima-link"), 5, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []struct {
		scope *Scope
		notes string
	}{
		{db.Owner(), "owner notes"},
		{db.For(guest.ID), "guest notes"},
	} {
		if err := who.scope.SaveDocument("two-sum", "notes", who.notes); err != nil {
			t.Fatal(err)
		}
		if _, err := who.scope.Archive("two-sum", "package main\n", now); err != nil {
			t.Fatal(err)
		}
	}

	ex, err := db.ExportUser(guest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ex.User.Name != "dima" {
		t.Errorf("exported user is %q", ex.User.Name)
	}
	for _, table := range []string{"documents", "attempts", "progress", "tutor_sessions", "tutor_events"} {
		if _, ok := ex.Tables[table]; !ok {
			t.Errorf("export has no %s table", table)
		}
	}
	if _, ok := ex.Tables["users"]; ok {
		t.Error("the users table, with its token hashes, was exported")
	}
	if docs := ex.Tables["documents"]; len(docs) != 1 || docs[0]["text"] != "guest notes" {
		t.Errorf("documents = %v, want the guest's one row", docs)
	}
	if attempts := ex.Tables["attempts"]; len(attempts) != 1 {
		t.Errorf("attempts = %v, want one", attempts)
	}

	b, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), HashToken("dima-link")) || strings.Contains(string(b), "token_hash") {
		t.Error("the token hash reached the JSON")
	}
}
