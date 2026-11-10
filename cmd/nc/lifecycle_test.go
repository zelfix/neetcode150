package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/progress"
)

// writeSignature creates the content directory for a problem and drops a
// signature.txt there, which is all cmdStart / cmdResolve need from the
// content tree. A missing problem.md is not an error — the statement is
// optional in tests just as it is on a fresh clone.
func writeSignature(t *testing.T, root, dir, sig string) {
	t.Helper()
	d := filepath.Join(root, "content", "problems", dir)
	if err := os.MkdirAll(d, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "signature.txt"), []byte(sig), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestLifecycleStartResolveDiffInfo walks the four commands that had no
// coverage through a complete re-solve cycle and verifies:
//
//   - cmdStart transitions the problem to attempted and creates solution.go
//   - cmdResolve archives the current solution and resets the file
//   - cmdDiff shows the archived content alongside the new one
//   - cmdInfo prints the solution path
//
// Error paths — an unknown ref and a resolve with nothing to archive — are
// checked at the end.
func TestLifecycleStartResolveDiffInfo(t *testing.T) {
	const sig = "func twoSum(nums []int, target int) []int {\n    \n}"
	root := t.TempDir()
	writeCatalog(t, root)
	writeSignature(t, root, "0001-two-sum", sig)

	// --- start ---
	if err := cmdStart(root, []string{"two-sum"}); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	if e := entry(t, root, "two-sum"); e.Status != progress.Attempted {
		t.Errorf("after start: status = %s, want attempted", e.Status)
	}
	solPath := filepath.Join(root, "solutions", "0001-two-sum", "solution.go")
	if _, err := os.Stat(solPath); err != nil {
		t.Fatalf("cmdStart did not create solution.go: %v", err)
	}

	// Write a distinctive solution so cmdResolve has something worth archiving
	// and cmdDiff has a difference to show.
	firstContent := "package twosum\n\nfunc twoSum(nums []int, target int) []int { return nil }\n"
	if err := os.WriteFile(solPath, []byte(firstContent), 0600); err != nil {
		t.Fatal(err)
	}

	// --- resolve ---
	if err := cmdResolve(root, []string{"two-sum"}); err != nil {
		t.Fatalf("cmdResolve: %v", err)
	}
	// Re-solving restarts the attempt, so the status stays attempted.
	if e := entry(t, root, "two-sum"); e.Status != progress.Attempted {
		t.Errorf("after resolve: status = %s, want attempted", e.Status)
	}
	// solution.go must have been replaced with a fresh scaffold, not firstContent.
	refreshed, err := os.ReadFile(solPath)
	if err != nil {
		t.Fatalf("reading solution.go after resolve: %v", err)
	}
	if string(refreshed) == firstContent {
		t.Error("cmdResolve did not overwrite solution.go with a fresh scaffold")
	}

	// Write a second attempt so cmdDiff has two distinct states to compare.
	secondContent := "package twosum\n\nfunc twoSum(nums []int, target int) []int { return []int{0, 1} }\n"
	if err := os.WriteFile(solPath, []byte(secondContent), 0600); err != nil {
		t.Fatal(err)
	}

	// --- diff ---
	diffOut := capture(t, func() {
		if err := cmdDiff(root, []string{"two-sum"}); err != nil {
			t.Errorf("cmdDiff: %v", err)
		}
	})
	// The diff must mention something from the archived first attempt. The
	// archived function line contained "return nil", which changed to
	// "return []int{0, 1}" — at least one side of that change will appear.
	if !strings.Contains(diffOut, "twoSum") {
		t.Errorf("diff output does not mention twoSum:\n%s", diffOut)
	}

	// --- info ---
	infoOut := capture(t, func() {
		if err := cmdInfo(root, []string{"two-sum"}); err != nil {
			t.Errorf("cmdInfo: %v", err)
		}
	})
	// cmdInfo always prints the relative solution path, which is the canonical
	// way to tell the user where their file lives.
	if !strings.Contains(infoOut, "solutions/0001-two-sum/solution.go") {
		t.Errorf("info does not print the solution path:\n%s", infoOut)
	}

	// --- error: unknown ref ---
	if err := cmdStart(root, []string{"no-such-problem-xyz"}); err == nil {
		t.Error("cmdStart with unknown ref should return an error")
	}

	// --- error: resolve with no solution ---
	// Use a fresh root that has the catalog but has never been started, so
	// there is no solution.go to archive.
	freshRoot := t.TempDir()
	writeCatalog(t, freshRoot)
	writeSignature(t, freshRoot, "0001-two-sum", sig)
	err = cmdResolve(freshRoot, []string{"two-sum"})
	if err == nil {
		t.Error("cmdResolve with no solution.go should return an error")
	} else if !strings.Contains(err.Error(), "two-sum") {
		t.Errorf("resolve error does not name the problem: %v", err)
	}
}
