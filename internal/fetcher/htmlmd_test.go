package fetcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLToMarkdownUnits(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "exponent survives inside a code span",
			in:   `<p><code>1 &lt;= n &lt;= 10<sup>4</sup></code></p>`,
			want: "`1 <= n <= 10^4`\n",
		},
		{
			name: "bare exponent outside a code span",
			in:   `<p>up to 2<sup>31</sup> - 1</p>`,
			want: "up to 2^31 - 1\n",
		},
		{
			name: "emphasis keeps its markers next to the words",
			in:   `<p>this is <strong> important </strong>text</p>`,
			want: "this is **important** text\n",
		},
		{
			name: "markdown metacharacters in prose are escaped",
			in:   `<p>a_b and c*d and [e]</p>`,
			want: "a\\_b and c\\*d and \\[e\\]\n",
		},
		{
			name: "code spans are not escaped",
			in:   `<p><code>a_b*c</code></p>`,
			want: "`a_b*c`\n",
		},
		{
			name: "a code span containing a backtick widens the fence",
			in:   "<p><code>x = `y`</code></p>",
			want: "`` x = `y` ``\n",
		},
		{
			name: "pre becomes a fenced block with its newlines intact",
			in:   "<pre>Input: nums = [1,2]\nOutput: 3\n</pre>",
			want: "```\nInput: nums = [1,2]\nOutput: 3\n```\n",
		},
		{
			name: "nested lists are indented under their item",
			in:   `<ul><li>outer<ul><li>inner</li></ul></li><li>second</li></ul>`,
			want: "- outer\n  - inner\n- second\n",
		},
		{
			name: "ordered lists are numbered",
			in:   `<ol><li>first</li><li>second</li></ol>`,
			want: "1. first\n2. second\n",
		},
		{
			name: "br is a hard break inside a paragraph",
			in:   `<p>one<br/>two</p>`,
			want: "one\ntwo\n",
		},
		{
			name: "non-breaking space becomes an ordinary space",
			in:   "<p>a&nbsp;b</p>",
			want: "a b\n",
		},
		{
			name: "images keep their source",
			in:   `<p><img alt="tree" src="https://example.com/a.png" /></p>`,
			want: "![tree](https://example.com/a.png)\n",
		},
		{
			name: "links are rendered inline",
			in:   `<p>see <a href="https://x.test/d">docs</a></p>`,
			want: "see [docs](https://x.test/d)\n",
		},
		{
			name: "tables become GFM",
			in:   `<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>`,
			want: "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
		},
		{
			name: "empty input yields empty output",
			in:   ``,
			want: "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HTMLToMarkdown(tc.in)
			if err != nil {
				t.Fatalf("HTMLToMarkdown: %v", err)
			}
			if got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestHTMLToMarkdownFixtures converts statements saved from LeetCode and checks
// the properties that matter for study: nothing HTML survives, exponents are
// preserved, and the examples and constraints sections are still present.
func TestHTMLToMarkdownFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no fixtures saved")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			md, err := HTMLToMarkdown(string(raw))
			if err != nil {
				t.Fatalf("HTMLToMarkdown: %v", err)
			}
			for _, leak := range []string{"<p>", "</p>", "<code>", "<strong>", "<sup>", "<ul>", "&nbsp;", "&lt;", "&gt;", "&amp;"} {
				if strings.Contains(md, leak) {
					t.Errorf("unconverted html %q left in output", leak)
				}
			}
			if !strings.Contains(md, "**Example 1:**") {
				t.Error("Example 1 heading missing")
			}
			if !strings.Contains(md, "Constraints") {
				t.Error("constraints section missing")
			}
			if strings.Contains(md, "10^") == false && strings.Contains(string(raw), "<sup>") {
				t.Error("statement had <sup> but no exponent survived")
			}
			if strings.Contains(md, "\n\n\n") {
				t.Error("output has a run of blank lines")
			}
		})
	}
}
