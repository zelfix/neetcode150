package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
)

func TestDocumentCommandsAndConsistentBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]catalog.Problem{{ID: 1, Slug: "two-sum", Title: "Two Sum", Dir: "0001-two-sum"}})
	if err := os.WriteFile(catalogPath(root), data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "entry.md")
	if err := os.WriteFile(path, []byte("My own notes.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdDocument(root, "notes", []string{"two-sum", "--file", path}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDocument(root, "log", []string{"--append-file", path}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDocument(root, "log", []string{"--append-file", path}); err != nil {
		t.Fatal(err)
	}
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if text, _ := db.Document("two-sum", "notes"); text != "My own notes.\n" {
		t.Fatalf("notes: %q", text)
	}
	if text, _ := db.Document("", "log"); text != "My own notes.\nMy own notes.\n" {
		t.Fatalf("append: %q", text)
	}
	backup := filepath.Join(t.TempDir(), "backup.sqlite3")
	if err := cmdDatabase(root, []string{backup}, true); err != nil {
		t.Fatal(err)
	}
	if err := cmdDatabase(root, []string{backup}, true); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	// Open the snapshot without consulting the source directory or a WAL file.
	copiedRoot := t.TempDir()
	backupBytes, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copiedRoot, studydb.Filename), backupBytes, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := studydb.Open(copiedRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if text, _ := restored.Document("two-sum", "notes"); text != "My own notes.\n" {
		t.Fatalf("backup lost notes: %q", text)
	}
}

// writeCatalog lays down the minimum content tree the commands need to resolve
// a problem reference.
func writeCatalog(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal([]catalog.Problem{
		{ID: 1, Slug: "two-sum", Title: "Two Sum", Dir: "0001-two-sum", Order: 0},
		{ID: 242, Slug: "valid-anagram", Title: "Valid Anagram", Dir: "0242-valid-anagram", Order: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath(root), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// entry reads one problem's state back through a fresh store, as a separate
// `nc` invocation would.
func entry(t *testing.T, root, slug string) progress.Entry {
	t.Helper()
	_, prog, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	defer prog.Close()
	return prog.Get(slug)
}

// A rating drives the whole schedule, so a mistyped one has to be recoverable.
func TestRateUndoDropsTheLastRatingAndAmendCorrectsIt(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)

	if err := cmdRate(root, []string{"two-sum", "4", "--rung", "0"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRate(root, []string{"two-sum", "5"}); err != nil {
		t.Fatal(err)
	}
	if e := entry(t, root, "two-sum"); len(e.History) != 2 {
		t.Fatalf("history has %d ratings, want 2", len(e.History))
	}

	if err := cmdRate(root, []string{"two-sum", "--undo"}); err != nil {
		t.Fatal(err)
	}
	e := entry(t, root, "two-sum")
	if len(e.History) != 1 {
		t.Errorf("after undo history has %d ratings, want 1", len(e.History))
	}
	if e.Confidence != 4 {
		t.Errorf("after undo last rating is %d, want the 4 back", e.Confidence)
	}

	if err := cmdRate(root, []string{"two-sum", "--amend", "2"}); err != nil {
		t.Fatal(err)
	}
	e = entry(t, root, "two-sum")
	if len(e.History) != 1 {
		t.Errorf("amend changed the history length to %d, want 1", len(e.History))
	}
	if e.Confidence != 2 || e.Status != progress.Shaky {
		t.Errorf("after amend: rated %d, status %s — want 2 and shaky", e.Confidence, e.Status)
	}

	// Undoing the only rating leaves nothing to undo.
	if err := cmdRate(root, []string{"two-sum", "--undo"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRate(root, []string{"two-sum", "--undo"}); err != nil {
		t.Fatalf("a second undo should report nothing to do, not fail: %v", err)
	}
}

func TestRateRejectsAnAmendWithoutARating(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)
	if err := cmdRate(root, []string{"two-sum", "--amend"}); err == nil {
		t.Error("--amend was accepted with no rating")
	}
}

func TestReviewListsTheDueQueueUpToTheLimit(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)

	// The commands all work off the wall clock, so the only way to have
	// something genuinely overdue is to rate it in the past through the store.
	func() {
		_, prog, err := load(root)
		if err != nil {
			t.Fatal(err)
		}
		defer prog.Close()
		past := time.Now().AddDate(0, 0, -40)
		if _, err := prog.RateHinted("two-sum", 4, 0, past); err != nil {
			t.Fatal(err)
		}
		if _, err := prog.RateHinted("valid-anagram", 4, 0, past.AddDate(0, 0, 20)); err != nil {
			t.Fatal(err)
		}
	}()

	cat, err := catalog.Load(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	_, prog, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	defer prog.Close()
	now := time.Now()
	if due := prog.DueReview(cat.Problems, now); len(due) != 2 {
		t.Fatalf("%d problems due, want both", len(due))
	}
	queue := prog.ReviewQueue(cat.Problems, now, 0)
	if queue[0].Slug != "two-sum" {
		t.Errorf("queue starts with %q, want the longest overdue two-sum", queue[0].Slug)
	}
	if capped := prog.ReviewQueue(cat.Problems, now, 1); len(capped) != 1 {
		t.Errorf("limit 1 returned %d", len(capped))
	}

	for _, args := range [][]string{nil, {"--limit", "1"}, {"--limit", "0"}} {
		if err := cmdReview(root, args); err != nil {
			t.Fatalf("nc review %v: %v", args, err)
		}
	}
}

func TestWeakReportsWhatTheStoreRanks(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)
	// Nothing rated yet: the command explains rather than failing.
	if err := cmdWeak(root, nil); err != nil {
		t.Fatal(err)
	}
	if err := cmdRate(root, []string{"two-sum", "2", "--rung", "5"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdWeak(root, []string{"-n", "5"}); err != nil {
		t.Fatal(err)
	}
	_, prog, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	defer prog.Close()
	cat, err := catalog.Load(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	weak := prog.Weakest(cat.Problems, 0)
	if len(weak) != 1 || weak[0].Problem.Slug != "two-sum" {
		t.Fatalf("Weakest returned %d problems, want just two-sum", len(weak))
	}
}
