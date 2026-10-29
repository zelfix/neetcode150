package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitFrontmatter(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantKeys map[string]string
		wantBody string
	}{
		{
			name:     "keys are parsed and the body starts after the fence",
			raw:      "---\ntitle: Two Sum\ndifficulty: Easy\n---\n\n# Two Sum\n\nbody\n",
			wantKeys: map[string]string{"title": "Two Sum", "difficulty": "Easy"},
			wantBody: "# Two Sum\n\nbody\n",
		},
		{
			name:     "a file without frontmatter is returned unchanged",
			raw:      "# Just Markdown\n",
			wantKeys: map[string]string{},
			wantBody: "# Just Markdown\n",
		},
		{
			name:     "values keep colons and are unquoted",
			raw:      "---\nleetcode: https://leetcode.com/problems/two-sum/\nname: \"Quoted\"\n---\nbody\n",
			wantKeys: map[string]string{"leetcode": "https://leetcode.com/problems/two-sum/", "name": "Quoted"},
			wantBody: "body\n",
		},
		{
			name:     "a stray --- later in the file is not frontmatter",
			raw:      "intro\n\n---\n\nmore\n",
			wantKeys: map[string]string{},
			wantBody: "intro\n\n---\n\nmore\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm, body := SplitFrontmatter(tc.raw)
			for k, want := range tc.wantKeys {
				if fm[k] != want {
					t.Errorf("frontmatter[%q] = %q, want %q", k, fm[k], want)
				}
			}
			if len(fm) != len(tc.wantKeys) {
				t.Errorf("got %d frontmatter keys, want %d: %v", len(fm), len(tc.wantKeys), fm)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestDocMissingFileIsNotAnError(t *testing.T) {
	s := NewStore(t.TempDir(), NewRenderer())
	doc, err := s.Doc("content/problems/nope/hints.md")
	if err != nil {
		t.Fatalf("Doc: %v", err)
	}
	if !doc.Missing || !doc.Empty() {
		t.Errorf("missing file came back as %+v", doc)
	}
}

func TestDocPicksUpEditsAfterCaching(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.md")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root, NewRenderer())

	if doc, err := s.Doc("notes.md"); err != nil || !strings.Contains(doc.Body, "first") {
		t.Fatalf("first read: %+v %v", doc, err)
	}
	// A rewrite with a bumped mtime must invalidate the cache.
	if err := os.WriteFile(path, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Doc("notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Body, "second") {
		t.Errorf("stale cache: body = %q", doc.Body)
	}
}

func TestHintsSplitOnHeadings(t *testing.T) {
	root := t.TempDir()
	md := "---\ntitle: Hints\n---\n\nRead the constraints first.\n\n## Hint 1 — pattern\n\nThink about a hash map.\n\n## Hint 2 — invariant\n\nOne pass is enough.\n"
	if err := os.WriteFile(filepath.Join(root, "hints.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root, NewRenderer())

	pre, hints, err := s.Hints("hints.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pre), "constraints") {
		t.Errorf("preamble = %q", pre)
	}
	if len(hints) != 2 {
		t.Fatalf("got %d hints, want 2", len(hints))
	}
	if hints[0].Title != "Hint 1 — pattern" {
		t.Errorf("first title = %q", hints[0].Title)
	}
	if !strings.Contains(string(hints[1].HTML), "One pass") {
		t.Errorf("second hint body = %q", hints[1].HTML)
	}
	if strings.Contains(string(hints[0].HTML), "One pass") {
		t.Error("hint 1 leaked the content of hint 2")
	}
}

func TestHintsWithoutHeadingsBecomeOneReveal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hints.md"), []byte("just one thought\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root, NewRenderer())
	_, hints, err := s.Hints("hints.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 1 || hints[0].Title != "Hint" {
		t.Errorf("got %+v, want a single generic reveal", hints)
	}
}

func TestRenderHighlightsGoAndLeavesPlainBlocksAlone(t *testing.T) {
	r := NewRenderer()
	out := string(r.MustRender("```go\nfunc main() {}\n```\n"))
	if !strings.Contains(out, `data-lang="go"`) || !strings.Contains(out, "<span") {
		t.Errorf("go block was not highlighted: %s", out)
	}
	plain := string(r.MustRender("```\nInput: nums = [1,2]\n```\n"))
	if !strings.Contains(plain, "code-plain") {
		t.Errorf("language-less block should render plain: %s", plain)
	}
	if strings.Contains(plain, "<span") {
		t.Errorf("language-less block should not be tokenised: %s", plain)
	}
	if css := r.HighlightCSS(); !strings.Contains(string(css), "chroma") {
		t.Error("highlight stylesheet is empty")
	}
}

func TestRenderSupportsGFMTables(t *testing.T) {
	out := string(NewRenderer().MustRender("| a | b |\n| --- | --- |\n| 1 | 2 |\n"))
	if !strings.Contains(out, "<table>") {
		t.Errorf("table not rendered: %s", out)
	}
}

func TestStripLeadingH1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# 1. Two Sum\n\nbody\n", "body\n"},
		{"\n\n# Title\nbody\n", "body\n"},
		{"body with no heading\n", "body with no heading\n"},
		{"## Not an H1\n\nbody\n", "## Not an H1\n\nbody\n"},
		{"intro\n\n# Later Heading\n", "intro\n\n# Later Heading\n"},
	}
	for _, tc := range cases {
		if got := StripLeadingH1(tc.in); got != tc.want {
			t.Errorf("StripLeadingH1(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDocHTMLBodyDropsOnlyTheTitle(t *testing.T) {
	root := t.TempDir()
	md := "---\ntitle: Two Sum\n---\n\n# 1. Two Sum\n\nFind the indices.\n\n## Examples\n\nhere\n"
	if err := os.WriteFile(filepath.Join(root, "problem.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := NewStore(root, NewRenderer()).Doc("problem.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc.HTML), "<h1") {
		t.Error("HTML should keep the title heading")
	}
	if strings.Contains(string(doc.HTMLBody), "<h1") {
		t.Errorf("HTMLBody should not repeat the title: %s", doc.HTMLBody)
	}
	for _, want := range []string{"Find the indices", "Examples", "here"} {
		if !strings.Contains(string(doc.HTMLBody), want) {
			t.Errorf("HTMLBody lost %q", want)
		}
	}
	if doc.Title() != "Two Sum" {
		t.Errorf("Title() = %q", doc.Title())
	}
}
