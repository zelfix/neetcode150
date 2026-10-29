// Package content reads the Markdown under content/ and renders it to HTML.
package content

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"sync"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// highlightStyle is the Chroma style the code blocks are rendered with. It is
// emitted as a class-based stylesheet once, so pages carry no inline colours.
const highlightStyle = "github-dark"

// Renderer turns Markdown into HTML. It is safe for concurrent use.
type Renderer struct {
	md      goldmark.Markdown
	cssOnce sync.Once
	css     template.CSS
}

// NewRenderer builds a Markdown renderer with GFM, heading anchors and
// server-side syntax highlighting.
func NewRenderer() *Renderer {
	r := &Renderer{}
	r.md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			htmlrenderer.WithUnsafe(), // the content under content/ is ours
			renderer.WithNodeRenderers(util.Prioritized(&codeRenderer{}, 100)),
		),
	)
	return r
}

// Render converts Markdown to HTML.
func (r *Renderer) Render(md string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(md), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// MustRender converts Markdown to HTML, falling back to an escaped <pre> block
// if the Markdown cannot be rendered, so a bad file never blanks a page.
func (r *Renderer) MustRender(md string) template.HTML {
	out, err := r.Render(md)
	if err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(md) + "</pre>")
	}
	return out
}

// HighlightCSS is the stylesheet for the highlighted code blocks.
func (r *Renderer) HighlightCSS() template.CSS {
	r.cssOnce.Do(func() {
		style := styles.Get(highlightStyle)
		if style == nil {
			style = styles.Fallback
		}
		var buf bytes.Buffer
		formatter := chromahtml.New(chromahtml.WithClasses(true))
		if err := formatter.WriteCSS(&buf, style); err != nil {
			r.css = ""
			return
		}
		r.css = template.CSS(buf.String())
	})
	return r.css
}

// codeRenderer replaces goldmark's plain code block output with Chroma-
// highlighted markup.
type codeRenderer struct{}

func (c *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, c.renderFenced)
	reg.Register(ast.KindCodeBlock, c.renderPlain)
}

func (c *codeRenderer) renderFenced(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := string(n.Language(src))
	writeHighlighted(w, lang, lines(n, src))
	return ast.WalkSkipChildren, nil
}

func (c *codeRenderer) renderPlain(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	// An indented block with no language: the example I/O in a statement.
	writeHighlighted(w, "", lines(node, src))
	return ast.WalkSkipChildren, nil
}

// lines collects a code block's source text.
func lines(n ast.Node, src []byte) string {
	var b strings.Builder
	l := n.Lines()
	for i := range l.Len() {
		seg := l.At(i)
		b.Write(seg.Value(src))
	}
	return b.String()
}

// writeHighlighted emits a highlighted <pre> for source, or a plain escaped one
// when no lexer matches the language.
func writeHighlighted(w util.BufWriter, lang, source string) {
	// A block with no language is example I/O from a statement; highlighting
	// it as some guessed language adds noise rather than meaning.
	lexer := lexers.Get(lang)
	if lang == "" || lexer == nil {
		writePlain(w, lang, source)
		return
	}
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		writePlain(w, lang, source)
		return
	}
	style := styles.Get(highlightStyle)
	if style == nil {
		style = styles.Fallback
	}
	var buf bytes.Buffer
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	if err := formatter.Format(&buf, style, iterator); err != nil {
		writePlain(w, lang, source)
		return
	}
	fmt.Fprintf(w, `<div class="code" data-lang=%q>`, lang)
	w.WriteString(buf.String())
	w.WriteString("</div>\n")
}

func writePlain(w util.BufWriter, lang, source string) {
	fmt.Fprintf(w, `<div class="code code-plain" data-lang=%q><pre><code>`, lang)
	w.WriteString(template.HTMLEscapeString(source))
	w.WriteString("</code></pre></div>\n")
}
