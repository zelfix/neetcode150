package content

import (
	"errors"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Doc is one Markdown file: its frontmatter, its body, and its HTML.
type Doc struct {
	Path        string
	Frontmatter map[string]string
	Body        string
	HTML        template.HTML // the whole document
	HTMLBody    template.HTML // the document without its leading H1
	ModTime     time.Time
	Missing     bool // the file does not exist yet
}

// Title is the frontmatter title, or the first heading, or "".
func (d *Doc) Title() string {
	if d == nil {
		return ""
	}
	if t := d.Frontmatter["title"]; t != "" {
		return t
	}
	for _, line := range strings.Split(d.Body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

// Empty reports whether the document has no usable content.
func (d *Doc) Empty() bool { return d == nil || d.Missing || strings.TrimSpace(d.Body) == "" }

// Store reads documents from disk, caching each by modification time so files
// written mid-session show up on the next page load without a restart.
type Store struct {
	root     string
	renderer *Renderer
	mu       sync.RWMutex
	cache    map[string]*Doc
}

// NewStore returns a Store rooted at the project directory.
func NewStore(root string, r *Renderer) *Store {
	return &Store{root: root, renderer: r, cache: map[string]*Doc{}}
}

// Renderer exposes the underlying Markdown renderer.
func (s *Store) Renderer() *Renderer { return s.renderer }

// Doc reads the Markdown file at a project-relative path. A missing file comes
// back as a Doc with Missing set, not as an error, because hints and notes are
// written as you go.
func (s *Store) Doc(relPath string) (*Doc, error) {
	full := filepath.Join(s.root, relPath)
	info, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Doc{Path: relPath, Missing: true, Frontmatter: map[string]string{}}, nil
		}
		return nil, err
	}

	s.mu.RLock()
	cached, ok := s.cache[relPath]
	s.mu.RUnlock()
	if ok && cached.ModTime.Equal(info.ModTime()) {
		return cached, nil
	}

	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	fm, body := SplitFrontmatter(string(raw))
	doc := &Doc{
		Path:        relPath,
		Frontmatter: fm,
		Body:        body,
		HTML:        s.renderer.MustRender(body),
		HTMLBody:    s.renderer.MustRender(StripLeadingH1(body)),
		ModTime:     info.ModTime(),
	}

	s.mu.Lock()
	s.cache[relPath] = doc
	s.mu.Unlock()
	return doc, nil
}

var leadingH1RE = regexp.MustCompile(`\A\s*#\s+[^\n]*\n+`)

// StripLeadingH1 removes a document's opening `# Title` line. Pages that
// render their own heading use this so the title does not appear twice.
func StripLeadingH1(body string) string {
	return leadingH1RE.ReplaceAllString(body, "")
}

var frontmatterRE = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// SplitFrontmatter separates a leading `---` block of `key: value` lines from
// the Markdown body. A file with no frontmatter comes back unchanged.
func SplitFrontmatter(raw string) (map[string]string, string) {
	fm := map[string]string{}
	m := frontmatterRE.FindStringSubmatch(raw)
	if m == nil {
		return fm, raw
	}
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fm[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return fm, strings.TrimLeft(raw[len(m[0]):], "\n")
}

// Hint is one rung of a problem's hint ladder.
type Hint struct {
	Title string
	HTML  template.HTML
}

var hintHeadingRE = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)

// Hints splits a hints document on its `## ` headings. Each heading becomes one
// rung, revealed on its own, and anything before the first heading is returned
// as an always-visible preamble.
func (s *Store) Hints(relPath string) (preamble template.HTML, hints []Hint, err error) {
	doc, err := s.Doc(relPath)
	if err != nil {
		return "", nil, err
	}
	if doc.Empty() {
		return "", nil, nil
	}

	matches := hintHeadingRE.FindAllStringSubmatchIndex(doc.Body, -1)
	if len(matches) == 0 {
		// No ladder structure: show the whole thing behind a single reveal.
		return "", []Hint{{Title: "Hint", HTML: doc.HTML}}, nil
	}
	if intro := strings.TrimSpace(doc.Body[:matches[0][0]]); intro != "" {
		preamble = s.renderer.MustRender(intro)
	}
	for i, m := range matches {
		end := len(doc.Body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		title := doc.Body[m[2]:m[3]]
		body := strings.TrimSpace(doc.Body[m[1]:end])
		hints = append(hints, Hint{Title: title, HTML: s.renderer.MustRender(body)})
	}
	return preamble, hints, nil
}
