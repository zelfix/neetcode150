package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			"package twosum\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif add(1, 2) != 3 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
		out, ok, err := runGoTest(t.Context(), root, pkgDir)
		if err != nil {
			t.Fatalf("err = %v, output:\n%s", err, out)
		}
		if !ok {
			t.Errorf("ok = false on a passing package, output:\n%s", out)
		}
	})

	t.Run("a failing test is not a runner error", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc add(a, b int) int { return a - b }\n",
			"package twosum\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif add(1, 2) != 3 {\n\t\tt.Fatal(\"wrong answer\")\n\t}\n}\n")
		out, ok, err := runGoTest(t.Context(), root, pkgDir)
		if err != nil {
			t.Fatalf("a failing test reported a runner error: %v", err)
		}
		if ok {
			t.Error("ok = true on a failing test")
		}
		if !strings.Contains(out, "FAIL") || !strings.Contains(out, "wrong answer") {
			t.Errorf("the failure is not in the output:\n%s", out)
		}
	})

	t.Run("source that does not compile", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc add(a, b int) int { return a + }\n",
			"package twosum\n")
		out, ok, err := runGoTest(t.Context(), root, pkgDir)
		// A compile error is still `go test` having its say, so it reports as a
		// failure with output rather than as a runner error — the user needs to
		// read the compiler, not a wrapper's summary of it.
		if err != nil {
			t.Fatalf("a compile error reported a runner error: %v", err)
		}
		if ok {
			t.Error("ok = true on source that does not compile")
		}
		if !strings.Contains(out, "syntax error") && !strings.Contains(out, "expected") {
			t.Errorf("the compiler's complaint is not in the output:\n%s", out)
		}
	})

	t.Run("a package that is not there", func(t *testing.T) {
		root := goModule(t, pkgDir, "package twosum\n", "package twosum\n")
		_, ok, err := runGoTest(t.Context(), root, "solutions/0999-nope")
		if err != nil {
			t.Fatalf("a missing package reported a runner error: %v", err)
		}
		if ok {
			t.Error("ok = true for a package that does not exist")
		}
	})

	t.Run("a cancelled run reports why", func(t *testing.T) {
		root := goModule(t, pkgDir,
			"package twosum\n\nfunc spin() {\n\tfor {\n\t}\n}\n",
			"package twosum\n\nimport \"testing\"\n\nfunc TestSpin(t *testing.T) {\n\tspin()\n}\n")
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		out, ok, err := runGoTest(ctx, root, pkgDir)
		if err == nil {
			t.Fatalf("an endless test was reported as a result, ok = %v, output:\n%s", ok, out)
		}
		if ok {
			t.Error("ok = true on a cut-off run")
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
