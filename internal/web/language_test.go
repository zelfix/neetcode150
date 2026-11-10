package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/lang"
)

const testPySignature = "class Solution:\n    def twoSum(self, nums: List[int], target: int) -> List[int]:\n        \n"

func writePySignature(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "content/problems/0001-two-sum/signature.py")
	if err := os.WriteFile(path, []byte(testPySignature), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Choosing Python on Start is the whole switch: the Python files appear, the
// page speaks Python, the run goes to the Python runner, and a save is kept
// verbatim because there is no formatter for it.
func TestStartingInPythonSwitchesTheWholeWorkspace(t *testing.T) {
	srv, root, _ := testSite(t)
	writePySignature(t, root)
	h := srv.Handler()

	var ran []lang.ID
	srv.runTests = runnerFunc(func(_ context.Context, req runRequest) (testRun, error) {
		ran = append(ran, req.Lang)
		return testRun{OK: true, Ran: true, Passed: 1, Output: "Tests: 1 passed"}, nil
	})

	if rec := postJSON(t, h, "/problems/two-sum/start", map[string]string{"lang": "python"}); rec.Code != http.StatusOK {
		t.Fatalf("start in python = %d: %s", rec.Code, rec.Body)
	}
	dir := filepath.Join(root, "solutions/0001-two-sum")
	for _, name := range []string{"solution.py", "test_solution.py", "practice_cases.py"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing after a Python start: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "solution.go")); err == nil {
		t.Error("a Python start wrote solution.go")
	}

	page := get(t, h, "/problems/two-sum/solve")
	for _, want := range []string{"solutions/0001-two-sum/solution.py", `data-indent="    "`, "lang-badge", ">Python<"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("solve page lacks %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "solution.go") {
		t.Error("solve page still names the Go file")
	}

	code := "class Solution:\n    def twoSum(self, nums, target):\n        return   [0, 1]\n"
	rec := postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": code})
	if rec.Code != http.StatusOK {
		t.Fatalf("run = %d: %s", rec.Code, rec.Body)
	}
	if len(ran) != 1 || ran[0] != lang.Python {
		t.Errorf("the run went to %v, want the Python runner", ran)
	}
	if saved, _ := os.ReadFile(filepath.Join(dir, "solution.py")); string(saved) != code {
		t.Errorf("a Python save was reformatted: %q", saved)
	}
	if !strings.Contains(get(t, h, "/problems/two-sum").Body.String(), "solution.py") {
		t.Error("the problem page does not name the Python file")
	}
}

// The header selector sets a person's default; it reaches nobody else, and a
// problem that already chose keeps its choice.
func TestTheDefaultLanguageIsPerPersonAndSetFromTheHeader(t *testing.T) {
	srv, root, _ := testSite(t)
	writePySignature(t, root)
	h := srv.Handler()
	guestToken, _ := guest(t, srv, "dima")

	if rec := postJSON(t, h, "/me/language", map[string]string{"lang": "cobol"}); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown language = %d", rec.Code)
	}
	if rec := postJSON(t, h, "/me/language", map[string]string{"lang": "python"}); rec.Code != http.StatusOK {
		t.Fatalf("set language = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(get(t, h, "/").Body.String(), `<option value="python" selected>`) {
		t.Error("the header does not show the new default")
	}
	if rec := postJSON(t, h, "/problems/two-sum/start", struct{}{}); rec.Code != http.StatusOK {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "solutions/0001-two-sum/solution.py")); err != nil {
		t.Errorf("the owner's default did not apply: %v", err)
	}

	// The guest is untouched: their default is still Go.
	if rec := postJSONAs(t, h, "/problems/two-sum/start", map[string]string{}, guestToken); rec.Code != http.StatusOK {
		t.Fatalf("guest start = %d: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "_work")); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "_work", "*", "solutions", "0001-two-sum", "solution.go"))
	if len(matches) != 1 {
		t.Errorf("the guest's start did not produce a Go file: %v", matches)
	}
}

// With no Python runner on this server, a Python run is refused with the
// reason, and never silently run some other way.
func TestAPythonRunIsRefusedWhenThereIsNoRunnerForIt(t *testing.T) {
	srv, root, _ := testSite(t)
	writePySignature(t, root)
	h := srv.Handler()
	srv.unavailable = map[lang.ID]string{lang.Python: "the sandbox image nc-sandbox:py3.12 is missing"}
	called := false
	srv.runTests = runnerFunc(func(context.Context, runRequest) (testRun, error) {
		called = true
		return testRun{}, nil
	})
	if rec := postJSON(t, h, "/problems/two-sum/start", map[string]string{"lang": "python"}); rec.Code != http.StatusOK {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec := postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": "class Solution:\n    pass\n"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "nc-sandbox:py3.12 is missing") {
		t.Errorf("run = %d: %s", rec.Code, rec.Body)
	}
	if called {
		t.Error("the runner was invoked for a language it cannot run")
	}
	if page := get(t, h, "/problems/two-sum/solve"); !strings.Contains(page.Body.String(), "lang-badge") {
		t.Error("the page lost its language badge")
	}
}
