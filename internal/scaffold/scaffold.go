// Package scaffold generates the Go files you write your solution in, from the
// official LeetCode signature for the problem.
package scaffold

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"unicode"

	"neetcode150/internal/catalog"
)

// Files is a generated solution scaffold.
type Files struct {
	Package     string
	Solution    string
	Test        string
	FuncName    string // the entry point, when the problem has a single one
	TableDriven bool   // whether the test came out as a filled-in table
}

// PackageName turns a problem slug into a legal Go package name. Slugs such as
// "3sum" start with a digit, which a package name may not.
func PackageName(slug string) string {
	var b strings.Builder
	for _, r := range slug {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	name := b.String()
	if name == "" {
		return "solution"
	}
	if unicode.IsDigit(rune(name[0])) {
		return "p" + name
	}
	return name
}

var commentBlockRE = regexp.MustCompile(`(?s)^\s*/\*\*(.*?)\*/\s*`)

// splitSignature separates the leading /** … */ block LeetCode puts above a
// snippet from the code itself.
func splitSignature(signature string) (header, code string) {
	m := commentBlockRE.FindStringSubmatchIndex(signature)
	if m == nil {
		return "", strings.TrimSpace(signature)
	}
	return signature[m[2]:m[3]], strings.TrimSpace(signature[m[1]:])
}

var typeDeclRE = regexp.MustCompile(`(?m)^\s*type\s+\w+\s+struct\s*\{`)

// uncomment strips the leading " * " of a block comment's lines.
func uncomment(block string) string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		trimmed = strings.TrimPrefix(trimmed, "*")
		out = append(out, strings.TrimPrefix(trimmed, " "))
	}
	return strings.Join(out, "\n")
}

// HelperTypes pulls the node type definitions out of a snippet's header
// comment. LeetCode comments out exactly the types the signature needs — the
// shape of Node differs between problems, so they are read rather than assumed.
func HelperTypes(signature string) string {
	header, _ := splitSignature(signature)
	if header == "" {
		return ""
	}
	text := uncomment(header)
	loc := typeDeclRE.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	// Take from the first `type … struct {` to its closing brace.
	depth := 0
	end := -1
	for i := loc[0]; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end > 0 {
			break
		}
	}
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(text[loc[0]:end])
}

// analyze inspects a snippet. A problem with methods or a type declaration is
// a design problem: there is no single entry point to drive from a table.
func analyze(code string) (entry *ast.FuncDecl, design bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", "package x\n"+code, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	var plain []*ast.FuncDecl
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				design = true
				continue
			}
			plain = append(plain, d)
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				design = true
			}
		}
	}
	if design || len(plain) != 1 {
		return nil, design
	}
	return plain[0], false
}

// field is one parameter or result of the entry function.
type field struct {
	Name string
	Type string
}

func fieldsOf(list *ast.FieldList, fallbackPrefix string, src string) []field {
	var out []field
	if list == nil {
		return out
	}
	for i, f := range list.List {
		typ := src[f.Type.Pos()-1 : f.Type.End()-1]
		if len(f.Names) == 0 {
			out = append(out, field{Name: fmt.Sprintf("%s%d", fallbackPrefix, i+1), Type: typ})
			continue
		}
		for _, n := range f.Names {
			out = append(out, field{Name: n.Name, Type: typ})
		}
	}
	return out
}

// exampleRE picks the Input/Output lines out of a statement so they can be
// listed in the test file as the cases to fill in.
var exampleRE = regexp.MustCompile(`(?m)^\s*\*{0,2}(Input|Output)\*{0,2}:?\*{0,2}\s*(.*)$`)

// Examples returns the Input/Output pairs found in a problem statement.
func Examples(statement string, limit int) []string {
	var out []string
	for _, m := range exampleRE.FindAllStringSubmatch(statement, -1) {
		line := strings.TrimSpace(m[2])
		line = strings.ReplaceAll(line, "`", "")
		if line == "" {
			continue
		}
		out = append(out, m[1]+": "+line)
		if limit > 0 && len(out) >= limit*2 {
			break
		}
	}
	return out
}

// Generate builds the solution and test files for a problem.
func Generate(p catalog.Problem, signature, statement string) Files {
	pkg := PackageName(p.Slug)
	f := Files{Package: pkg}

	_, code := splitSignature(signature)
	helpers := HelperTypes(signature)

	var sol strings.Builder
	fmt.Fprintf(&sol, "// %d. %s — %s, %s\n", p.ID, p.Title, p.Difficulty, p.Pattern)
	fmt.Fprintf(&sol, "// %s\n", p.LeetCodeURL)
	fmt.Fprintf(&sol, "//\n// Statement: %s/problem.md\n", p.ContentDir())
	fmt.Fprintf(&sol, "package %s\n\n", pkg)
	if helpers != "" {
		sol.WriteString(helpers)
		sol.WriteString("\n\n")
	}
	if strings.TrimSpace(code) == "" {
		fmt.Fprintf(&sol, "// No signature on file for this problem. Write the entry point yourself.\n")
	} else {
		sol.WriteString(withTODO(code))
		sol.WriteString("\n")
	}
	f.Solution = sol.String()

	entry, _ := analyze(code)
	if entry == nil {
		f.Test = gofmt(genericTest(pkg, statement))
		f.Solution = gofmt(f.Solution)
		return f
	}

	src := "package x\n" + code
	params := fieldsOf(entry.Type.Params, "arg", src)
	results := fieldsOf(entry.Type.Results, "want", src)
	if len(results) != 1 || len(params) == 0 {
		// Nothing to compare, or nothing to pass: a table buys nothing here.
		f.FuncName = entry.Name.Name
		f.Test = gofmt(genericTest(pkg, statement))
		f.Solution = gofmt(f.Solution)
		return f
	}

	f.FuncName = entry.Name.Name
	f.TableDriven = true
	f.Test = gofmt(tableTest(pkg, entry.Name.Name, params, results[0], statement))
	f.Solution = gofmt(f.Solution)
	return f
}

// gofmt formats generated source, returning it unchanged if it does not parse
// so that a surprising signature still produces a file you can fix by hand.
func gofmt(src string) string {
	out, err := format.Source([]byte(src))
	if err != nil {
		return src
	}
	return string(out)
}

// withTODO fills the empty body LeetCode ships with a panic, so the scaffold
// compiles and `go test ./...` fails loudly rather than refusing to build.
func withTODO(code string) string {
	lines := strings.Split(code, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "func ") || !strings.HasSuffix(trimmed, "{") {
			continue
		}
		// Only an empty body gets the panic; anything else is left as given.
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j < len(lines) && strings.TrimSpace(lines[j]) == "}" {
			out = append(out, "\tpanic(\"TODO: implement\")")
			i = j - 1
		}
	}
	return strings.Join(out, "\n")
}

func exampleComment(statement string) string {
	ex := Examples(statement, 3)
	if len(ex) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("// Examples from the statement:\n")
	for _, line := range ex {
		fmt.Fprintf(&b, "//   %s\n", line)
	}
	return b.String()
}

func tableTest(pkg, funcName string, params []field, result field, statement string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"reflect\"\n\t\"testing\"\n)\n\n", pkg)
	b.WriteString(exampleComment(statement))
	fmt.Fprintf(&b, "func Test%s(t *testing.T) {\n", exportedName(funcName))
	b.WriteString("\ttests := []struct {\n\t\tname string\n")
	for _, prm := range params {
		fmt.Fprintf(&b, "\t\t%s %s\n", prm.Name, prm.Type)
	}
	fmt.Fprintf(&b, "\t\twant %s\n\t}{\n", result.Type)
	b.WriteString("\t\t// TODO: one case per example in problem.md, then the edge cases.\n\t}\n\n")
	b.WriteString("\tif len(tests) == 0 {\n\t\tt.Skip(\"add the examples from problem.md\")\n\t}\n")
	b.WriteString("\tfor _, tc := range tests {\n\t\tt.Run(tc.name, func(t *testing.T) {\n")
	args := make([]string, 0, len(params))
	for _, prm := range params {
		args = append(args, "tc."+prm.Name)
	}
	fmt.Fprintf(&b, "\t\t\tgot := %s(%s)\n", funcName, strings.Join(args, ", "))
	b.WriteString("\t\t\tif !reflect.DeepEqual(got, tc.want) {\n")
	b.WriteString("\t\t\t\tt.Errorf(\"got %v, want %v\", got, tc.want)\n")
	b.WriteString("\t\t\t}\n\t\t})\n\t}\n}\n")
	return b.String()
}

func genericTest(pkg, statement string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport \"testing\"\n\n", pkg)
	b.WriteString(exampleComment(statement))
	fmt.Fprintf(&b, "// This problem does not reduce to one function of one return value,\n")
	fmt.Fprintf(&b, "// so drive it the way the statement describes: build it, call it, check it.\n")
	fmt.Fprintf(&b, "func TestSolution(t *testing.T) {\n")
	fmt.Fprintf(&b, "\tt.Skip(\"write the cases from problem.md\")\n}\n")
	return b.String()
}

func exportedName(s string) string {
	if s == "" {
		return "Solution"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// NotesTemplate is the starting point for a problem's notes file.
func NotesTemplate(p catalog.Problem) string {
	return fmt.Sprintf(`---
title: Notes — %s
---

## Approach

_In your own words, before you look at anything else._

## Complexity

- Time:
- Space:

## What tripped me up

## The one thing worth remembering
`, p.Title)
}
