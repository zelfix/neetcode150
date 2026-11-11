// Package lang is the one place that knows what differs between the languages
// a solution can be written in: file names, the formatter, how the tutor
// labels code. Everything else asks here rather than spelling "solution.go".
//
// Deliberately absent: the scaffold generators (they would import this
// package and be imported by it) and the container argument lists (the mount
// set is the security model, and it stays beside its golden test in
// internal/web). Both switch on ID where they live.
package lang

import (
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
)

// ID names a language the way the database and the CLI spell it.
type ID string

const (
	Go     ID = "go"
	Python ID = "python"
)

// Lang describes one language.
type Lang struct {
	ID    ID
	Label string

	SolutionFile  string // the file the user edits
	TestFile      string // their own tests, scaffolded once
	PracticeFile  string // the generated fixture suite, regenerable
	SignatureFile string // under content/problems/<dir>/

	Fence      string // the Markdown code-fence label
	AttemptExt string // archived snapshots, not compiled with the package
	IdiomsRef  string // under tutor/references/
	Indent     string // what Tab inserts in the browser editor
	RunHint    string // the command a person types to test by hand

	// testGlobs are the test files a workspace may hold, for the Tests tab
	// and the tutor's context.
	testGlobs []string
}

var golang = Lang{
	ID: Go, Label: "Go",
	SolutionFile: "solution.go", TestFile: "solution_test.go", PracticeFile: "practice_cases_test.go",
	SignatureFile: "signature.txt",
	Fence:         "go", AttemptExt: ".go.txt", IdiomsRef: "go-idioms.md", Indent: "\t",
	RunHint:   "go test",
	testGlobs: []string{"*_test.go"},
}

var python = Lang{
	ID: Python, Label: "Python",
	SolutionFile: "solution.py", TestFile: "test_solution.py", PracticeFile: "practice_cases.py",
	SignatureFile: "signature.py",
	Fence:         "python", AttemptExt: ".py.txt", IdiomsRef: "python-idioms.md", Indent: "    ",
	RunHint:   "python3 internal/testkit/py/run.py --package",
	testGlobs: []string{"test_*.py", "practice_cases.py"},
}

// Known lists the languages in the order a chooser shows them.
func Known() []Lang { return []Lang{golang, python} }

// Parse accepts the ids and the usual short forms. An empty string is Go, so
// a row written before languages existed reads as it always did.
func Parse(s string) (Lang, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "go", "golang":
		return golang, nil
	case "python", "py", "python3":
		return python, nil
	}
	return Lang{}, fmt.Errorf("unknown language %q; use go or python", s)
}

// Of is Parse for a value already stored, where an unknown id is a bug
// rather than user input: it falls back to Go instead of failing a page.
func Of(id ID) Lang {
	l, err := Parse(string(id))
	if err != nil {
		return golang
	}
	return l
}

// Resolve is the one rule for which language a problem is worked in: what the
// problem's own entry says, else Go.
//
// There is deliberately no per-person default in between. A language that
// applies to every problem at once has to be changed before starting one and
// read back everywhere afterwards, which is a setting to maintain rather than a
// choice to make; the choice belongs to the problem, where Start asks for it
// outright. Go is the fallback only for a problem nobody has started yet, so
// that paths and labels have something to show.
func Resolve(entry string) Lang {
	if l, err := Parse(entry); err == nil && entry != "" {
		return l
	}
	return golang
}

// Formatter is the name of the tool Format runs, or "" when the language has
// none here. Callers that offer formatting as an action need to say so rather
// than appear to do nothing: Format returns Python unchanged and no error,
// which is indistinguishable from source that was already tidy.
func (l Lang) Formatter() string {
	if l.ID == Go {
		return "gofmt"
	}
	return ""
}

// Format tidies source the way the language's own tool would. Python has no
// formatter in the standard library and this project takes no dependency for
// one, so Python source is returned as written.
func (l Lang) Format(src string) (string, error) {
	if l.ID != Go {
		return src, nil
	}
	out, err := format.Source([]byte(src))
	if err != nil {
		return src, err
	}
	return string(out), nil
}

// IsTestFile reports whether a file in the workspace is one of this
// language's test files.
func (l Lang) IsTestFile(name string) bool {
	base := filepath.Base(name)
	for _, g := range l.testGlobs {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
	}
	return false
}

// TestFiles lists the test files present in dir, the user's own first.
func (l Lang) TestFiles(dir string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range l.testGlobs {
		matches, _ := filepath.Glob(filepath.Join(dir, g))
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}
