package testkit

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"neetcode150/internal/testbank"
)

func fixture(args string, want string) testbank.Case {
	var a []json.RawMessage
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		panic(err)
	}
	return testbank.Case{Args: a, Want: json.RawMessage(want)}
}

type policyCase struct {
	name, policy, args, want string
	fn                       any
	valid                    bool
}

func policyCases() []policyCase {
	return []policyCase{
		{"unordered pair", "unordered", `[[2,7],9]`, `[0,1]`, func([]int, int) []int { return []int{1, 0} }, true},
		{"duplicate result", "unordered", `[[2,7],9]`, `[0,1]`, func([]int, int) []int { return []int{0, 0} }, false},
		{"group order", "unordered-groups", `[]`, `[["a","b"],["c"]]`, func() [][]string { return [][]string{{"c"}, {"b", "a"}} }, true},
		{"inner order matters", "unordered", `[]`, `[[1,2],[2,1]]`, func() [][]int { return [][]int{{1, 2}, {1, 2}} }, false},
		{"alternative palindrome", "palindrome", `["babad"]`, `"bab"`, func(string) string { return "aba" }, true},
		{"nonpalindrome", "palindrome", `["babad"]`, `"bab"`, func(string) string { return "bad" }, false},
		{"nonmaximal palindrome", "palindrome", `["babad"]`, `"bab"`, func(string) string { return "b" }, false},
		{"other topological order", "topological", `[3,[[2,0],[2,1]]]`, `[0,1,2]`, func(int, [][]int) []int { return []int{1, 0, 2} }, true},
		{"invalid topological order", "topological", `[3,[[2,0],[2,1]]]`, `[0,1,2]`, func(int, [][]int) []int { return []int{2, 1, 0} }, false},
		{"duplicate course", "topological", `[3,[]]`, `[0,1,2]`, func(int, [][]int) []int { return []int{0, 0, 2} }, false},
		{"empty order on cycle", "topological", `[2,[[0,1],[1,0]]]`, `[]`, func(int, [][]int) []int { return nil }, true},
		{"independent alphabet letters", "alphabet", `[["za","zb"]]`, `"zab"`, func([]string) string { return "abz" }, true},
		{"alphabet edge violated", "alphabet", `[["za","zb"]]`, `"zab"`, func([]string) string { return "baz" }, false},
		{"alphabet duplicate", "alphabet", `[["za","zb"]]`, `"zab"`, func([]string) string { return "aab" }, false},
		{"nil empty slice", "", `[]`, `[]`, func() []int { return nil }, true},
		{"integer comparison exact", "", `[]`, `1000000000`, func() int { return 1000000001 }, false},
		{"float rounding", "float", `[]`, `9.261`, func() float64 { return 9.261000000000001 }, true},
		{"tiny nonzero must not equal zero", "float", `[]`, `9.094947017729282e-13`, func() float64 { return 0 }, false},
	}
}

func TestPoliciesAcceptAlternativesAndRejectWrongAnswers(t *testing.T) {
	for _, tc := range policyCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCase(testbank.Suite{Mode: "function", Policy: tc.policy}, fixture(tc.args, tc.want), false, tc.fn)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

type listNode struct {
	Val  int
	Next *listNode
}
type treeNode struct {
	Val         int
	Left, Right *treeNode
}
type graphNode struct {
	Val       int
	Neighbors []*graphNode
}
type randomNode struct {
	Val          int
	Next, Random *randomNode
}

func TestNodeFixturesRoundTripAndRejectCycles(t *testing.T) {
	for _, tc := range []struct {
		raw string
		typ reflect.Type
	}{
		{`[1,2,3]`, reflect.TypeFor[*listNode]()},
		{`[1,null,2,3,null,null,4]`, reflect.TypeFor[*treeNode]()},
		{`[[2,3],[1,3],[1,2]]`, reflect.TypeFor[*graphNode]()},
		{`[[7,null],[7,0],[8,2]]`, reflect.TypeFor[*randomNode]()},
	} {
		v, err := decode(json.RawMessage(tc.raw), tc.typ)
		if err != nil {
			t.Fatal(err)
		}
		out, err := normalize(v)
		if err != nil {
			t.Fatal(err)
		}
		var want any
		_ = json.Unmarshal([]byte(tc.raw), &want)
		if !equivalent(out, want, "") {
			t.Fatalf("round trip %s: %v", tc.raw, out)
		}
	}
	cyclic := &listNode{Val: 1}
	cyclic.Next = cyclic
	if _, err := normalize(reflect.ValueOf(cyclic)); err == nil {
		t.Fatal("list cycle accepted")
	}
	shared := &treeNode{Val: 2}
	root := &treeNode{Val: 1, Left: shared, Right: shared}
	if _, err := normalize(reflect.ValueOf(root)); err == nil {
		t.Fatal("shared tree node accepted")
	}
}

func TestCloneMustBeIndependentAndPreserveOriginal(t *testing.T) {
	c := fixture(`[[[2],[1]]]`, `[[2],[1]]`)
	s := testbank.Suite{Mode: "function", Policy: "graph-clone"}
	if err := checkCase(s, c, false, func(n *graphNode) *graphNode { return n }); err == nil {
		t.Fatal("alias accepted as clone")
	}
	clone := func() *graphNode {
		a, b := &graphNode{Val: 1}, &graphNode{Val: 2}
		a.Neighbors = []*graphNode{b}
		b.Neighbors = []*graphNode{a}
		return a
	}
	if err := checkCase(s, c, false, func(*graphNode) *graphNode { return clone() }); err != nil {
		t.Fatal(err)
	}
	if err := checkCase(s, c, false, func(*graphNode) *graphNode { return clone().Neighbors[0] }); err == nil {
		t.Fatal("copy returned from the wrong starting node accepted")
	}
	if err := checkCase(s, c, false, func(n *graphNode) *graphNode { n.Neighbors = nil; return clone() }); err == nil {
		t.Fatal("original mutation accepted")
	}
	c = fixture(`[[[3,1],[4,0]]]`, `[[3,1],[4,0]]`)
	s.Policy = "random-clone"
	if err := checkCase(s, c, false, func(n *randomNode) *randomNode { return &randomNode{Val: 3, Next: &randomNode{Val: 4}, Random: n.Next} }); err == nil {
		t.Fatal("random pointer into original accepted")
	}
}

func TestMutationCycleAndAncestorFixtures(t *testing.T) {
	s := testbank.Suite{Mode: "function", Mutates: true}
	c := fixture(`[[["O"]]]`, `[["X"]]`)
	if err := checkCase(s, c, false, func(b [][]byte) { b[0][0] = 'X' }); err != nil {
		t.Fatal(err)
	}
	if err := checkCase(s, c, false, func(b [][]byte) {}); err == nil {
		t.Fatal("no mutation accepted")
	}
	s = testbank.Suite{Mode: "function", Policy: "ancestor"}
	c = fixture(`[[2,1,3],1,3]`, `2`)
	if err := checkCase(s, c, false, func(root, p, q *treeNode) *treeNode {
		if p != root.Left || q != root.Right {
			panic("arguments are not original nodes")
		}
		return root
	}); err != nil {
		t.Fatal(err)
	}
	if err := checkCase(s, c, false, func(root, p, q *treeNode) *treeNode { return &treeNode{Val: 2} }); err == nil {
		t.Fatal("new ancestor node accepted")
	}
	s.Policy = "cycle"
	c = fixture(`[[1,2]]`, `true`)
	pos := 1
	c.CyclePos = &pos
	if err := checkCase(s, c, false, func(n *listNode) bool { return n.Next == n.Next.Next }); err != nil {
		t.Fatal(err)
	}
}

type counter struct{ value int }

func (c *counter) Add(n int)     { c.value += n }
func (c *counter) Value() int    { return c.value }
func (c *counter) Half() float64 { return float64(c.value) / 2 }

func TestOperationSequencesAndPanicReporting(t *testing.T) {
	var c testbank.Case
	err := json.Unmarshal([]byte(`{"ops":["Counter","add","value","half"],"calls":[[2],[3],[],[]],"want":[null,null,5,2.5]}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	ctor := func(n int) counter { return counter{n} }
	s := testbank.Suite{Mode: "operations"}
	if err := checkCase(s, c, false, ctor); err != nil {
		t.Fatal(err)
	}
	c.Calls[1] = nil
	if err := checkCase(s, c, true, ctor); err == nil {
		t.Fatal("invalid method arguments accepted")
	}
	err = checkCase(testbank.Suite{Mode: "function"}, fixture(`[]`, `0`), false, func() int { panic("TODO") })
	if err == nil || !strings.Contains(err.Error(), "TODO") {
		t.Fatalf("lost panic: %v", err)
	}
}

func TestFailureNamesTheInput(t *testing.T) {
	c := fixture(`[[2, 7, 11, 15], 9]`, `[0,1]`)
	if got := describeInput(testbank.Suite{Mode: "function"}, c); got != "input: [2,7,11,15], 9" {
		t.Errorf("function input: %q", got)
	}
	pos := 1
	c.CyclePos = &pos
	if got := describeInput(testbank.Suite{Policy: "cycle"}, c); got != "input: [2,7,11,15], 9; tail links back to index 1" {
		t.Errorf("cycle input: %q", got)
	}
	var ops testbank.Case
	if err := json.Unmarshal([]byte(`{"ops":["Counter","add","value"],"calls":[[2],[3],[]],"want":[null,null,5]}`), &ops); err != nil {
		t.Fatal(err)
	}
	if got := describeInput(testbank.Suite{Mode: "operations"}, ops); got != "ops: Counter(2), add(3), value()" {
		t.Errorf("operations input: %q", got)
	}
	long := fixture(`["`+strings.Repeat("x", 1000)+`"]`, `0`)
	if got := describeInput(testbank.Suite{}, long); len([]rune(got)) != inputLimit+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("long input not clipped: %d runes", len([]rune(got)))
	}
}

func TestCodecsCheckRoundTripWithoutPrescribingFormat(t *testing.T) {
	c := fixture(`[["","a#"]]`, `["","a#"]`)
	s := testbank.Suite{Mode: "strings-codec"}
	encode := func([]string) string { return "arbitrary format" }
	decode := func(data string) []string {
		if data != "arbitrary format" {
			panic("lost encoded output")
		}
		return []string{"", "a#"}
	}
	if err := checkCase(s, c, false, encode, decode); err != nil {
		t.Fatal(err)
	}
	if err := checkCase(s, c, false, encode, func(string) []string { return []string{"a#"} }); err == nil {
		t.Fatal("codec losing an empty string accepted")
	}
	c = fixture(`[[0,null,-1]]`, `[0,null,-1]`)
	s.Mode = "tree-codec"
	if err := checkCase(s, c, false, func(*treeNode) string { return "tree" }, func(string) *treeNode { return &treeNode{Val: 0, Right: &treeNode{Val: -1}} }); err != nil {
		t.Fatal(err)
	}
}

func TestTopologicalValidationUsesOriginalPrerequisites(t *testing.T) {
	s := testbank.Suite{Mode: "function", Policy: "topological"}
	c := fixture(`[2,[[1,0]]]`, `[0,1]`)
	err := checkCase(s, c, false, func(_ int, edges [][]int) []int {
		edges[0][0], edges[0][1] = 0, 1
		return []int{1, 0}
	})
	if err == nil {
		t.Fatal("mutated prerequisites hid an invalid order")
	}
}

// The Python harness in py/ reimplements every policy, and two hand-kept
// copies of eighteen rules will drift. testdata/policy-cases.json is the one
// table both suites run; this test pins the Go table above to it, so a case
// added to one side without the other fails here rather than months later.
func TestPolicyTableMatchesTheSharedFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/policy-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var shared []struct {
		Name, Policy string
		Args, Want   json.RawMessage
		Returns      json.RawMessage
		Valid        bool
	}
	if err := json.Unmarshal(data, &shared); err != nil {
		t.Fatal(err)
	}
	goCases := policyCases()
	if len(shared) != len(goCases) {
		t.Fatalf("shared table has %d cases, the Go table %d", len(shared), len(goCases))
	}
	for i, row := range shared {
		tc := goCases[i]
		if tc.name != row.Name || tc.policy != row.Policy || tc.valid != row.Valid {
			t.Errorf("row %d: shared %q/%q/%v, Go %q/%q/%v", i, row.Name, row.Policy, row.Valid, tc.name, tc.policy, tc.valid)
			continue
		}
		var a, b any
		_ = json.Unmarshal([]byte(tc.args), &a)
		_ = json.Unmarshal(row.Args, &b)
		if !equivalent(a, b, "") {
			t.Errorf("%s: args differ: %s vs %s", row.Name, tc.args, row.Args)
		}
		_ = json.Unmarshal([]byte(tc.want), &a)
		_ = json.Unmarshal(row.Want, &b)
		if !equivalent(a, b, "") {
			t.Errorf("%s: want differs: %s vs %s", row.Name, tc.want, row.Want)
		}
		// The Go closures ignore their input, so calling them with the fixture
		// arguments yields the constant the shared table says they return.
		fn := reflect.ValueOf(tc.fn)
		var raw []json.RawMessage
		_ = json.Unmarshal([]byte(tc.args), &raw)
		args, err := arguments(raw, fn.Type())
		if err != nil {
			t.Fatalf("%s: %v", row.Name, err)
		}
		got, err := normalize(fn.Call(args)[0])
		if err != nil {
			t.Fatal(err)
		}
		var returns any
		_ = json.Unmarshal(row.Returns, &returns)
		if !equivalent(got, returns, "") {
			t.Errorf("%s: Go returns %v, shared table says %s", row.Name, got, row.Returns)
		}
	}
}
