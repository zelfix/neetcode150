package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/catalog"
	"neetcode150/internal/testbank"
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
	if !strings.Contains(design.Test, "skipTest") || !strings.Contains(design.Practice, `"cls": "LRUCache"`) {
		t.Errorf("design files:\n%s\n%s", design.Test, design.Practice)
	}
}
