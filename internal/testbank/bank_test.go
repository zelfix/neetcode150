package testbank

import (
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/catalog"
)

var knownModes = map[string]bool{
	"function":      true,
	"operations":    true,
	"strings-codec": true,
	"tree-codec":    true,
}

// allSlugs reads the embedded cases/ directory and returns one slug per file.
func allSlugs(t *testing.T) []string {
	t.Helper()
	entries, err := files.ReadDir("cases")
	if err != nil {
		t.Fatal(err)
	}
	slugs := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			slugs = append(slugs, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return slugs
}

func TestLoadAllFixtures(t *testing.T) {
	for _, slug := range allSlugs(t) {
		t.Run(slug, func(t *testing.T) {
			s, err := Load(slug)
			if err != nil {
				t.Fatalf("Load(%q): %v", slug, err)
			}
			if s.Slug != slug {
				t.Errorf("Suite.Slug = %q, want %q", s.Slug, slug)
			}
		})
	}
}

func TestFixtureStructure(t *testing.T) {
	// Collect all violations before failing so a single run shows every problem.
	var violations []string
	add := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	for _, slug := range allSlugs(t) {
		s, err := Load(slug)
		if err != nil {
			add("%s: Load error: %v", slug, err)
			continue
		}
		if s.Entry == "" {
			add("%s: empty Entry", slug)
		}
		if !knownModes[s.Mode] {
			add("%s: unknown Mode %q", slug, s.Mode)
		}
		if len(s.Cases) == 0 {
			add("%s: no cases", slug)
		}
		seen := make(map[string]bool, len(s.Cases))
		for _, c := range s.Cases {
			if c.Name == "" {
				add("%s: case with empty Name", slug)
			}
			if seen[c.Name] {
				add("%s/%s: duplicate Name", slug, c.Name)
			}
			seen[c.Name] = true
			switch s.Mode {
			case "function":
				if len(c.Want) == 0 {
					add("%s/%s: Want not set", slug, c.Name)
				}
			case "operations":
				if len(c.Ops) == 0 {
					add("%s/%s: Ops is empty", slug, c.Name)
				}
				if len(c.Ops) != len(c.Calls) {
					add("%s/%s: len(Ops)=%d != len(Calls)=%d", slug, c.Name, len(c.Ops), len(c.Calls))
				}
			}
		}
	}

	for _, v := range violations {
		t.Error(v)
	}
}

func TestCatalogCoverage(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(filepath.Join(root, "content", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}

	fixtureSet := make(map[string]bool)
	for _, slug := range allSlugs(t) {
		fixtureSet[slug] = true
	}
	catalogSet := make(map[string]bool)
	for _, p := range cat.Problems {
		catalogSet[p.Slug] = true
	}

	for _, p := range cat.Problems {
		if !fixtureSet[p.Slug] {
			t.Errorf("catalog slug %q has no fixture", p.Slug)
		}
	}
	for slug := range fixtureSet {
		if !catalogSet[slug] {
			t.Errorf("fixture %q has no corresponding catalog entry (orphan)", slug)
		}
	}
}

func TestSourceIsGofmtClean(t *testing.T) {
	cases := []struct {
		name string
		slug string
	}{
		{"function", "best-time-to-buy-and-sell-stock"},
		{"strings-codec", "encode-and-decode-strings"},
		{"tree-codec", "serialize-and-deserialize-binary-tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := TestSource("solution", tc.slug)
			if err != nil {
				t.Fatalf("TestSource: %v", err)
			}
			if !strings.Contains(src, "testkit.Run(") {
				t.Error("output does not contain testkit.Run(")
			}
			// A package declaration is the only required top-level construct.
			if !strings.Contains(src, "package solution") {
				t.Error("output missing package clause")
			}
			// format.Source is a no-op on already-formatted code; any diff means
			// the generator produced something gofmt would rewrite.
			formatted, err := format.Source([]byte(src))
			if err != nil {
				t.Fatalf("go/format.Source: %v", err)
			}
			if string(formatted) != src {
				t.Error("output is not gofmt-clean (format.Source changed it)")
			}
		})
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load("no-such-slug")
	if err == nil {
		t.Error("Load(no-such-slug) returned nil error, want an error")
	}
}
