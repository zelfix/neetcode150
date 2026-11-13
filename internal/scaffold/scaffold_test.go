package scaffold

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/studydb"
	"neetcode150/internal/workspace"
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

// A fetched statement escapes brackets because Markdown needs it; a comment
// meant to be read as the input does not.
func TestExamplesUnescapeMarkdownPunctuation(t *testing.T) {
	statement := "**Example 1:**\n\n**Input:** nums = \\[1,2,3,1\\]\n\n**Output:** true\n"
	got := Examples(statement, 3)
	if len(got) == 0 || got[0] != "Input: nums = [1,2,3,1]" {
		t.Errorf("got %q, want the brackets unescaped", got)
	}
}

// Only punctuation can be escaped in Markdown, so a statement that talks about
// escape sequences keeps them.
func TestExamplesKeepNonPunctuationBackslashes(t *testing.T) {
	statement := "**Example 1:**\n\n**Input:** s = \"a\\nb\"\n\n**Output:** 2\n"
	got := Examples(statement, 1)
	if len(got) == 0 || got[0] != `Input: s = "a\nb"` {
		t.Errorf("got %q, want the \\n left alone", got)
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
	for _, want := range []string{"testkit.Run(t,", `"kind": "example"`, `"kind": "edge"`, "twoSum)"} {
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

	if !f.TableDriven || f.PracticeCases == 0 {
		t.Error("design problems need operation sequence cases")
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
	if !f.TableDriven || !strings.Contains(f.Test, "encode, decode") {
		t.Error("codec needs round-trip cases using both entry points")
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
	if !f.TableDriven || !strings.Contains(f.Test, `"mutates": true`) {
		t.Error("void functions need cases asserting the mutated argument")
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

func TestAttemptsKeepStableIDsAndSnapshots(t *testing.T) {
	root := t.TempDir()
	p := catalog.Problem{Dir: "0001-two-sum"}
	day := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	var want []string
	for i := 1; i <= 12; i++ {
		path, err := ArchiveAttempt(ownerScope(t, root), p, lang.Of(lang.Go), []byte(fmt.Sprintf("attempt %d", i)), day)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, path)
	}
	// Creation order must not override the dates encoded in the filenames.
	earlier, err := ArchiveAttempt(ownerScope(t, root), p, lang.Of(lang.Go), nil, day.AddDate(0, 0, -1))
	if err != nil {
		t.Fatal(err)
	}
	later, err := ArchiveAttempt(ownerScope(t, root), p, lang.Of(lang.Go), nil, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, earlier, later)
	got, err := Attempts(ownerScope(t, root), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d attempts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attempt %d: got %s, want %s", i, filepath.Base(got[i]), filepath.Base(want[i]))
		}
	}
}

// ownerScope opens the study database for a test root and returns the owner's
// view, which is what the CLI passes in production.
func ownerScope(t *testing.T, root string) *studydb.Scope {
	t.Helper()
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.Owner()
}

// fakeProblem is a synthetic catalog entry whose slug has no testbank suite,
// so Generate falls back to the standard table template. That keeps these tests
// independent of the curated fixtures and avoids testkit imports in the
// generated test file.
var fakeProblem = catalog.Problem{
	ID: 9999, Slug: "fake-add", Title: "Fake Add", Difficulty: "Easy",
	Pattern: "Arrays & Hashing", Dir: "9999-fake-add",
	LeetCodeURL: "https://leetcode.com/problems/fake-add/",
}

const fakeSignature = "func fakeAdd(a, b int) int {\n    \n}"

func TestCreateWritesFilesAndDoesNotOverwriteExisting(t *testing.T) {
	root := t.TempDir()
	lay := workspace.Owner(root)

	written, f, err := Create(ownerScope(t, root), lay, fakeProblem, lang.Of(lang.Go), fakeSignature, "")
	if err != nil {
		t.Fatal(err)
	}

	// Both solution.go and solution_test.go must be on disk after the first Create.
	var hasSol, hasTest bool
	for _, w := range written {
		base := filepath.Base(w)
		if base == "solution.go" {
			hasSol = true
		}
		if strings.HasSuffix(base, "_test.go") {
			hasTest = true
		}
	}
	if !hasSol {
		t.Errorf("solution.go was not created; written = %v", written)
	}
	if !hasTest {
		t.Errorf("no test file was created; written = %v", written)
	}
	// Both generated files must parse without errors.
	compiles(t, "solution.go", f.Solution)
	compiles(t, "solution_test.go", f.Test)

	// Overwrite solution.go to simulate work in progress.
	solPath := lay.Path(fakeProblem, "solution.go")
	personal := "package fakeadd\n\n// personal work\nfunc fakeAdd(a, b int) int { return a + b }\n"
	if err := os.WriteFile(solPath, []byte(personal), 0600); err != nil {
		t.Fatal(err)
	}

	// A second Create must not touch the modified solution.go.
	written2, _, err := Create(ownerScope(t, root), lay, fakeProblem, lang.Of(lang.Go), fakeSignature, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range written2 {
		if filepath.Base(w) == "solution.go" {
			t.Errorf("second Create wrote solution.go: %s", w)
		}
	}
	got, err := os.ReadFile(solPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != personal {
		t.Errorf("second Create changed solution.go:\ngot  %q\nwant %q", got, personal)
	}
}

func TestResetArchivesCurrentSolutionAndWritesFreshScaffold(t *testing.T) {
	root := t.TempDir()
	lay := workspace.Owner(root)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	// Create an initial workspace so solution.go exists on disk.
	if _, _, err := Create(ownerScope(t, root), lay, fakeProblem, lang.Of(lang.Go), fakeSignature, ""); err != nil {
		t.Fatal(err)
	}

	// Replace the stub with a recognisable first attempt.
	solPath := lay.Path(fakeProblem, "solution.go")
	firstAttempt := "package fakeadd\n\n// first attempt\nfunc fakeAdd(a, b int) int { return a + b }\n"
	if err := os.WriteFile(solPath, []byte(firstAttempt), 0600); err != nil {
		t.Fatal(err)
	}

	archive, _, err := Reset(ownerScope(t, root), lay, fakeProblem, lang.Of(lang.Go), fakeSignature, "", now)
	if err != nil {
		t.Fatal(err)
	}

	// The archive reference must be retrievable and contain the exact code that
	// was on disk before the reset.
	refs, err := Attempts(ownerScope(t, root), fakeProblem)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 archived attempt, got %d", len(refs))
	}
	if refs[0] != archive {
		t.Errorf("archive ref mismatch: Attempts returned %q, Reset returned %q", refs[0], archive)
	}
	attempt, err := ReadAttempt(ownerScope(t, root), archive)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Code != firstAttempt {
		t.Errorf("archived code:\ngot  %q\nwant %q", attempt.Code, firstAttempt)
	}

	// solution.go must now contain a fresh scaffold, not the old attempt.
	fresh, err := os.ReadFile(solPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(fresh) == firstAttempt {
		t.Error("Reset did not overwrite solution.go")
	}
	compiles(t, "solution.go (after reset)", string(fresh))

	// Resetting a workspace that has no solution.go must fail.
	emptyRoot := t.TempDir()
	if _, _, err := Reset(ownerScope(t, emptyRoot), workspace.Owner(emptyRoot), fakeProblem, lang.Of(lang.Go), fakeSignature, "", now); err == nil {
		t.Error("Reset with no solution.go returned nil error")
	}
}

// An interview scaffolds with WithoutPattern, because the file it hands over
// opens with a comment naming the problem — and the pattern is the thing the
// candidate is being asked to work out.
func TestWithoutPatternKeepsThePatternOutOfTheHeader(t *testing.T) {
	sig := "func twoSum(nums []int, target int) []int {\n    \n}"
	statement := "**Input:** `nums = [2,7,11,15], target = 9`\n\n**Output:** `[0,1]`\n"

	plain := Generate(twoSum, sig, statement)
	if !strings.Contains(plain.Solution, twoSum.Pattern) {
		t.Fatal("the ordinary header lost the pattern, so this test proves nothing")
	}
	anon := WithoutPattern(twoSum)
	for _, l := range []lang.Lang{lang.Of(lang.Go), lang.Of(lang.Python)} {
		f := GenerateFor(l, anon, sig, statement)
		if strings.Contains(f.Solution, twoSum.Pattern) {
			t.Errorf("%s solution names the pattern", l.Label)
		}
		// Still the same problem, and the rest of the header has to survive.
		for _, want := range []string{"1. Two Sum", "Easy", twoSum.LeetCodeURL} {
			if !strings.Contains(f.Solution, want) {
				t.Errorf("%s solution lost %q from the header", l.Label, want)
			}
		}
		if strings.Contains(f.Solution, "Easy, ") {
			t.Errorf("%s solution left a dangling separator where the pattern was", l.Label)
		}
	}
	// It is a copy: the catalog's own problem must keep its pattern.
	if twoSum.Pattern == "" {
		t.Error("WithoutPattern mutated the problem it was given")
	}
}
