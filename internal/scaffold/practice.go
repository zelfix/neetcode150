package scaffold

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/testbank"
	"neetcode150/internal/workspace"
)

// EnsurePracticeTests adds a separate suite to an existing workspace. Personal
// tests stay untouched, and new attempts receive the suite through Generate.
func EnsurePracticeTests(lay workspace.Layout, p catalog.Problem, l lang.Lang) ([]string, error) {
	suite, err := testbank.Load(p.Slug)
	if err != nil {
		return nil, nil // custom problems still use their own test skeleton
	}
	dir := lay.Dir(p)
	if _, err := os.Stat(filepath.Join(dir, l.SolutionFile)); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if l.ID == lang.Python {
		return ensurePythonPractice(lay, p, l, suite)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "TestPracticeCases" {
				return nil, nil
			}
		}
	}
	source, err := testbank.TestSource(PackageName(p.Slug), p.Slug)
	if err != nil {
		return nil, err
	}
	return writeNew(map[string]string{filepath.Join(dir, "practice_cases_test.go"): source})
}

// ensurePythonPractice writes practice_cases.py once. There is no parser to
// look inside the user's tests with, and none is needed: the Python suite is
// its own file, so its presence is the whole check. ENTRY comes from the
// official signature on disk, never from the user's edited copy.
func ensurePythonPractice(lay workspace.Layout, p catalog.Problem, l lang.Lang, suite testbank.Suite) ([]string, error) {
	path := filepath.Join(lay.Dir(p), l.PracticeFile)
	if _, err := os.Stat(path); err == nil {
		return nil, nil
	}
	signature, err := os.ReadFile(filepath.Join(lay.Root, p.ContentDir(), l.SignatureFile))
	if err != nil {
		return nil, fmt.Errorf("no Python signature for %s: run `nc fetch`", p.Slug)
	}
	entry, err := PyEntry(string(signature), suite)
	if err != nil {
		return nil, err
	}
	source, err := testbank.PyTestSource(p.Slug, entry)
	if err != nil {
		return nil, err
	}
	return writeNew(map[string]string{path: source})
}
