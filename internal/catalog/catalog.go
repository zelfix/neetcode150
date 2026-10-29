// Package catalog holds the NeetCode 150 problem list: its on-disk form, the
// curriculum ordering, and lookups used by the CLI and the site.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PatternOrder is NeetCode's own roadmap sequence. "What should I do next"
// follows this, so prerequisites land before the patterns that build on them.
var PatternOrder = []string{
	"Arrays & Hashing",
	"Two Pointers",
	"Sliding Window",
	"Stack",
	"Binary Search",
	"Linked List",
	"Trees",
	"Tries",
	"Heap / Priority Queue",
	"Backtracking",
	"Graphs",
	"Advanced Graphs",
	"1-D Dynamic Programming",
	"2-D Dynamic Programming",
	"Greedy",
	"Intervals",
	"Math & Geometry",
	"Bit Manipulation",
}

// Problem is one of the 150, as stored in content/problems.json.
type Problem struct {
	ID          int      `json:"id"`   // LeetCode frontend id
	Slug        string   `json:"slug"` // LeetCode title slug
	Title       string   `json:"title"`
	Pattern     string   `json:"pattern"`
	PatternSlug string   `json:"patternSlug"`
	PatternIdx  int      `json:"patternIndex"` // position of Pattern in PatternOrder
	Order       int      `json:"order"`        // 0-based curriculum position, 0..149
	Difficulty  string   `json:"difficulty"`   // Easy | Medium | Hard
	Blind75     bool     `json:"blind75"`
	Premium     bool     `json:"premium"` // LeetCode premium: statement written by hand
	VideoID     string   `json:"video"`   // YouTube id of the NeetCode walkthrough
	LeetCodeURL string   `json:"leetcode"`
	Dir         string   `json:"dir"`              // e.g. "0001-two-sum"
	Topics      []string `json:"topics,omitempty"` // LeetCode topic tags
}

// VideoURL is the NeetCode walkthrough for the problem, or "" if none is known.
func (p Problem) VideoURL() string {
	if p.VideoID == "" {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + p.VideoID
}

// ContentDir is the problem's content directory, relative to the project root.
func (p Problem) ContentDir() string { return filepath.Join("content", "problems", p.Dir) }

// SolutionDir is the problem's solution directory, relative to the project root.
func (p Problem) SolutionDir() string { return filepath.Join("solutions", p.Dir) }

// Catalog is the ordered list of problems plus lookups.
type Catalog struct {
	Problems []Problem
	bySlug   map[string]*Problem
	byDir    map[string]*Problem
}

// PatternGroup is one pattern with its problems, in curriculum order.
type PatternGroup struct {
	Pattern  string
	Slug     string
	Index    int
	Problems []Problem
}

// New indexes problems into a Catalog. The slice is kept in the order given.
func New(problems []Problem) *Catalog {
	c := &Catalog{
		Problems: problems,
		bySlug:   make(map[string]*Problem, len(problems)),
		byDir:    make(map[string]*Problem, len(problems)),
	}
	for i := range c.Problems {
		p := &c.Problems[i]
		c.bySlug[p.Slug] = p
		c.byDir[p.Dir] = p
	}
	return c
}

// Load reads a catalog from a problems.json file.
func Load(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s not found — run `nc fetch` first", path)
		}
		return nil, err
	}
	var problems []Problem
	if err := json.Unmarshal(b, &problems); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(problems) == 0 {
		return nil, fmt.Errorf("%s is empty — run `nc fetch`", path)
	}
	return New(problems), nil
}

// Save writes the catalog to path, creating parent directories as needed.
func (c *Catalog) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c.Problems, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(b, '\n'))
}

// BySlug returns the problem with the given LeetCode slug.
func (c *Catalog) BySlug(slug string) (Problem, bool) {
	if p, ok := c.bySlug[slug]; ok {
		return *p, true
	}
	return Problem{}, false
}

// Resolve finds a problem by slug, directory name ("0001-two-sum"), numeric id,
// or unambiguous title/slug substring, so the CLI can take a loose argument.
func (c *Catalog) Resolve(ref string) (Problem, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Problem{}, errors.New("no problem given")
	}
	if p, ok := c.bySlug[ref]; ok {
		return *p, nil
	}
	if p, ok := c.byDir[ref]; ok {
		return *p, nil
	}
	norm := strings.ToLower(strings.ReplaceAll(ref, " ", "-"))
	var hits []Problem
	for _, p := range c.Problems {
		if fmt.Sprint(p.ID) == ref {
			return p, nil
		}
		if strings.Contains(p.Slug, norm) || strings.Contains(strings.ToLower(p.Title), strings.ToLower(ref)) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return Problem{}, fmt.Errorf("no problem matches %q", ref)
	case 1:
		return hits[0], nil
	default:
		names := make([]string, 0, len(hits))
		for _, p := range hits {
			names = append(names, p.Slug)
		}
		if len(names) > 8 {
			names = append(names[:8], "…")
		}
		return Problem{}, fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(names, ", "))
	}
}

// Groups returns the problems grouped by pattern, in curriculum order.
func (c *Catalog) Groups() []PatternGroup {
	groups := make([]PatternGroup, 0, len(PatternOrder))
	index := make(map[string]int, len(PatternOrder))
	for _, p := range c.Problems {
		i, ok := index[p.Pattern]
		if !ok {
			i = len(groups)
			index[p.Pattern] = i
			groups = append(groups, PatternGroup{Pattern: p.Pattern, Slug: p.PatternSlug, Index: p.PatternIdx})
		}
		groups[i].Problems = append(groups[i].Problems, p)
	}
	return groups
}

// Group returns the single pattern group with the given pattern slug.
func (c *Catalog) Group(patternSlug string) (PatternGroup, bool) {
	for _, g := range c.Groups() {
		if g.Slug == patternSlug {
			return g, true
		}
	}
	return PatternGroup{}, false
}

// Slugify turns a display name into a URL/file-safe slug.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// PatternIndex is the position of a pattern in PatternOrder, or len(PatternOrder)
// for a pattern that is not part of the known roadmap.
func PatternIndex(pattern string) int {
	for i, p := range PatternOrder {
		if p == pattern {
			return i
		}
	}
	return len(PatternOrder)
}

// FindRoot walks up from the working directory to the project root, the
// directory holding go.mod.
func FindRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("project root not found: no go.mod in this directory or any parent")
		}
		dir = parent
	}
}

// WriteFileAtomic writes data to path via a temp file and a rename, so a
// crash or a concurrent reader never sees a half-written file.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
