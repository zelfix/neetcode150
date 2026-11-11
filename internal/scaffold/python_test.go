package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/testbank"
	"neetcode150/internal/workspace"
)

const pyListStub = `# Definition for singly-linked list.
# class ListNode:
#     def __init__(self, val=0, next=None):
#         self.val = val
#         self.next = next
class Solution:
    def mergeKLists(self, lists: list[ListNode | None]) -> ListNode | None:
        
`

const pyGraphStub = `"""
# Definition for a Node.
class Node:
    def __init__(self, val = 0, neighbors = None):
        self.val = val
        self.neighbors = neighbors if neighbors is not None else []
"""

from typing import Optional
class Solution:
    def cloneGraph(self, node: Optional['Node']) -> Optional['Node']:
        
`

const pyDesignStub = `class LRUCache:

    def __init__(self, capacity: int):
        

    def get(self, key: int) -> int:
        

    def put(self, key: int, value: int) -> None:
        


# Your LRUCache object will be instantiated and called as such:
# obj = LRUCache(capacity)
`

const pyCodecStub = `# Definition for a binary tree node.
# class TreeNode(object):
#     def __init__(self, x):
#         self.val = x
#         self.left = None
#         self.right = None

class Codec:

    def serialize(self, root):
        """Encodes a tree to a single string.

        :type root: TreeNode
        :rtype: str
        """
        

    def deserialize(self, data):
        """Decodes your encoded data to tree."""
        
`

func TestPyHelperTypesComeFromEitherKindOfHeader(t *testing.T) {
	if got := PyHelperTypes(pyListStub); !strings.HasPrefix(got, "class ListNode:") || !strings.Contains(got, "self.next = next") {
		t.Errorf("hash header: %q", got)
	}
	if got := PyHelperTypes(pyGraphStub); !strings.HasPrefix(got, "class Node:") || strings.Contains(got, `"""`) {
		t.Errorf("docstring header: %q", got)
	}
	if got := PyHelperTypes(pyDesignStub); got != "" {
		t.Errorf("design stub has no helper, got %q", got)
	}
}

func TestPyAnalyzeFindsTheClassAndItsMethods(t *testing.T) {
	for _, tc := range []struct {
		stub    string
		class   string
		methods []string
		params  []string // of the last method
	}{
		{pyListStub, "Solution", []string{"mergeKLists"}, []string{"list[ListNode | None]"}},
		{pyDesignStub, "LRUCache", []string{"__init__", "get", "put"}, []string{"int", "int"}},
		{pyCodecStub, "Codec", []string{"serialize", "deserialize"}, []string{""}},
	} {
		_, code := pySplitSignature(tc.stub)
		class, methods := pyAnalyze(code)
		if class != tc.class {
			t.Errorf("%s: class %q", tc.class, class)
		}
		var names []string
		for _, m := range methods {
			names = append(names, m.Name)
		}
		if strings.Join(names, ",") != strings.Join(tc.methods, ",") {
			t.Errorf("%s: methods %v", tc.class, names)
		}
		if last := methods[len(methods)-1]; strings.Join(last.Params, "|") != strings.Join(tc.params, "|") {
			t.Errorf("%s: params %q", tc.class, last.Params)
		}
	}
}

func TestPyArgKindsFollowTheAnnotations(t *testing.T) {
	graph := PyHelperTypes(pyGraphStub)
	random := "class Node:\n    def __init__(self, x, next=None, random=None):"
	for _, tc := range []struct {
		annotation, helpers, want string
	}{
		{"List[int]", "", ""},
		{"Optional[TreeNode]", "", "tree"},
		{"'TreeNode'", "", "tree"},
		{"ListNode | None", "", "list"},
		{"list[ListNode | None]", "", "list[]"},
		{"List[Optional[ListNode]]", "", "list[]"},
		{"Optional['Node']", graph, "graph"},
		{"'Optional[Node]'", random, "random"},
		{"int", "", ""},
	} {
		if got := pyArgKind(tc.annotation, tc.helpers); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.annotation, got, tc.want)
		}
	}
}

func TestPyEntryUsesTheSignatureNotTheFixture(t *testing.T) {
	entry, err := PyEntry(pyDesignStub, testbank.Suite{Slug: "lru-cache", Mode: "operations", Entry: "Constructor"})
	if err != nil || entry.Class != "LRUCache" || len(entry.Methods) != 0 {
		t.Errorf("design entry = %+v, %v", entry, err)
	}
	entry, err = PyEntry(pyCodecStub, testbank.Suite{Slug: "codec", Mode: "tree-codec", Entry: "Constructor"})
	if err != nil || entry.Class != "Codec" || strings.Join(entry.Methods, ",") != "serialize,deserialize" || strings.Join(entry.Args, ",") != "tree" {
		t.Errorf("codec entry = %+v, %v", entry, err)
	}
	entry, err = PyEntry(pyListStub, testbank.Suite{Slug: "merge-k", Mode: "function", Entry: "mergeKLists"})
	if err != nil || strings.Join(entry.Methods, ",") != "mergeKLists" || strings.Join(entry.Args, ",") != "list[]" {
		t.Errorf("function entry = %+v, %v", entry, err)
	}
	// One method is taken to be the entry whatever the fixture calls it;
	// with two there is nothing to fall back on.
	if _, err := PyEntry(pyCodecStub, testbank.Suite{Slug: "x", Mode: "function", Entry: "other"}); err == nil {
		t.Error("a method the signature lacks was accepted")
	}
}

func TestPyWithTODOFillsEmptyAndDocstringBodies(t *testing.T) {
	_, code := pySplitSignature(pyDesignStub)
	got := pyWithTODO(code)
	if n := strings.Count(got, `raise NotImplementedError("TODO: implement")`); n != 3 {
		t.Errorf("design stub got %d raises:\n%s", n, got)
	}
	_, code = pySplitSignature(pyCodecStub)
	got = pyWithTODO(code)
	if n := strings.Count(got, "raise NotImplementedError"); n != 2 {
		t.Errorf("codec stub got %d raises:\n%s", n, got)
	}
	if strings.Index(got, ":rtype: str") > strings.Index(got, "raise NotImplementedError") {
		t.Error("the raise landed inside the docstring")
	}
	kept := "class S:\n    def f(self):\n        return 1\n"
	if pyWithTODO(kept) != kept {
		t.Errorf("a written body was touched:\n%s", pyWithTODO(kept))
	}
}

func TestGeneratePythonProducesValidPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	p := catalog.Problem{ID: 23, Slug: "merge-k-sorted-lists", Title: "Merge k Sorted Lists", Difficulty: "Hard",
		Pattern: "Linked List", Dir: "0023-merge-k-sorted-lists", LeetCodeURL: "https://leetcode.com/problems/merge-k-sorted-lists/"}
	f := GeneratePython(p, pyListStub, "Input: lists = [[1,4,5],[1,3,4],[2,6]]\nOutput: [1,1,2,3,4,4,5,6]\n")
	if f.FuncName != "mergeKLists" || !f.TableDriven || f.PracticeCases == 0 || f.Practice == "" {
		t.Fatalf("files = %+v", f)
	}
	if !strings.Contains(f.Solution, "class ListNode:") || !strings.Contains(f.Solution, "raise NotImplementedError") {
		t.Errorf("solution:\n%s", f.Solution)
	}
	if !strings.Contains(f.Test, "Input: lists") || !strings.Contains(f.Test, "Solution().mergeKLists(*args)") {
		t.Errorf("test:\n%s", f.Test)
	}
	dir := t.TempDir()
	for name, src := range map[string]string{"solution.py": f.Solution, "test_solution.py": f.Test, "practice_cases.py": f.Practice} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(python, "-I", "-B", "-c", "import ast,sys; ast.parse(open(sys.argv[1]).read())", filepath.Join(dir, name)).CombinedOutput()
		if err != nil {
			t.Errorf("%s does not parse: %v\n%s\n%s", name, err, out, src)
		}
	}
	design := GeneratePython(catalog.Problem{ID: 146, Slug: "lru-cache", Dir: "0146-lru-cache"}, pyDesignStub, "")
	if !strings.Contains(design.Practice, `"cls": "LRUCache"`) {
		t.Errorf("design practice suite:\n%s", design.Practice)
	}
	// The curated suite drives this problem, so the personal file must not run
	// anything: a skip would report missing coverage that is not missing.
	if strings.Contains(design.Test, "skipTest") || strings.Contains(design.Test, "\nclass ") {
		t.Errorf("design personal test should be a commented scaffold:\n%s", design.Test)
	}
}

// Starting a problem in Python beside its Go workspace adds the three Python
// files and leaves the Go ones exactly as they were.
func TestCreateInPythonLeavesTheGoWorkspaceAlone(t *testing.T) {
	root := t.TempDir()
	lay := workspace.Owner(root)
	p := catalog.Problem{ID: 1, Slug: "two-sum", Title: "Two Sum", Dir: "0001-two-sum"}
	goSig := "func twoSum(nums []int, target int) []int {\n    \n}\n"
	pySig := "class Solution:\n    def twoSum(self, nums: List[int], target: int) -> List[int]:\n        \n"
	if err := os.MkdirAll(filepath.Join(root, p.ContentDir()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, p.ContentDir(), "signature.py"), []byte(pySig), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := ownerScope(t, root)
	if _, _, err := Create(scope, lay, p, lang.Of(lang.Go), goSig, ""); err != nil {
		t.Fatal(err)
	}
	mine := "package twosum\n// my own work\n"
	if err := os.WriteFile(lay.Path(p, "solution.go"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	written, f, err := Create(scope, lay, p, lang.Of(lang.Python), pySig, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, w := range written {
		names = append(names, filepath.Base(w))
	}
	if strings.Join(names, ",") != "practice_cases.py,solution.py,test_solution.py" {
		t.Errorf("python create wrote %v", names)
	}
	if f.PracticeCases == 0 {
		t.Error("the Python scaffold carried no practice cases")
	}
	if got, _ := os.ReadFile(lay.Path(p, "solution.go")); string(got) != mine {
		t.Error("the Go solution was touched")
	}
	// A second Python start is a no-op too, and so is `nc tests` afterwards.
	if again, _, err := Create(scope, lay, p, lang.Of(lang.Python), pySig, ""); err != nil || len(again) != 0 {
		t.Errorf("second python create: %v %v", again, err)
	}
	if added, err := EnsurePracticeTests(lay, p, lang.Of(lang.Python)); err != nil || len(added) != 0 {
		t.Errorf("practice file written twice: %v %v", added, err)
	}

	// Reset archives the Python file as a Python attempt.
	if err := os.WriteFile(lay.Path(p, "solution.py"), []byte("class Solution:\n    pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, _, err := Reset(scope, lay, p, lang.Of(lang.Python), pySig, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadAttempt(scope, ref)
	if err != nil || a.Language != "python" || a.Code != "class Solution:\n    pass\n" {
		t.Errorf("archived attempt = %+v, %v", a, err)
	}
	if got, _ := os.ReadFile(lay.Path(p, "solution.py")); !strings.Contains(string(got), "raise NotImplementedError") {
		t.Error("reset did not put a fresh Python scaffold in place")
	}
}

// The typing import was unconditional, so 138 of the 150 problems opened with a
// name they never used — the first thing a reader questions, and the tutor did.
func TestPythonSolutionImportsTypingOnlyWhenItIsUsed(t *testing.T) {
	plain := GeneratePython(
		catalog.Problem{ID: 217, Slug: "contains-duplicate", Dir: "0217-contains-duplicate"},
		"class Solution:\n    def containsDuplicate(self, nums: list[int]) -> bool:\n        \n", "")
	if strings.Contains(plain.Solution, "from typing import") {
		t.Errorf("builtin annotations need no typing import:\n%s", plain.Solution)
	}

	// cloneGraph is annotated Optional['Node'], so the import has to be there.
	graph := GeneratePython(catalog.Problem{ID: 133, Slug: "clone-graph", Dir: "0133-clone-graph"}, pyGraphStub, "")
	if !strings.Contains(graph.Solution, "from typing import Optional") {
		t.Errorf("Optional is used and must be imported:\n%s", graph.Solution)
	}
	if strings.Contains(graph.Solution, "import List") {
		t.Errorf("List is unused here and must not be imported:\n%s", graph.Solution)
	}
}

// An empty personal table has only bad options, so when the curated suite
// already covers the problem the scaffold is written out commented: it reports
// no skip, and claims no pass it has not earned.
func TestPythonPersonalTestDoesNotSkipWhenTheSuiteCoversIt(t *testing.T) {
	f := GeneratePython(
		catalog.Problem{ID: 217, Slug: "contains-duplicate", Dir: "0217-contains-duplicate"},
		"class Solution:\n    def containsDuplicate(self, nums: list[int]) -> bool:\n        \n", "")
	if f.Practice == "" {
		t.Fatal("contains-duplicate has a curated suite; the fixture should have produced one")
	}
	if strings.Contains(f.Test, "skipTest") {
		t.Errorf("personal test still skips although the suite covers the examples:\n%s", f.Test)
	}
	for _, want := range []string{"# class TestContainsDuplicate", "# practice_cases.py beside this file"} {
		if !strings.Contains(f.Test, want) {
			t.Errorf("personal test is missing %q:\n%s", want, f.Test)
		}
	}
	// Every line is a comment or blank: nothing here runs.
	for _, line := range strings.Split(f.Test, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			t.Errorf("personal test has a live line %q", line)
		}
	}
}
