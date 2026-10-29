package scaffold

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"neetcode150/internal/catalog"
)

func TestPackageName(t *testing.T) {
	cases := map[string]string{
		"two-sum":                          "twosum",
		"3sum":                             "p3sum", // a package name may not start with a digit
		"pow-x-n":                          "powxn",
		"lru-cache":                        "lrucache",
		"two-sum-ii-input-array-is-sorted": "twosumiiinputarrayissorted",
		"":                                 "solution",
	}
	for in, want := range cases {
		if got := PackageName(in); got != want {
			t.Errorf("PackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

const treeSignature = `/**
 * Definition for a binary tree node.
 * type TreeNode struct {
 *     Val int
 *     Left *TreeNode
 *     Right *TreeNode
 * }
 */
func invertTree(root *TreeNode) *TreeNode {
    
}`

func TestHelperTypesComeFromTheSnippetComment(t *testing.T) {
	got := HelperTypes(treeSignature)
	if !strings.HasPrefix(got, "type TreeNode struct {") {
		t.Errorf("TreeNode definition not recovered:\n%s", got)
	}
	if !strings.Contains(got, "Right *TreeNode") || !strings.HasSuffix(got, "}") {
		t.Errorf("definition is truncated:\n%s", got)
	}
	if HelperTypes("func twoSum(nums []int, target int) []int {\n\n}") != "" {
		t.Error("a snippet with no comment should yield no helper types")
	}
}

func TestExamplesStripMarkdownEmphasis(t *testing.T) {
	statement := "**Example 1:**\n\n**Input:** `nums = [2,7,11,15], target = 9`\n\n**Output:** `[0,1]`\n"
	got := Examples(statement, 3)
	want := []string{"Input: nums = [2,7,11,15], target = 9", "Output: [0,1]"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExamplesHandleFencedFormat(t *testing.T) {
	statement := "```\nInput: lists = [[1,4,5]]\nOutput: [1,4,5]\n```\n"
	got := Examples(statement, 1)
	if len(got) != 2 || got[0] != "Input: lists = [[1,4,5]]" {
		t.Errorf("got %q", got)
	}
}

// compiles parses generated source the way the Go toolchain would.
func compiles(t *testing.T, name, src string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), name, src, parser.AllErrors); err != nil {
		t.Errorf("%s does not parse: %v\n%s", name, err, src)
	}
}

var twoSum = catalog.Problem{
	ID: 1, Slug: "two-sum", Title: "Two Sum", Difficulty: "Easy",
	Pattern: "Arrays & Hashing", Dir: "0001-two-sum",
	LeetCodeURL: "https://leetcode.com/problems/two-sum/",
}

func TestGenerateSingleFunctionProducesATable(t *testing.T) {
	sig := "func twoSum(nums []int, target int) []int {\n    \n}"
	statement := "**Input:** `nums = [2,7,11,15], target = 9`\n\n**Output:** `[0,1]`\n"
	f := Generate(twoSum, sig, statement)

	if !f.TableDriven {
		t.Error("a single function with one result should get a table-driven test")
	}
	if f.FuncName != "twoSum" {
		t.Errorf("FuncName = %q", f.FuncName)
	}
	if f.Package != "twosum" {
		t.Errorf("Package = %q", f.Package)
	}
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)

	// The stub must compile, so the empty body gets a panic.
	if !strings.Contains(f.Solution, `panic("TODO: implement")`) {
		t.Errorf("stub body has no panic:\n%s", f.Solution)
	}
	for _, want := range []string{"nums   []int", "target int", "want   []int", "twoSum(tc.nums, tc.target)"} {
		if !strings.Contains(f.Test, want) {
			t.Errorf("test is missing %q:\n%s", want, f.Test)
		}
	}
}

func TestGenerateCarriesHelperTypesIntoTheSolution(t *testing.T) {
	p := twoSum
	p.Slug, p.Title = "invert-binary-tree", "Invert Binary Tree"
	f := Generate(p, treeSignature, "")
	if !strings.Contains(f.Solution, "type TreeNode struct {") {
		t.Errorf("TreeNode not emitted:\n%s", f.Solution)
	}
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)
}

func TestGenerateTreatsADesignProblemAsASkeleton(t *testing.T) {
	sig := `type LRUCache struct {
    
}

func Constructor(capacity int) LRUCache {
    
}

func (this *LRUCache) Get(key int) int {
    
}`
	p := twoSum
	p.Slug, p.Title = "lru-cache", "LRU Cache"
	f := Generate(p, sig, "")

	if f.TableDriven {
		t.Error("a design problem must not get a table-driven test built around its constructor")
	}
	if !strings.Contains(f.Solution, "type LRUCache struct") {
		t.Error("the type declaration was dropped")
	}
	if strings.Contains(f.Solution, "panic(\"TODO: implement\")\n}\n\ntype") {
		t.Error("a panic was inserted into the struct body")
	}
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)
}

func TestGenerateHandlesTwoPlainFunctions(t *testing.T) {
	sig := "func encode(strs []string) string {\n    \n}\n\nfunc decode(s string) []string {\n    \n}"
	p := twoSum
	p.Slug, p.Title = "encode-and-decode-strings", "Encode and Decode Strings"
	f := Generate(p, sig, "")
	if f.TableDriven {
		t.Error("two entry points cannot be driven from one table")
	}
	if strings.Count(f.Solution, "panic(") != 2 {
		t.Errorf("both bodies should panic:\n%s", f.Solution)
	}
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)
}

func TestGenerateHandlesAVoidFunction(t *testing.T) {
	sig := "func wallsAndGates(rooms [][]int) {\n    \n}"
	p := twoSum
	p.Slug, p.Title = "walls-and-gates", "Walls And Gates"
	f := Generate(p, sig, "")
	if f.TableDriven {
		t.Error("a function with no result has nothing to compare")
	}
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)
}

func TestGenerateWithNoSignatureStillProducesValidFiles(t *testing.T) {
	f := Generate(twoSum, "", "")
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)
	if !strings.Contains(f.Solution, "Write the entry point yourself") {
		t.Errorf("no guidance for a missing signature:\n%s", f.Solution)
	}
}

func TestNotesTemplateIsValidMarkdownWithFrontmatter(t *testing.T) {
	out := NotesTemplate(twoSum)
	if !strings.HasPrefix(out, "---\n") || !strings.Contains(out, "title: Notes — Two Sum") {
		t.Errorf("frontmatter missing:\n%s", out)
	}
	for _, heading := range []string{"## Approach", "## Complexity", "## What tripped me up"} {
		if !strings.Contains(out, heading) {
			t.Errorf("missing %q", heading)
		}
	}
}
