package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
)

// A problem that has been started but not finished is still unsolved, and
// the pool `nc drill` draws from has to say so rather than report it empty.
func TestDrillCountsAnAttemptedProblemAsUnsolved(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)
	if err := cmdStatus(root, []string{"two-sum", "attempted"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdStatus(root, []string{"valid-anagram", "solved"}); err != nil {
		t.Fatal(err)
	}
	if e := entry(t, root, "two-sum"); e.Status != progress.Attempted {
		t.Fatalf("status = %s, want attempted", e.Status)
	}
	if err := cmdDrill(root, nil); err != nil {
		t.Errorf("drill with one attempted problem: %v", err)
	}
	if err := cmdDrill(root, []string{"--solved"}); err != nil {
		t.Errorf("drill --solved with one solved problem: %v", err)
	}
}

// Reissuing a link must not touch the budget unless --budget was typed, and
// when it was, it has to take effect rather than be parsed and dropped.
func TestUserLinkChangesTheBudgetOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)
	budgetOf := func() float64 {
		t.Helper()
		db, err := studydb.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		users, err := db.Users()
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range users {
			if u.Name == "dima" {
				return u.BudgetUSD
			}
		}
		t.Fatal("dima was not created")
		return 0
	}

	if err := cmdUser(root, []string{"add", "dima", "--budget", "3"}); err != nil {
		t.Fatal(err)
	}
	if got := budgetOf(); got != 3 {
		t.Fatalf("after add: budget %v, want 3", got)
	}
	if err := cmdUser(root, []string{"link", "dima"}); err != nil {
		t.Fatal(err)
	}
	if got := budgetOf(); got != 3 {
		t.Errorf("a plain link changed the budget to %v", got)
	}
	if err := cmdUser(root, []string{"link", "dima", "--budget", "9"}); err != nil {
		t.Fatal(err)
	}
	if got := budgetOf(); got != 9 {
		t.Errorf("link --budget 9 left the budget at %v", got)
	}
	if err := cmdUser(root, []string{"link", "dima", "--budget", "-1"}); err == nil {
		t.Error("a negative budget was accepted")
	}
}

// Flags such as --rung and --undo are only discoverable from the CLI if the
// command itself answers --help.
func TestEveryCommandAnswersHelp(t *testing.T) {
	for _, cmd := range []string{"rate", "drill", "serve", "user", "fetch", "weak"} {
		out := capture(t, func() {
			if err := run([]string{cmd, "--help"}); err != nil {
				t.Errorf("nc %s --help: %v", cmd, err)
			}
		})
		if !strings.Contains(out, "nc "+cmd) {
			t.Errorf("nc %s --help printed %q", cmd, out)
		}
	}
	if err := run([]string{"nonsense", "--help"}); err == nil {
		t.Error("help for an unknown command did not fail")
	}
	// A flag set's own ErrHelp, from `nc rate two-sum --help`, is finished the
	// same way rather than reported as a failure.
	if err := commandHelpOnErr("rate", flag.ErrHelp); err != nil {
		t.Errorf("ErrHelp was not turned into help: %v", err)
	}
}

func capture(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	f()
	w.Close()
	os.Stdout = prev
	select {
	case s := <-done:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("capture timed out")
		return ""
	}
}

// An export is the one way a person on a shared instance gets their own work
// back, so it has to be theirs alone and must not clobber an earlier one.
func TestExportWritesOnePersonsDataOnce(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)
	if err := cmdUser(root, []string{"add", "dima"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdStatus(root, []string{"two-sum", "solved"}); err != nil {
		t.Fatal(err) // the owner's row, which must stay out of dima's export
	}
	out := filepath.Join(t.TempDir(), "dima.json")
	if err := cmdExport(root, []string{"dima", out}); err != nil {
		t.Fatal(err)
	}
	var ex studydb.Export
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &ex); err != nil {
		t.Fatal(err)
	}
	if ex.User.Name != "dima" || len(ex.Tables["progress"]) != 0 {
		t.Errorf("export = user %q with %d progress rows, want dima with none", ex.User.Name, len(ex.Tables["progress"]))
	}
	if err := cmdExport(root, []string{"dima", out}); err == nil {
		t.Error("a second export overwrote the first")
	}
	if err := cmdExport(root, []string{"nobody", filepath.Join(t.TempDir(), "x.json")}); err == nil {
		t.Error("an unknown name was exported")
	}
}
