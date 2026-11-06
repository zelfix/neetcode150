package scaffold

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"neetcode150/internal/catalog"
	"neetcode150/internal/testbank"
	"neetcode150/internal/workspace"
)

// EnsurePracticeTests adds a separate suite to an existing workspace. Personal
// tests stay untouched, and new attempts receive the suite through Generate.
func EnsurePracticeTests(lay workspace.Layout, p catalog.Problem) ([]string, error) {
	if _, err := testbank.Load(p.Slug); err != nil {
		return nil, nil // custom problems still use their own test skeleton
	}
	dir := lay.Dir(p)
	if _, err := os.Stat(filepath.Join(dir, "solution.go")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
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
