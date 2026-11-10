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

// The language a problem is worked in is decided once, here, and everything
// after reads it back: a Python start writes Python files and records the
// choice, while the owner's default covers problems that never chose.
func TestStartInPythonRecordsTheChoiceAndWritesPythonFiles(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root)
	writeSignatures(t, root)

	if err := cmdStart(root, []string{"two-sum", "--lang", "python"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"solution.py", "test_solution.py", "practice_cases.py"} {
		if _, err := os.Stat(filepath.Join(root, "solutions/0001-two-sum", name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "solutions/0001-two-sum/solution.go")); err == nil {
		t.Error("a Python start wrote a Go file")
	}
	if e := entry(t, root, "two-sum"); e.Lang != "python" || e.Status != progress.Attempted {
		t.Errorf("entry = lang %q status %s", e.Lang, e.Status)
	}

	// The owner's default applies to a problem with no choice of its own.
	if err := cmdUser(root, []string{"lang", "owner", "python"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdStart(root, []string{"valid-anagram"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "solutions/0242-valid-anagram/solution.py")); err != nil {
		t.Errorf("the default language was not applied: %v", err)
	}
	if e := entry(t, root, "valid-anagram"); e.Lang != "" {
		t.Errorf("a default start recorded a per-problem language %q", e.Lang)
	}
	if err := cmdUser(root, []string{"lang", "owner", "cobol"}); err == nil {
		t.Error("an unknown language was accepted")
	}
	// Info names the language, and a Python resolve archives a Python attempt.
	out := capture(t, func() {
		if err := cmdInfo(root, []string{"two-sum"}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "language   Python") || !strings.Contains(out, "solution.py") {
		t.Errorf("info did not name the language:\n%s", out)
	}
	if err := cmdResolve(root, []string{"two-sum"}); err != nil {
		t.Fatal(err)
	}
	capture(t, func() {
		if err := cmdDiff(root, []string{"two-sum"}); err != nil {
			t.Errorf("diff after a python resolve: %v", err)
		}
	})
}

func writeSignatures(t *testing.T, root string) {
	t.Helper()
	for dir, files := range map[string]map[string]string{
		"0001-two-sum": {
			"signature.txt": "func twoSum(nums []int, target int) []int {\n    \n}\n",
			"signature.py":  "class Solution:\n    def twoSum(self, nums: List[int], target: int) -> List[int]:\n        \n",
		},
		"0242-valid-anagram": {
			"signature.txt": "func isAnagram(s string, t string) bool {\n    \n}\n",
			"signature.py":  "class Solution:\n    def isAnagram(self, s: str, t: str) -> bool:\n        \n",
		},
	} {
		full := filepath.Join(root, "content/problems", dir)
		if err := os.MkdirAll(full, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(full, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
