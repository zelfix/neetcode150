package scaffold

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/catalog"
	"neetcode150/internal/testbank"
)

// pythonForTests is python3 when it is at least 3.12, the version the sandbox
// image ships; otherwise "" and the caller skips.
func pythonForTests(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	out, err := exec.Command(python, "-I", "-c", "import sys; print(sys.version_info >= (3, 12))").Output()
	if err != nil || strings.TrimSpace(string(out)) != "True" {
		t.Skip("python3 is older than 3.12")
	}
	return python
}

func loadPythonSignatures(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/practice-signatures-python.json")
	if err != nil {
		t.Fatal(err)
	}
	var signatures map[string]string
	if err := json.Unmarshal(data, &signatures); err != nil {
		t.Fatal(err)
	}
	return signatures
}

// Cheap enough for -short: the table has to name every problem before the
// slower check below can mean anything.
func TestEveryProblemHasAPythonSignature(t *testing.T) {
	root, _ := filepath.Abs("../..")
	cat, err := catalog.Load(filepath.Join(root, "content/problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	signatures := loadPythonSignatures(t)
	for _, p := range cat.Problems {
		sig, ok := signatures[p.Slug]
		if !ok || !strings.Contains(sig, "class ") {
			t.Errorf("%s: no Python signature", p.Slug)
		}
	}
}

// The Python counterpart of TestAllPracticeSuitesMatchGoSignatures: every
// fixture is checked against the official Python stub by the harness itself,
// in validate mode, before any of it is visible to a user.
func TestAllPracticeSuitesMatchPythonSignatures(t *testing.T) {
	if testing.Short() {
		t.Skip("validates the 150 practice interfaces under python3")
	}
	python := pythonForTests(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(filepath.Join(root, "content/problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	signatures := loadPythonSignatures(t)

	dir := t.TempDir()
	args := []string{"-I", "-B", filepath.Join(root, "internal/testkit/py/run.py"), "--mode", "validate"}
	for _, p := range cat.Problems {
		signature, ok := signatures[p.Slug]
		if !ok {
			t.Fatalf("missing signature: %s", p.Slug)
		}
		suite, err := testbank.Load(p.Slug)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := PyEntry(signature, suite)
		if err != nil {
			t.Fatal(err)
		}
		// The class name is read from the signature; the fixture is only a
		// cross-check, because its first op is sometimes "Constructor".
		if suite.Mode == "operations" {
			for _, c := range suite.Cases {
				if c.Ops[0] != "Constructor" && c.Ops[0] != entry.Class {
					t.Errorf("%s: signature class %s, fixture constructs %s", p.Slug, entry.Class, c.Ops[0])
					break
				}
			}
		} else if suite.Mode == "function" && entry.Methods[0] != suite.Entry {
			t.Errorf("%s: signature method %s, fixture entry %s", p.Slug, entry.Methods[0], suite.Entry)
		}
		f := GeneratePython(p, signature, "")
		if f.PracticeCases == 0 || f.Practice == "" {
			t.Fatalf("%s: no practice file generated", p.Slug)
		}
		target := filepath.Join(dir, p.Dir)
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, src := range map[string]string{"solution.py": f.Solution, "practice_cases.py": f.Practice, "test_solution.py": f.Test} {
			if err := os.WriteFile(filepath.Join(target, name), []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		args = append(args, "--package", target)
	}
	cmd := exec.Command(python, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("python practice interface/fixture check: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "validated 150 packages, 0 problems") {
		t.Fatalf("unexpected validate output:\n%s", out.String())
	}
}
