package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/studydb"
)

// goModule writes a throwaway module so runGoTest has something real to run.
// The handler tests use an injected runner; this file is the one place the
// actual subprocess is exercised, because the classification of its three
// outcomes is the part that is easy to get wrong.
func goModule(t *testing.T, pkgDir, solution, test string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, pkgDir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(full, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module scratch\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("solution.go", solution)
	write("solution_test.go", test)
	return root
}

func TestRunGoTestClassifiesItsOutcomes(t *testing.T) {
	const pkgDir = "solutions/0001-two-sum"

	t.Run("a passing package", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc add(a, b int) int { return a + b }\n",
			`package twosum

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdd(t *testing.T) {
	if add(1, 2) != 3 {
		t.Fatal("no")
	}
}

func TestRunnerKeepsGoScratchDirsInWorkspace(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"GOCACHE": filepath.Join(root, ".nc-cache", "go-build"),
		"GOTMPDIR": filepath.Join(root, ".nc-cache", "tmp"),
		"TMPDIR": filepath.Join(root, ".nc-cache", "tmp"),
	}
	// On macOS the temporary root is reached through a symlink, so the two
	// sides have to be resolved before they can be compared.
	resolve := func(path string) string {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real
		}
		return path
	}
	for name, path := range want {
		if got := resolve(os.Getenv(name)); got != resolve(path) {
			t.Errorf("%s = %q, want %q", name, got, resolve(path))
		}
	}
}
`)
		run, err := runGoTest(t.Context(), root, pkgDir)
		if err != nil {
			t.Fatalf("err = %v, output:\n%s", err, run.Output)
		}
		if !run.OK {
			t.Errorf("OK = false on a passing package, output:\n%s", run.Output)
		}
	})

	t.Run("a failing test is not a runner error", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc add(a, b int) int { return a - b }\n",
			"package twosum\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif add(1, 2) != 3 {\n\t\tt.Fatal(\"wrong answer\")\n\t}\n}\n")
		run, err := runGoTest(t.Context(), root, pkgDir)
		if err != nil {
			t.Fatalf("a failing test reported a runner error: %v", err)
		}
		if run.OK {
			t.Error("OK = true on a failing test")
		}
		if !strings.Contains(run.Output, "FAIL") || !strings.Contains(run.Output, "wrong answer") {
			t.Errorf("the failure is not in the output:\n%s", run.Output)
		}
		// The broken test is named for the record, not just counted.
		if len(run.Cases) != 1 || run.Cases[0].Name != "TestAdd" {
			t.Errorf("failed cases = %+v, want the one TestAdd", run.Cases)
		}
	})

	t.Run("source that does not compile", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc add(a, b int) int { return a + }\n",
			"package twosum\n")
		run, err := runGoTest(t.Context(), root, pkgDir)
		// A compile error is still `go test` having its say, so it reports as a
		// failure with output rather than as a runner error — the user needs to
		// read the compiler, not a wrapper's summary of it.
		if err != nil {
			t.Fatalf("a compile error reported a runner error: %v", err)
		}
		if run.OK {
			t.Error("OK = true on source that does not compile")
		}
		if run.Ran {
			t.Error("Ran = true although nothing compiled")
		}
		if !strings.Contains(run.Output, "syntax error") && !strings.Contains(run.Output, "expected") {
			t.Errorf("the compiler's complaint is not in the output:\n%s", run.Output)
		}
	})

	t.Run("a package that is not there", func(t *testing.T) {
		root := goModule(t, pkgDir, "package twosum\n", "package twosum\n")
		run, err := runGoTest(t.Context(), root, "solutions/0999-nope")
		if err != nil {
			t.Fatalf("a missing package reported a runner error: %v", err)
		}
		if run.OK {
			t.Error("OK = true for a package that does not exist")
		}
	})

	t.Run("a cancelled run reports why", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc spin() {\n\tfor {\n\t}\n}\n",
			"package twosum\n\nimport \"testing\"\n\nfunc TestSpin(t *testing.T) {\n\tspin()\n}\n")
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		run, err := runGoTest(ctx, root, pkgDir)
		if err == nil {
			t.Fatalf("an endless test was reported as a result, OK = %v, output:\n%s", run.OK, run.Output)
		}
		if run.OK {
			t.Error("OK = true on a cut-off run")
		}
		if !strings.Contains(err.Error(), "cut off") {
			t.Errorf("err = %v, want it to say the run was cut off", err)
		}
	})
}

func TestFormatGo(t *testing.T) {
	t.Run("formats what parses", func(t *testing.T) {
		got, err := formatGo("package x\nfunc f( a int )int{return a}\n")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(got, "func f(a int) int { return a }") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("keeps what does not parse", func(t *testing.T) {
		src := "package x\nfunc f() {\n"
		got, err := formatGo(src)
		if err == nil {
			t.Fatal("no error on source that does not parse")
		}
		if got != src {
			t.Errorf("got %q, want the input back unchanged", got)
		}
	})
}

func TestRunGoTestRejectsMissingAndSkippedTests(t *testing.T) {
	for name, tests := range map[string]string{
		"missing": "package demo\n",
		"skipped": `package demo
import "testing"
func TestDraft(t *testing.T){t.Skip("add cases")}
`,
	} {
		t.Run(name, func(t *testing.T) {
			root := goModule(t, "solutions/demo", "package demo\n", tests)
			run, err := runGoTest(t.Context(), root, "solutions/demo")
			if run.OK || err == nil || !strings.Contains(err.Error(), "no tests ran") {
				t.Fatalf("false pass: %v, %v, %s", run.OK, err, run.Output)
			}
			if name == "skipped" && !strings.Contains(run.Output, "1 skipped") {
				t.Fatalf("missing skip count: %s", run.Output)
			}
		})
	}
}

func TestReadTestEventsShowsCasesWithoutCountingTheParentTwice(t *testing.T) {
	raw := []byte(`{"Action":"output","Output":"=== RUN TestSuite/case_one\n"}
{"Action":"pass","Test":"TestSuite/case_one"}
{"Action":"fail","Test":"TestSuite/case_two"}
{"Action":"skip","Test":"TestSuite/case_three"}
{"Action":"fail","Test":"TestSuite"}
{"Action":"output","Output":"wrong answer\n"}`)
	run := readTestEvents(raw)
	if !run.Ran || !strings.Contains(run.Output, "1 passed, 1 failed, 1 skipped") ||
		!strings.Contains(run.Output, "wrong answer") {
		t.Fatalf("unexpected report: %s", run.Output)
	}
	// The parent failed only because a child did, so it is not a case of its own.
	if len(run.Cases) != 1 || run.Cases[0].Name != "case two" {
		t.Fatalf("failed cases = %+v, want just the one child", run.Cases)
	}
}

// Go renders a subtest name with its spaces replaced by underscores, and the
// fixtures are tagged example or edge. Both have to survive the round trip or
// the counts are filed under names nobody recognises.
func TestNamedCaseRecoversTheFixtureNameAndKind(t *testing.T) {
	cases := map[string]studydb.FailedCase{
		"TestPracticeCases/edge/empty_input":  {Kind: "edge", Name: "empty input"},
		"TestPracticeCases/example/example_1": {Kind: "example", Name: "example 1"},
		"TestPracticeCases/edge/single":       {Kind: "edge", Name: "single"},
		"TestSolution/some_table_case":        {Kind: "other", Name: "some table case"},
		"TestSolution":                        {Kind: "other", Name: "TestSolution"},
	}
	for test, want := range cases {
		if got := namedCase(test); got != want {
			t.Errorf("namedCase(%q) = %+v, want %+v", test, got, want)
		}
	}
}
