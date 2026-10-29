package catalog

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Arrays & Hashing":        "arrays-hashing",
		"Heap / Priority Queue":   "heap-priority-queue",
		"1-D Dynamic Programming": "1-d-dynamic-programming",
		"Math & Geometry":         "math-geometry",
		"Stack":                   "stack",
		"  padded  ":              "padded",
		"&&&":                     "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPatternIndexOrdersTheRoadmap(t *testing.T) {
	if PatternIndex("Arrays & Hashing") != 0 {
		t.Error("Arrays & Hashing should come first")
	}
	if PatternIndex("Bit Manipulation") != len(PatternOrder)-1 {
		t.Error("Bit Manipulation should come last")
	}
	if got := PatternIndex("Not A Pattern"); got != len(PatternOrder) {
		t.Errorf("unknown pattern sorts to %d, want %d", got, len(PatternOrder))
	}
	if PatternIndex("Graphs") >= PatternIndex("Advanced Graphs") {
		t.Error("Graphs must come before Advanced Graphs")
	}
}

func testCatalog() *Catalog {
	return New([]Problem{
		{ID: 217, Slug: "contains-duplicate", Title: "Contains Duplicate", Pattern: "Arrays & Hashing", PatternSlug: "arrays-hashing", Order: 0, Dir: "0217-contains-duplicate"},
		{ID: 1, Slug: "two-sum", Title: "Two Sum", Pattern: "Arrays & Hashing", PatternSlug: "arrays-hashing", Order: 1, Dir: "0001-two-sum"},
		{ID: 167, Slug: "two-sum-ii-input-array-is-sorted", Title: "Two Sum II", Pattern: "Two Pointers", PatternSlug: "two-pointers", PatternIdx: 1, Order: 2, Dir: "0167-two-sum-ii-input-array-is-sorted"},
	})
}

func TestResolve(t *testing.T) {
	c := testCatalog()
	cases := []struct {
		ref      string
		wantSlug string
	}{
		{"two-sum", "two-sum"},                             // exact slug beats the substring match
		{"0001-two-sum", "two-sum"},                        // directory name
		{"1", "two-sum"},                                   // leetcode id
		{"217", "contains-duplicate"},                      // leetcode id
		{"Contains Duplicate", "contains-duplicate"},       // full title
		{"contains dup", "contains-duplicate"},             // partial title, spaces
		{"two-sum-ii", "two-sum-ii-input-array-is-sorted"}, // unique prefix
	}
	for _, tc := range cases {
		got, err := c.Resolve(tc.ref)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.ref, err)
			continue
		}
		if got.Slug != tc.wantSlug {
			t.Errorf("Resolve(%q) = %q, want %q", tc.ref, got.Slug, tc.wantSlug)
		}
	}
}

func TestResolveReportsAmbiguityAndMisses(t *testing.T) {
	c := testCatalog()
	if _, err := c.Resolve("two sum"); err == nil {
		t.Error("an ambiguous reference should be an error, not a guess")
	} else if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should say it is ambiguous: %v", err)
	}
	if _, err := c.Resolve("nonesuch"); err == nil {
		t.Error("an unknown reference should be an error")
	}
	if _, err := c.Resolve("  "); err == nil {
		t.Error("an empty reference should be an error")
	}
}

func TestGroupsFollowCurriculumOrder(t *testing.T) {
	c := testCatalog()
	groups := c.Groups()
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].Pattern != "Arrays & Hashing" || groups[1].Pattern != "Two Pointers" {
		t.Errorf("groups out of order: %q then %q", groups[0].Pattern, groups[1].Pattern)
	}
	if len(groups[0].Problems) != 2 {
		t.Errorf("first group has %d problems, want 2", len(groups[0].Problems))
	}
	if _, ok := c.Group("two-pointers"); !ok {
		t.Error("Group(two-pointers) not found")
	}
	if _, ok := c.Group("nope"); ok {
		t.Error("Group(nope) should not be found")
	}
}

func TestProblemPaths(t *testing.T) {
	p := Problem{Dir: "0001-two-sum", VideoID: "abc123"}
	if got := p.ContentDir(); got != filepath.Join("content", "problems", "0001-two-sum") {
		t.Errorf("ContentDir() = %q", got)
	}
	if got := p.SolutionDir(); got != filepath.Join("solutions", "0001-two-sum") {
		t.Errorf("SolutionDir() = %q", got)
	}
	if got := p.VideoURL(); got != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("VideoURL() = %q", got)
	}
	if got := (Problem{}).VideoURL(); got != "" {
		t.Errorf("a problem with no video should have no URL, got %q", got)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "problems.json")
	if err := testCatalog().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Problems) != 3 {
		t.Fatalf("loaded %d problems, want 3", len(loaded.Problems))
	}
	if p, ok := loaded.BySlug("two-sum"); !ok || p.ID != 1 {
		t.Errorf("BySlug(two-sum) = %+v, %v", p, ok)
	}
}

func TestLoadMissingFileExplainsItself(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "problems.json"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "nc fetch") {
		t.Errorf("error should point at `nc fetch`: %v", err)
	}
}
