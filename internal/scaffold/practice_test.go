package scaffold

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/catalog"
	"neetcode150/internal/testbank"
	"neetcode150/internal/workspace"
)

// These are public interfaces with empty bodies, so this check works on a fresh
// clone without downloading statements or reading a learner's solutions.
func TestAllPracticeSuitesMatchGoSignatures(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the 150 practice interfaces")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(filepath.Join(root, "content/problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/practice-signatures.json")
	if err != nil {
		t.Fatal(err)
	}
	var signatures map[string]string
	if err := json.Unmarshal(data, &signatures); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module neetcode150/practicecheck\n\ngo 1.26.0\n\nrequire neetcode150 v0.0.0\nreplace neetcode150 => %q\n", filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range cat.Problems {
		signature, ok := signatures[p.Slug]
		if !ok {
			t.Fatalf("missing signature: %s", p.Slug)
		}
		f := Generate(p, signature, "")
		if f.PracticeCases == 0 {
			t.Fatalf("missing suite: %s", p.Slug)
		}
		suite, err := testbank.Load(p.Slug)
		if err != nil {
			t.Fatal(err)
		}
		examples, edges := 0, 0
		names := map[string]bool{}
		for _, c := range suite.Cases {
			if names[c.Name] {
				t.Fatalf("%s: duplicate case name %s", p.Slug, c.Name)
			}
			names[c.Name] = true
			switch c.Kind {
			case "example":
				examples++
			case "edge":
				edges++
			default:
				t.Fatalf("unknown case kind %s", c.Kind)
			}
		}
		if examples < 1 || edges < 2 {
			t.Fatalf("%s needs examples and several edge cases", p.Slug)
		}
		target := filepath.Join(dir, p.Dir)
		if err := os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "solution.go"), []byte(f.Solution), 0600); err != nil {
			t.Fatal(err)
		}
		src := strings.Replace(f.Test, "testkit.Run(t,", "if err := testkit.Validate(", 1)
		end := strings.LastIndex(src, "\n}")
		if end < 0 {
			t.Fatal("missing generated test body")
		}
		close := strings.LastIndex(src[:end], ")")
		src = src[:close+1] + "; err != nil { t.Fatal(err) }" + src[close+1:]
		if err := os.WriteFile(filepath.Join(target, "solution_test.go"), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-mod=mod", "-p=4", "-count=1", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("practice interface/fixture check: %v\n%s", err, out)
	}
}

func TestAddingPracticeTestsPreservesWork(t *testing.T) {
	root := t.TempDir()
	dir := workspace.Owner(root).Dir(twoSum)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	solution := "package twosum\n// personal work in progress\n"
	personal := "package twosum\n// personal test cases\n"
	for name, src := range map[string]string{"solution.go": solution, "solution_test.go": personal} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	written, err := EnsurePracticeTests(workspace.Owner(root), twoSum)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("created %v", written)
	}
	written, err = EnsurePracticeTests(workspace.Owner(root), twoSum)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatal("duplicate suite on second call")
	}
	for name, want := range map[string]string{"solution.go": solution, "solution_test.go": personal} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("changed %s", name)
		}
	}
	root2 := t.TempDir()
	if paths, err := EnsurePracticeTests(workspace.Owner(root2), twoSum); err != nil || len(paths) > 0 {
		t.Fatalf("unstarted: %v %v", paths, err)
	}
	if _, err := os.Stat(filepath.Join(root2, "solutions")); !os.IsNotExist(err) {
		t.Fatal("created an unstarted workspace")
	}
}
