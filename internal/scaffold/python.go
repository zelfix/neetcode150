package scaffold

import (
	"fmt"
	"regexp"
	"strings"

	"neetcode150/internal/catalog"
	"neetcode150/internal/testbank"
)

// The Python side of the scaffold mirrors the Go side function for function.
// There is no Python parser in Go's standard library and no new dependency is
// allowed, so this is a line scanner. The input is LeetCode's generated stub,
// whose shape is known, and the test over all 150 signatures is what makes
// that safe.

// pySplitSignature separates the block LeetCode puts above a Python stub (the
// node class definitions) from the code itself. Most stubs comment the block
// with "#"; the two Node problems wrap it in a module docstring instead.
func pySplitSignature(signature string) (header, code string) {
	lines := strings.Split(strings.TrimRight(signature, "\n"), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), `"""`) {
		j := i + 1
		for j < len(lines) && !strings.Contains(lines[j], `"""`) {
			j++
		}
		if j < len(lines) {
			j++
		}
		return strings.Join(lines[i:j], "\n"), strings.TrimSpace(strings.Join(lines[j:], "\n"))
	}
	for i < len(lines) && (strings.HasPrefix(lines[i], "#") || strings.TrimSpace(lines[i]) == "") {
		i++
	}
	return strings.Join(lines[:i], "\n"), strings.TrimSpace(strings.Join(lines[i:], "\n"))
}

var pyClassRE = regexp.MustCompile(`(?m)^class\s+(\w+)\b`)
var pyDefRE = regexp.MustCompile(`^(\s*)def\s+(\w+)\s*\((.*)\)\s*(?:->\s*(.*?))?\s*:\s*$`)

// PyHelperTypes pulls the node classes out of the header comment, uncommented,
// so solution.py defines TreeNode or ListNode itself the way the Go scaffold
// inlines the struct. Resolving the names locally is also what lets the stub's
// own annotations work without importing anything.
func PyHelperTypes(signature string) string {
	header, _ := pySplitSignature(signature)
	var lines []string
	for _, line := range strings.Split(header, "\n") {
		t := strings.TrimPrefix(line, "#")
		t = strings.TrimPrefix(t, " ")
		if strings.TrimSpace(t) == `"""` {
			continue
		}
		lines = append(lines, t)
	}
	text := strings.Join(lines, "\n")
	loc := pyClassRE.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	return strings.TrimSpace(text[loc[0]:])
}

// pyMethod is one def inside the stub's class.
type pyMethod struct {
	Name   string
	Params []string // annotations, one per parameter after self; "" when absent
	Line   int
}

// pyAnalyze reads the first class in the stub and its methods, in order.
// __init__ is reported like any other method; callers that want the entry
// points skip it.
func pyAnalyze(code string) (class string, methods []pyMethod) {
	for i, line := range strings.Split(code, "\n") {
		if m := pyClassRE.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") {
			if class != "" {
				break // a second top-level class is not the entry point
			}
			class = m[1]
			continue
		}
		if class == "" {
			continue
		}
		if m := pyDefRE.FindStringSubmatch(line); m != nil {
			methods = append(methods, pyMethod{Name: m[2], Params: pyParamTypes(m[3]), Line: i})
		}
	}
	return class, methods
}

// pyParamTypes splits a def's parameter list and keeps each annotation.
// Brackets nest (List[List[int]]), so the split counts depth.
func pyParamTypes(list string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, list[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, list[start:])
	var out []string
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if i == 0 && (p == "self" || p == "cls") {
			continue
		}
		if p == "" {
			continue
		}
		if eq := strings.Index(p, "="); eq >= 0 {
			p = strings.TrimSpace(p[:eq])
		}
		if colon := strings.Index(p, ":"); colon >= 0 {
			out = append(out, strings.TrimSpace(p[colon+1:]))
		} else {
			out = append(out, "")
		}
	}
	return out
}

// pyArgKind maps a parameter annotation to the harness's builder kind. helpers
// is the node class text, which is what tells a graph Node from a random one.
func pyArgKind(annotation, helpers string) string {
	a := strings.Trim(strings.ReplaceAll(annotation, " ", ""), "'\"")
	nested := (strings.HasPrefix(a, "List[") || strings.HasPrefix(a, "list[")) && strings.Contains(a, "Node")
	switch {
	case strings.Contains(a, "TreeNode"):
		if nested {
			return "tree[]"
		}
		return "tree"
	case strings.Contains(a, "ListNode"):
		if nested {
			return "list[]"
		}
		return "list"
	case strings.Contains(a, "Node"):
		kind := "graph"
		if strings.Contains(helpers, "random") {
			kind = "random"
		}
		if nested {
			return kind + "[]"
		}
		return kind
	}
	return ""
}

// PyEntry decides what practice_cases.py will tell the harness to call. The
// class name comes from the signature, never from the fixture: ops[0] in a
// design suite is sometimes "Constructor" and sometimes the class name.
func PyEntry(signature string, suite testbank.Suite) (testbank.PyEntry, error) {
	_, code := pySplitSignature(signature)
	helpers := PyHelperTypes(signature)
	class, methods := pyAnalyze(code)
	if class == "" {
		return testbank.PyEntry{}, fmt.Errorf("%s: no class in the Python signature", suite.Slug)
	}
	entry := testbank.PyEntry{Class: class, Methods: []string{}, Args: []string{}}
	var named []pyMethod
	for _, m := range methods {
		if m.Name != "__init__" {
			named = append(named, m)
		}
	}
	switch suite.Mode {
	case "operations":
		return entry, nil
	case "strings-codec", "tree-codec":
		if len(named) != 2 {
			return entry, fmt.Errorf("%s: a codec needs two methods, the signature has %d", suite.Slug, len(named))
		}
		entry.Methods = []string{named[0].Name, named[1].Name}
		if suite.Mode == "tree-codec" {
			entry.Args = []string{"tree"} // the old-style stub carries no annotation
		} else {
			entry.Args = kindsOf(named[0], helpers)
		}
		return entry, nil
	}
	var target *pyMethod
	for i := range named {
		if named[i].Name == suite.Entry {
			target = &named[i]
		}
	}
	if target == nil && len(named) == 1 {
		target = &named[0]
	}
	if target == nil {
		return entry, fmt.Errorf("%s: the signature has no method %s", suite.Slug, suite.Entry)
	}
	entry.Methods = []string{target.Name}
	entry.Args = kindsOf(*target, helpers)
	return entry, nil
}

func kindsOf(m pyMethod, helpers string) []string {
	out := make([]string, len(m.Params))
	for i, p := range m.Params {
		out[i] = pyArgKind(p, helpers)
	}
	return out
}

// pyWithTODO gives every empty method body something to run. LeetCode ships
// the body as a blank indented line, which is a SyntaxError in Python rather
// than Go's compilable {}; a body that is only a docstring is filled too, so
// the stub fails loudly instead of returning None.
func pyWithTODO(code string) string {
	lines := strings.Split(code, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		m := pyDefRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		indent := m[1] + "    "
		// The body is every following line indented deeper than the def.
		j := i + 1
		var body []int
		for j < len(lines) {
			t := strings.TrimSpace(lines[j])
			if t != "" && !strings.HasPrefix(lines[j], indent) {
				break
			}
			if t != "" {
				body = append(body, j)
			}
			j++
		}
		if len(body) == 0 {
			out = append(out, indent+`raise NotImplementedError("TODO: implement")`)
			i = j - 1
			continue
		}
		first, last := strings.TrimSpace(lines[body[0]]), strings.TrimSpace(lines[body[len(body)-1]])
		docOnly := (strings.HasPrefix(first, `"""`) && strings.HasSuffix(last, `"""`) && (len(body) > 1 || len(first) >= 6)) ||
			(strings.HasPrefix(first, "'''") && strings.HasSuffix(last, "'''") && (len(body) > 1 || len(first) >= 6))
		if docOnly {
			out = append(out, lines[i+1:body[len(body)-1]+1]...)
			out = append(out, indent+`raise NotImplementedError("TODO: implement")`)
			i = body[len(body)-1]
		}
	}
	return strings.Join(out, "\n")
}

// GeneratePython builds solution.py, test_solution.py and practice_cases.py
// for a problem, the Python counterpart of Generate.
// typingNames is which typing names the generated body refers to, in import
// order. Only the subscripted forms count: `Optional` inside a comment or a
// word like "Optionally" is not a use.
func typingNames(body string) []string {
	var out []string
	for _, name := range []string{"List", "Optional"} {
		if strings.Contains(body, name+"[") {
			out = append(out, name)
		}
	}
	return out
}

func GeneratePython(p catalog.Problem, signature, statement string) Files {
	f := Files{Package: PackageName(p.Slug)}
	_, code := pySplitSignature(signature)
	helpers := PyHelperTypes(signature)

	// The body is built first so the import line can be decided by what the body
	// actually says. Modern stubs annotate with the builtins — `list[int]`,
	// `TreeNode | None` — so for all but a dozen problems the typing names are
	// dead on arrival, and a dead import is the first thing a reader questions.
	var body strings.Builder
	if helpers != "" {
		body.WriteString(helpers)
		body.WriteString("\n\n\n")
	}
	if strings.TrimSpace(code) == "" {
		body.WriteString("# No signature on file for this problem. Write the entry point yourself.\n")
		body.WriteString("class Solution:\n    pass\n")
	} else {
		body.WriteString(pyWithTODO(code))
		body.WriteString("\n")
	}

	var sol strings.Builder
	fmt.Fprintf(&sol, "# %d. %s — %s, %s\n", p.ID, p.Title, p.Difficulty, p.Pattern)
	fmt.Fprintf(&sol, "# %s\n", p.LeetCodeURL)
	fmt.Fprintf(&sol, "#\n# Statement: %s/problem.md\n\n", p.ContentDir())
	if names := typingNames(body.String()); len(names) > 0 {
		fmt.Fprintf(&sol, "from typing import %s\n\n\n", strings.Join(names, ", "))
	}
	sol.WriteString(body.String())
	f.Solution = sol.String()

	class, methods := pyAnalyze(code)
	suite, err := testbank.Load(p.Slug)
	if err == nil && strings.TrimSpace(code) != "" {
		if entry, err := PyEntry(signature, suite); err == nil {
			if practice, err := testbank.PyTestSource(p.Slug, entry); err == nil {
				f.Practice = practice
				f.TableDriven, f.PracticeCases = true, len(suite.Cases)
				if len(entry.Methods) == 1 {
					f.FuncName = entry.Methods[0]
				}
			}
		}
	}
	single := ""
	if class == "Solution" {
		var named []pyMethod
		for _, m := range methods {
			if m.Name != "__init__" {
				named = append(named, m)
			}
		}
		if len(named) == 1 {
			single = named[0].Name
		}
	}
	if single != "" {
		if f.FuncName == "" {
			f.FuncName = single
		}
		f.Test = pyTableTest(single, statement, f.Practice != "")
	} else {
		f.Test = pyGenericTest(statement, f.Practice != "")
	}
	return f
}

func pyExampleComment(statement string) string {
	ex := Examples(statement, 3)
	if len(ex) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Examples from the statement:\n")
	for _, line := range ex {
		fmt.Fprintf(&b, "#   %s\n", line)
	}
	return b.String()
}

// pyTableTest writes the personal test file.
//
// When covered is true the populated suite in practice_cases.py already asserts
// the statement's examples and the corner cases, so a second table here starts
// empty — and an empty table has only bad options: skipping reports a skip on
// every run for coverage that is not actually missing, and looping over nothing
// reports a pass without having tested anything. So the scaffold is written out
// commented instead: nothing to run, nothing to claim, and the shape is there to
// uncomment when a case of your own is worth keeping. The Go scaffold reaches
// the same place by a different road — it embeds the suite in the one test file
// and never writes a second.
func pyTableTest(method, statement string, covered bool) string {
	var b strings.Builder
	b.WriteString(pyExampleComment(statement))
	if covered {
		b.WriteString("#\n# practice_cases.py beside this file already asserts those examples and the\n")
		b.WriteString("# corner cases, so nothing is missing here. This file is for cases of your\n")
		b.WriteString("# own — uncomment and add to it.\n#\n")
		b.WriteString("# import unittest\n#\n# from solution import Solution\n#\n#\n")
		fmt.Fprintf(&b, "# class Test%s(unittest.TestCase):\n", exportedName(method))
		b.WriteString("#     cases = [\n#         # (\"name\", (arguments...), expected)\n#     ]\n#\n")
		b.WriteString("#     def test_cases(self):\n")
		b.WriteString("#         for name, args, want in self.cases:\n")
		b.WriteString("#             with self.subTest(name):\n")
		fmt.Fprintf(&b, "#                 self.assertEqual(Solution().%s(*args), want)\n", method)
		return b.String()
	}
	// No curated suite for this problem, so this file is the only thing that
	// tests anything: it stays runnable, and skipping is the honest report
	// until there are cases.
	b.WriteString("import unittest\n\nfrom solution import Solution\n\n\n")
	fmt.Fprintf(&b, "class Test%s(unittest.TestCase):\n", exportedName(method))
	b.WriteString("    # TODO: one case per example in problem.md, then the edge cases:\n")
	b.WriteString("    # (\"name\", (arguments...), expected)\n")
	b.WriteString("    cases = []\n\n")
	b.WriteString("    def test_cases(self):\n")
	b.WriteString("        if not self.cases:\n")
	b.WriteString("            self.skipTest(\"add the examples from problem.md\")\n")
	b.WriteString("        for name, args, want in self.cases:\n")
	b.WriteString("            with self.subTest(name):\n")
	fmt.Fprintf(&b, "                self.assertEqual(Solution().%s(*args), want)\n", method)
	return b.String()
}

func pyGenericTest(statement string, covered bool) string {
	var b strings.Builder
	b.WriteString(pyExampleComment(statement))
	b.WriteString("# This problem does not reduce to one function of one return value,\n")
	b.WriteString("# so drive it the way the statement describes: build it, call it, check it.\n")
	if covered {
		b.WriteString("#\n# practice_cases.py beside this file already drives it that way, so nothing\n")
		b.WriteString("# is missing here. This file is for cases of your own — uncomment and add.\n#\n")
		b.WriteString("# import unittest\n#\n# import solution\n#\n#\n")
		b.WriteString("# class TestSolution(unittest.TestCase):\n")
		b.WriteString("#     def test_solution(self):\n#         pass\n")
		return b.String()
	}
	b.WriteString("import unittest\n\nimport solution  # noqa: F401\n\n\n")
	b.WriteString("class TestSolution(unittest.TestCase):\n")
	b.WriteString("    def test_solution(self):\n")
	b.WriteString("        self.skipTest(\"write the cases from problem.md\")\n")
	return b.String()
}
